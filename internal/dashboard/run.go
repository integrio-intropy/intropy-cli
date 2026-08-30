package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// Run supervision: the flow view's start/stop for one system's host, the one
// place this package launches a long-lived process. Everything else the
// server runs (graph verbs, deploy status) is short-lived and read-only; a
// run is neither, so the rules differ:
//
//   - The process is parented to the dashboard lifetime, never to the request
//     that started it. Serve calls the shutdownRuns func returned by
//     newHandler after the HTTP server drains; children do not outlive the
//     dashboard. If the dashboard itself is SIGKILLed the children are
//     orphaned holding their ports — accepted, not handled.
//   - Stopping kills the whole process group: `dotnet run` spawns the actual
//     host as a child, and killing only the dotnet process leaves the app
//     listening. This is Unix process-group semantics (Setpgid + a negative
//     pid) — supported platforms are macOS, Linux, and Windows through WSL;
//     a native Windows build fails to compile here, which is the intent.
//   - A host that exits on its own keeps its entry so the flow view can show
//     the exit error and the logs — the run panel is the only terminal a
//     dashboard-started host has.

// logLinesMax bounds the per-run log tail; logTailServed is how much of it a
// status response carries.
const (
	logLinesMax   = 1000
	logTailServed = 200
)

// stopGracePeriod is how long a stopped run has to exit after SIGTERM before
// SIGKILL.
const stopGracePeriod = 5 * time.Second

// starter launches one system's host and returns a handle to it. It is a
// field on apiServer — the same function-value seam the providers struct
// uses — so tests supervise fake processes instead of dotnet.
type starter func(ctx context.Context, hostDir string) (*runHandle, error)

// runHandle is one launched host: the process plus the log pump feeding its
// ring buffer.
type runHandle struct {
	cmd     *exec.Cmd
	pid     int
	started time.Time
	pump    *logPump
	// exited is closed when cmd.Wait returns; exit carries its result.
	exited chan struct{}
	exit   error
}

// systemRun is the map entry for one system: the live handle, plus the
// terminal state once the process has exited so GET can report it.
type systemRun struct {
	handle *runHandle
	err    error // non-nil once the process has exited on its own
}

// runStatus is the GET /api/run/{path} payload, returned by POST and DELETE
// as well so a button click lands on fresh state.
type runStatus struct {
	System    string   `json:"system"`
	Running   bool     `json:"running"`
	PID       int      `json:"pid,omitempty"`
	StartedAt string   `json:"startedAt,omitempty"`
	ExitError string   `json:"exitError,omitempty"`
	Logs      []string `json:"logs"`
}

// logPump is a bounded ring of the run's combined stdout/stderr, filled by a
// pumpLines goroutine off the process pipes.
type logPump struct {
	mu    sync.Mutex
	lines []string
}

func (p *logPump) add(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.lines) >= logLinesMax {
		p.lines = p.lines[1:]
	}
	p.lines = append(p.lines, line)
}

func (p *logPump) tail() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	lines := p.lines
	if len(lines) > logTailServed {
		lines = lines[len(lines)-logTailServed:]
	}
	out := make([]string, len(lines))
	copy(out, lines)
	return out
}

// dotnetStart is the real starter: `dotnet run --project <hostDir>`. Unlike
// RunGraph it keeps the default launch profile — a dev server wants the
// launchSettings ports and environment — and it lets run build, so the flow
// view's start picks up the code as edited.
//
// Dapr is deliberately not wired in: a host whose components need sidecars
// fails here, and the run panel's logs are where that failure shows up.
func dotnetStart(ctx context.Context, hostDir string) (*runHandle, error) {
	cmd := exec.CommandContext(ctx, "dotnet", "run", "--project", hostDir)
	setProcGroup(cmd)
	r, w := io.Pipe()
	cmd.Stdout, cmd.Stderr = w, w

	h := &runHandle{cmd: cmd, started: time.Now(), pump: &logPump{}, exited: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("start host: %w", err)
	}
	h.pid = cmd.Process.Pid
	go pumpLines(r, h.pump)
	go func() {
		h.exit = cmd.Wait()
		_ = w.Close()
		close(h.exited)
	}()
	return h, nil
}

