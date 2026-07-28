package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// topologyProvider fetches the declared topologies for the workspace. Each
// entry's Path is the on-disk system directory (the handler normalizes it to
// a root-relative identifier). Per-host failures are returned as messages
// alongside the successes so one broken host cannot hide the rest.
type topologyProvider func(ctx context.Context) (entries []topology.Entry, errs []string)

// graphTimeout bounds one host's graph verb. It is generous because the first
// `dotnet run` restores and builds the project before the verb executes.
const graphTimeout = 5 * time.Minute

// stderrTailBytes bounds how much of a failing host's stderr is echoed into
// the error message shown in the dashboard.
const stderrTailBytes = 2048

// hostGraphProvider returns the default provider: it finds every scaffolded
// system host under root and asks each one to print its declared topology via
// `dotnet run --project <host> -- graph`. The verb's contract is JSON only on
// stdout (logs on stderr); hosts scaffolded before the graph verb existed
// simply fail, which surfaces as an error the UI renders.
func hostGraphProvider(root string) topologyProvider {
	return func(ctx context.Context) ([]topology.Entry, []string) {
		hosts, _ := template.ListScaffolds(root)
		var entries []topology.Entry
		var errs []string
		for _, h := range hosts {
			if h.Role != template.RoleSystemHost {
				continue
			}
			t, err := runGraphVerb(ctx, h.Path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", displayPath(root, h.Path), err))
				continue
			}
			// The system directory is the workspace directory holding the
			// host: components are scaffolded as its siblings.
			entries = append(entries, topology.Entry{Path: filepath.Dir(h.Path), Topology: *t})
		}
		return entries, errs
	}
}

// runGraphVerb executes one host's graph verb and decodes its stdout. It
// builds first and then runs with --no-build --no-launch-profile — the
// invocation the topology library documents — because a plain `dotnet run`
// writes build and launch-profile messages to stdout, corrupting the JSON.
func runGraphVerb(ctx context.Context, hostDir string) (*topology.Topology, error) {
	ctx, cancel := context.WithTimeout(ctx, graphTimeout)
	defer cancel()

	build := exec.CommandContext(ctx, "dotnet", "build", hostDir, "-v", "q")
	var buildOut bytes.Buffer
	build.Stdout, build.Stderr = &buildOut, &buildOut
	if err := build.Run(); err != nil {
		return nil, fmt.Errorf("build failed: %v%s", err, tail(buildOut.Bytes()))
	}

	cmd := exec.CommandContext(ctx, "dotnet", "run", "--no-build", "--no-launch-profile", "--project", hostDir, "--", "graph")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("graph verb failed: %v%s", err, tail(stderr.Bytes()))
	}
	t, err := topology.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("%w%s", err, tail(stdout.Bytes()))
	}
	return t, nil
}

// displayPath shortens a host directory to its root-relative form for error
// messages, matching the identifier space the rest of the API uses.
func displayPath(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Clean(p))
}

// tail formats the last stderrTailBytes of a command stream for inclusion in
// an error message, or nothing when the stream is empty.
func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if len(s) > stderrTailBytes {
		s = "…" + s[len(s)-stderrTailBytes:]
	}
	return ": " + s
}
