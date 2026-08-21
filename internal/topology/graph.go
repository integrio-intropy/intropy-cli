package topology

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GraphTimeout bounds one host's graph verb. It is generous because the first
// `dotnet run` restores and builds the project before the verb executes.
const GraphTimeout = 5 * time.Minute

// stderrTailBytes bounds how much of a failing host's output is echoed into the
// error message.
const stderrTailBytes = 2048

// RunGraph executes one system host's graph verb and decodes its stdout.
//
// It builds first and then runs with --no-build --no-launch-profile — the
// invocation the topology library documents — because a plain `dotnet run`
// writes build and launch-profile messages to stdout, corrupting the JSON.
//
// Callers should say what they are doing before calling: on a cold project this
// can take minutes, and a silent wait that long reads as a hang.
func RunGraph(ctx context.Context, hostDir string) (*Topology, error) {
	ctx, cancel := context.WithTimeout(ctx, GraphTimeout)
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
	t, err := Decode(&stdout)
	if err != nil {
		// The stdout tail, not stderr: a decode failure means the verb printed
		// something other than the record, and that something is the evidence.
		return nil, fmt.Errorf("%w%s", err, tail(stdout.Bytes()))
	}
	return t, nil
}

// tail formats the last stderrTailBytes of a command stream for inclusion in an
// error message, or nothing when the stream is empty.
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
