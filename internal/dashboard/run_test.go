package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// runAPI builds a handler plus its apiServer so a test can swap the starter
// for a fake process; the cleanup stops anything started.
func runAPI(t *testing.T, root string) (http.Handler, *apiServer, func()) {
	t.Helper()
	h, api, err := newHandler(root, "test", providers{topology: emptyTopo, deploy: emptyDeploy})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return h, api, api.shutdownRuns
}

func TestRunStartStatusStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake starter uses unix sleep")
	}
	root := t.TempDir()
	writeHostRecord(t, filepath.Join(root, "acme", "erp", "erp-host"), "erp", "")
	h, s, shutdown := runAPI(t, root)
	defer shutdown()
	s.start = fakeStart(t, "sleep", "60")

	rec := post(t, h, "/api/run/acme/erp")
	if rec.Code != http.StatusOK {
		t.Fatalf("start: status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var st runStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.PID == 0 {
		t.Errorf("start status = %+v, want running with a pid", st)
	}

	rec = get(t, h, "/api/run/acme/erp")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Running {
		t.Errorf("GET reports running=false for a live run: %+v", st)
	}

	rec = del(t, h, "/api/run/acme/erp")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop: status = %d, want 200: %s", rec.Code, rec.Body)
	}
	// Fresh variable per decode: encoding/json leaves omitted (omitempty)
	// fields at their prior values, so reusing st would carry the dead PID.
	var stopped runStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &stopped); err != nil {
		t.Fatal(err)
	}
	if stopped.Running || stopped.PID != 0 {
		t.Errorf("stop response = %+v, want not running and no pid", stopped)
	}

	rec = get(t, h, "/api/run/acme/erp")
	var after runStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Running || after.PID != 0 || after.ExitError != "" {
		t.Errorf("GET after stop = %+v, want the empty state", after)
	}
}

func TestRunDoubleStartConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake starter uses unix sleep")
	}
	root := t.TempDir()
	writeHostRecord(t, filepath.Join(root, "acme", "erp", "erp-host"), "erp", "")
	h, s, shutdown := runAPI(t, root)
	defer shutdown()
	s.start = fakeStart(t, "sleep", "60")

	if rec := post(t, h, "/api/run/acme/erp"); rec.Code != http.StatusOK {
		t.Fatalf("first start: %d: %s", rec.Code, rec.Body)
	}
	rec := post(t, h, "/api/run/acme/erp")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second start: status = %d, want 409: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "already running") {
		t.Errorf("409 body = %s, want the already-running message", rec.Body)
	}
}

func TestRunStartWithoutHost(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme", "crm"), 0o755); err != nil {
		t.Fatal(err)
	}
	h, _, shutdown := runAPI(t, root)
	defer shutdown()

	rec := post(t, h, "/api/run/acme/crm")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "no system host") {
		t.Errorf("body = %s, want the no-host message", rec.Body)
	}
}

func TestRunStopWithoutEntry(t *testing.T) {
	root := t.TempDir()
	h, _, shutdown := runAPI(t, root)
	defer shutdown()

	rec := del(t, h, "/api/run/acme/erp")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "not running") {
		t.Errorf("body = %s, want the not-running message", rec.Body)
	}
}

func TestRunCrashReportedAndRestarted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake starter uses unix false")
	}
	root := t.TempDir()
	writeHostRecord(t, filepath.Join(root, "acme", "erp", "erp-host"), "erp", "")
	h, s, shutdown := runAPI(t, root)
	defer shutdown()

	s.start = fakeStart(t, "false")
	if rec := post(t, h, "/api/run/acme/erp"); rec.Code != http.StatusOK {
		t.Fatalf("start: %d: %s", rec.Code, rec.Body)
	}

	// The watchRun goroutine records the exit; poll rather than sleep a
	// fixed interval.
	var st runStatus
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := get(t, h, "/api/run/acme/erp")
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		if !st.Running && st.ExitError != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("crash never surfaced in status: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(st.ExitError, "exit status") {
		t.Errorf("exitError = %q, want the process's exit status", st.ExitError)
	}

	// POST on the crashed entry restarts rather than conflicting.
	s.start = fakeStart(t, "sleep", "60")
	rec := post(t, h, "/api/run/acme/erp")
	if rec.Code != http.StatusOK {
		t.Fatalf("restart: status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Running {
		t.Errorf("restart status = %+v, want running", st)
	}
}

