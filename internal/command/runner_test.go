package command

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeRunner records calls and replays scripted results, keyed by the command
// line joined with spaces.
type fakeRunner struct {
	calls   []string
	results map[string]fakeResult
	def     fakeResult
}

type fakeResult struct {
	stdout string
	stderr string
	err    error
}

func (f *fakeRunner) Run(_ context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, key)
	res, ok := f.results[key]
	if !ok {
		res = f.def
	}
	return []byte(res.stdout), []byte(res.stderr), res.err
}

func (f *fakeRunner) called(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestExecRunnerCapturesOutput(t *testing.T) {
	stdout, stderr, err := ExecRunner{}.Run(context.Background(), "", "sh", "-c", "printf out; printf err >&2")
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != "out" {
		t.Errorf("stdout = %q, want %q", stdout, "out")
	}
	if string(stderr) != "err" {
		t.Errorf("stderr = %q, want %q", stderr, "err")
	}
}

// A failing command must surface the child's own stderr: git and kustomize
// both explain themselves better than any paraphrase would.
func TestExecRunnerExitErrorCarriesStderr(t *testing.T) {
	_, _, err := ExecRunner{}.Run(context.Background(), "", "sh", "-c", "echo 'something specific' >&2; exit 3")
	if err == nil {
		t.Fatal("expected an error")
	}
	ee, ok := errors.AsType[*ExitError](err)
	if !ok {
		t.Fatalf("error %v is not an *ExitError", err)
	}
	if ee.Code != 3 {
		t.Errorf("Code = %d, want 3", ee.Code)
	}
	if !strings.Contains(err.Error(), "something specific") {
		t.Errorf("error %q should carry the child's stderr", err)
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	_, _, err := ExecRunner{}.Run(context.Background(), "", "definitely-not-a-real-binary-xyz")
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("error %v should be ErrNotInstalled", err)
	}
}

// Cancelling the context must report the interruption, not the subprocess's
// incidental "signal: killed" — the exit-code mapping depends on seeing
// context.Canceled.
func TestExecRunnerCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := ExecRunner{}.Run(ctx, "", "sh", "-c", "sleep 5")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v should be context.Canceled", err)
	}
}

func TestRequireBinaries(t *testing.T) {
	if err := RequireBinaries("sh"); err != nil {
		t.Errorf("RequireBinaries(sh) = %v, want nil", err)
	}

	err := RequireBinaries("definitely-not-a-real-binary-xyz")
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("error %v should be ErrNotInstalled", err)
	}
	if !strings.Contains(err.Error(), "definitely-not-a-real-binary-xyz") {
		t.Errorf("error %q should name the binary", err)
	}

	// Both missing binaries are reported at once, so a user installs
	// everything in one go instead of discovering the second only after
	// fixing the first.
	err = RequireBinaries("definitely-not-a-real-binary-xyz", "also-not-real-abc")
	if !strings.Contains(err.Error(), "also-not-real-abc") || !strings.Contains(err.Error(), "definitely-not-a-real-binary-xyz") {
		t.Errorf("error %q should name both missing binaries", err)
	}
}

func TestNotInstalledErrorIncludesHint(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err == nil {
		t.Skip("kustomize is installed, cannot exercise the hint")
	}
	err := RequireBinaries("kustomize")
	if !strings.Contains(err.Error(), "brew install kustomize") {
		t.Errorf("error %q should hint at how to install kustomize", err)
	}
}

// Env exists so a caller can pin behaviour a user's shell would otherwise decide —
// git's terminal prompting above all, which would wait for input on a tty whose
// output this package captures.
func TestExecRunnerEnvReachesTheChild(t *testing.T) {
	r := ExecRunner{Env: []string{"INTROPY_TEST_ENV=set-by-runner"}}
	stdout, _, err := r.Run(context.Background(), t.TempDir(), "sh", "-c", `printf %s "$INTROPY_TEST_ENV"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(stdout); got != "set-by-runner" {
		t.Errorf("child saw %q", got)
	}
}

// The parent environment has to survive, or git loses the PATH, SSH agent and
// credential configuration it needs to work at all.
func TestExecRunnerEnvKeepsTheParentEnvironment(t *testing.T) {
	t.Setenv("INTROPY_TEST_PARENT", "inherited")
	r := ExecRunner{Env: []string{"INTROPY_TEST_ENV=set-by-runner"}}
	stdout, _, err := r.Run(context.Background(), t.TempDir(), "sh", "-c", `printf %s "$INTROPY_TEST_PARENT"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(stdout); got != "inherited" {
		t.Errorf("child saw %q, want the parent's value", got)
	}
}