// pumpLines copies complete lines from r into the pump until EOF (the process
// closed its end). Scanner-free: a Scanner's token limit would silently
// truncate a host's long log line, and a partial final line is still worth
// showing. The reader is closed when the copy ends.
func pumpLines(r io.ReadCloser, p *logPump) {
	defer r.Close()
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		for {
			i := indexByte(buf, '\n')
			if i < 0 {
				break
			}
			p.add(strings.TrimRight(string(buf[:i]), "\r"))
			buf = buf[i+1:]
		}
		if err != nil {
			if len(buf) > 0 {
				p.add(strings.TrimRight(string(buf), "\r"))
			}
			return
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// resolveRun maps the {path...} wildcard to (system dir, host dir): the
// workspace-root system arrives as "" (ServeMux redirects /api/run/. to
// /api/run/), which resolveSystemDir already accepts as the root. The host is
// rediscovered on every request rather than cached — a sys update or manual
// delete between start and stop must not strand the lookup.
func (s *apiServer) resolveRun(raw string) (sysDir, hostDir string, err error) {
	rel := strings.Trim(raw, "/")
	if rel == "" {
		rel = "."
	}
	sysDir, err = s.resolveSystemDir(rel)
	if err != nil {
		return "", "", err
	}
	hosts, _ := template.ListSystemHosts(sysDir)
	if len(hosts) == 0 {
		return "", "", fmt.Errorf("no system host under %s\ncreate one with the system's host sync or 'intropy sys create'", rel)
	}
	return sysDir, hosts[0].Path, nil
}

// statusOf renders the map entry (or its absence) as the API payload. exited
// overrides the entry's recorded error: a just-stopped entry was deleted
// before its watchRun could record the exit, and the stop response must not
// re-report the dead process as running.
func statusOf(sysDir string, run *systemRun, exited error) runStatus {
	st := runStatus{System: sysDir, Logs: []string{}}
	if run == nil {
		return st
	}
	err := run.err
	if err == nil {
		err = exited
	}
	if h := run.handle; h != nil {
		st.StartedAt = h.started.UTC().Format(time.RFC3339)
		st.Logs = h.pump.tail()
		if err == nil {
			st.Running = true
			st.PID = h.pid
			return st
		}
	}
	if err != nil && err != errStopped {
		st.ExitError = err.Error()
	}
	return st
}

// errStopped marks a deliberate stop: the process is gone, but there is no
// failure for the panel to shout about.
var errStopped = errors.New("stopped")

// getRun is GET /api/run/{path...}: what the dashboard last knew about one
// system's host process. A system with no entry and no host reports
// running:false — the run panel renders the start button disabled, same as
// the toolbar gating.
func (s *apiServer) getRun(w http.ResponseWriter, r *http.Request) {
	rel := strings.Trim(r.PathValue("path"), "/")
	if rel == "" {
		rel = "."
	}
	s.runMu.Lock()
	st := statusOf(rel, s.runs[rel], nil)
	s.runMu.Unlock()
	writeJSON(w, http.StatusOK, st)
}

// startRun is POST /api/run/{path...}: launch the system's host. A running
// entry is a 409; an exited one is replaced (restart). The launch holds
// createMu — the workspace-write mutex — so a host sync or template create
// cannot rewrite sources underneath the build `dotnet run` kicks off.
func (s *apiServer) startRun(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("path")
	_, hostDir, err := s.resolveRun(raw)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rel := strings.Trim(raw, "/")
	if rel == "" {
		rel = "."
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()

	s.runMu.Lock()
	if run := s.runs[rel]; run != nil && run.err == nil {
		s.runMu.Unlock()
		writeError(w, http.StatusConflict, "system already running: "+rel)
		return
	}
	s.runMu.Unlock()

	handle, err := s.start(context.Background(), hostDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.runMu.Lock()
	if s.runs == nil {
		s.runs = map[string]*systemRun{}
	}
	s.runs[rel] = &systemRun{handle: handle}
	s.runMu.Unlock()

	go s.watchRun(rel, handle)
	writeJSON(w, http.StatusOK, statusOf(rel, &systemRun{handle: handle}, nil))
}

// watchRun records a host's exit into its entry, unless the entry has moved
// on (stopped, or replaced by a restart) — a stale Wait result must not mark
// a fresh run exited.
func (s *apiServer) watchRun(rel string, h *runHandle) {
	<-h.exited
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if run := s.runs[rel]; run != nil && run.handle == h {
		run.err = exitError(h.exit)
	}
}

// stopRun is DELETE /api/run/{path...}: SIGTERM the process group, SIGKILL
// after the grace period, clear the entry. No entry at all is a 404; an
// already-exited entry is cleared and answered 200 — the run is gone either
// way, and the caller gets the exit error in the body.
func (s *apiServer) stopRun(w http.ResponseWriter, r *http.Request) {
	rel := strings.Trim(r.PathValue("path"), "/")
	if rel == "" {
		rel = "."
	}

	s.runMu.Lock()
	run := s.runs[rel]
	if run == nil {
		s.runMu.Unlock()
		writeError(w, http.StatusNotFound, "system not running: "+rel)
		return
	}
	delete(s.runs, rel)
	s.runMu.Unlock()

	if run.err == nil && run.handle != nil {
		stopHandle(run.handle)
	}
	// The entry is already deleted, so its watchRun never records the exit:
	// tell statusOf the process is gone. A deliberate stop is not an error —
	// a crashed run keeps its real exit error in the body.
	exited := errStopped
	if run.err != nil {
		exited = run.err
	}
	writeJSON(w, http.StatusOK, statusOf(rel, run, exited))
}

// stopHandle terminates a live run: SIGTERM the group, escalate to SIGKILL
// when the grace period lapses.
func stopHandle(h *runHandle) {
	if err := killProcessGroup(h.pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		// The group lookup itself failed (not just an already-dead process):
		// fall back to the direct child so stop still does something.
		_ = h.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-h.exited:
	case <-time.After(stopGracePeriod):
		_ = killProcessGroup(h.pid, syscall.SIGKILL)
		<-h.exited
	}
}

// shutdownRuns stops every supervised run; Serve calls it after the HTTP
// server drains. Snapshot under the mutex, stop outside it: a stop's
// escalation wait must not hold the map.
func (s *apiServer) shutdownRuns() {
	s.runMu.Lock()
	handles := make([]*runHandle, 0, len(s.runs))
	for rel, run := range s.runs {
		if run.err == nil && run.handle != nil {
			handles = append(handles, run.handle)
		}
		delete(s.runs, rel)
	}
	s.runMu.Unlock()
	for _, h := range handles {
		stopHandle(h)
	}
}

// exitError flattens a Wait result for the status payload: nil on a clean
// exit (a host the developer stopped with its own signal is not an error the
// panel should shout about), the message otherwise.
func exitError(err error) error {
	if err == nil {
		return errors.New("exited")
	}
	return err
}

// setProcGroup puts the launched host in its own process group so a stop can
// signal the group rather than only the dotnet process. Unix-only by design:
// supported platforms are macOS, Linux, and Windows through WSL (which is a
// Linux kernel to the toolchain). Native Windows has no Setpgid and no
// negative-pid group kill, so a GOOS=windows build fails to compile — the
// loud refusal a silently-degraded fallback would hide.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup signals every process in the group pid leads.
func killProcessGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}