func TestRunDeleteAfterCrashClears(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake starter uses unix false")
	}
	root := t.TempDir()
	writeHostRecord(t, filepath.Join(root, "acme", "erp", "erp-host"), "erp", "")
	h, s, shutdown := runAPI(t, root)
	defer shutdown()

	s.start = fakeStart(t, "false")
	if rec := post(t, h, "/api/run/acme/erp"); rec.Code != http.StatusOK {
		t.Fatalf("start: %d: %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var st runStatus
		if err := json.Unmarshal(get(t, h, "/api/run/acme/erp").Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		if !st.Running && st.ExitError != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("crash never surfaced")
		}
		time.Sleep(20 * time.Millisecond)
	}

	rec := del(t, h, "/api/run/acme/erp")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete crashed run: status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var st runStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.ExitError == "" {
		t.Errorf("delete body = %+v, want the exit error carried through", st)
	}
	// And the entry is gone: the next delete is the 404 path.
	if rec := del(t, h, "/api/run/acme/erp"); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d, want 404", rec.Code)
	}
}

func TestRunLogsCaptured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake starter uses unix echo")
	}
	root := t.TempDir()
	writeHostRecord(t, filepath.Join(root, "acme", "erp", "erp-host"), "erp", "")
	h, s, shutdown := runAPI(t, root)
	defer shutdown()

	s.start = fakeStart(t, "sh", "-c", "echo hello-from-host; echo err-from-host >&2; sleep 60")
	if rec := post(t, h, "/api/run/acme/erp"); rec.Code != http.StatusOK {
		t.Fatalf("start: %d: %s", rec.Code, rec.Body)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var st runStatus
		if err := json.Unmarshal(get(t, h, "/api/run/acme/erp").Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		if len(st.Logs) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("logs never arrived: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var st runStatus
	if err := json.Unmarshal(get(t, h, "/api/run/acme/erp").Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(st.Logs, "\n")
	if !strings.Contains(joined, "hello-from-host") || !strings.Contains(joined, "err-from-host") {
		t.Errorf("logs = %q, want both the stdout and the stderr line", joined)
	}
}

func TestRunWorkspaceRootAddressing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake starter uses unix sleep")
	}
	root := t.TempDir()
	writeHostRecord(t, filepath.Join(root, "root-host"), "root", "")
	h, s, shutdown := runAPI(t, root)
	defer shutdown()
	s.start = fakeStart(t, "sleep", "60")

	// The browser-facing paths for the workspace-root system: ServeMux
	// redirects /api/run/. to /api/run/ with an empty wildcard, which the
	// handlers normalize to ".". httptest bypasses ServeMux's redirect
	// follow-through, so address the canonical trailing-slash form directly.
	for _, path := range []string{"/api/run/", "/api/run/."} {
		rec := post(t, h, path)
		if rec.Code == http.StatusTemporaryRedirect {
			continue // the "." form: ServeMux answered the redirect itself
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: status = %d, want 200: %s", path, rec.Code, rec.Body)
		}
		var st runStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		if st.System != "." || !st.Running {
			t.Errorf("POST %s: status = %+v, want system \".\" running", path, st)
		}
	}
	// Exactly one run exists: the two posts name the same system, so the
	// second was either the redirect or a 409 that skipped state assertion
	// above — stop via the canonical identifier and confirm.
	rec := del(t, h, "/api/run/")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop root system: %d: %s", rec.Code, rec.Body)
	}
}

// fakeStart returns a starter that supervises the given command instead of
// dotnet: a real child process with log capture, so stop, crash detection and
// the log tail all exercise the production machinery.
func fakeStart(t *testing.T, name string, args ...string) starter {
	t.Helper()
	return func(ctx context.Context, hostDir string) (*runHandle, error) {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s not on PATH", name)
		}
		cmd := exec.CommandContext(ctx, name, args...)
		setProcGroup(cmd)
		r, w, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("pipe: %w", err)
		}
		cmd.Stdout, cmd.Stderr = w, w
		h := &runHandle{cmd: cmd, started: time.Now(), pump: &logPump{}, exited: make(chan struct{})}
		if err := cmd.Start(); err != nil {
			_ = w.Close()
			return nil, fmt.Errorf("start fake: %w", err)
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
}

func del(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	return rec
}
