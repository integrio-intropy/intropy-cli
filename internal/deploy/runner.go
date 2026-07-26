// Package deploy pins a component's image digest into one environment's
// kustomize overlay in a GitOps repository, commits the change, and waits for
// ArgoCD to converge on it.
//
// The GitOps repository is laid out by coordinate:
//
//	<repo>/deploy.yaml
//	<repo>/domains/<domain>/<system>/<component>/component.yaml
//	<repo>/domains/<domain>/<system>/<component>/base/
//	<repo>/domains/<domain>/<system>/<component>/overlays/<env>/
//
// which mirrors the ApplicationSet git-directory generator that turns each
// overlay into an ArgoCD Application named <domain>-<system>-<component>-<env>.
package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs an external command. It exists so the git and kustomize call
// sites can be tested without either binary present, and so every subprocess
// in this package is context-aware by construction.
type Runner interface {
	// Run executes name with args in dir, returning its captured stdout and
	// stderr. A non-zero exit is an error; stdout and stderr are still
	// returned so callers can inspect them.
	Run(ctx context.Context, dir, name string, args ...string) (stdout, stderr []byte, err error)
}

// ErrNotInstalled reports that a required external binary is not on PATH. It
// maps to exit code 127.
var ErrNotInstalled = errors.New("not installed")

// NotInstalledError names the missing binary and how to get it.
type NotInstalledError struct {
	Binary string
	Hint   string
}

func (e *NotInstalledError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s is not installed or not on PATH (%s)", e.Binary, e.Hint)
	}
	return fmt.Sprintf("%s is not installed or not on PATH", e.Binary)
}

func (e *NotInstalledError) Unwrap() error { return ErrNotInstalled }

// ExitError reports a command that ran and failed. Stderr is carried verbatim
// because git and kustomize both explain themselves well and paraphrasing
// their messages loses detail.
type ExitError struct {
	Name   string
	Args   []string
	Dir    string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	cmd := e.Name + " " + strings.Join(e.Args, " ")
	if msg == "" {
		return fmt.Sprintf("%s: exit status %d", cmd, e.Code)
	}
	return fmt.Sprintf("%s: %s", cmd, msg)
}

// ExecRunner runs commands as subprocesses.
type ExecRunner struct{}

// Run implements Runner using exec.CommandContext, so cancelling ctx kills the
// child. That matters here: this package shells out to `git push` over SSH and
// can sit in an ArgoCD poll for minutes, and with a context-free exec.Command a
// Ctrl-C would cancel the Go side while the subprocess kept running.
func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// A cancelled context is the user interrupting; report that rather
		// than the subprocess's incidental "signal: killed".
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stdout.Bytes(), stderr.Bytes(), ctxErr
		}
		if errors.Is(err, exec.ErrNotFound) {
			return stdout.Bytes(), stderr.Bytes(), &NotInstalledError{Binary: name}
		}
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return stdout.Bytes(), stderr.Bytes(), &ExitError{
				Name:   name,
				Args:   args,
				Dir:    dir,
				Code:   ee.ExitCode(),
				Stderr: stderr.String(),
			}
		}
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("run %s: %w", name, err)
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

// RequireBinaries checks that every named binary is on PATH, so a run fails
// immediately with one clear message naming all of them rather than part-way
// through after the worktree has already been mutated.
func RequireBinaries(names ...string) error {
	var missing []string
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			missing = append(missing, n)
		}
	}
	switch len(missing) {
	case 0:
		return nil
	case 1:
		return &NotInstalledError{Binary: missing[0], Hint: binaryHints[missing[0]]}
	default:
		return fmt.Errorf("%w: %s", ErrNotInstalled, strings.Join(missing, ", "))
	}
}

var binaryHints = map[string]string{
	"git":       "install git",
	"kustomize": "install kustomize, e.g. brew install kustomize",
}
