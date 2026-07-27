// Package gittest builds real git repositories for tests.
//
// Real git rather than a fake: clone, fetch, reset and ancestry are exactly the
// operations where a mock encodes our assumptions instead of git's behaviour.
// The git binary is available wherever this repository's tests run.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// NewRepo creates a repository with one commit on the given branch and returns
// its path.
func NewRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	Init(t, dir, branch)
	WriteFile(t, filepath.Join(dir, "README.md"), "hello\n")
	Run(t, dir, "add", ".")
	Run(t, dir, "commit", "--quiet", "-m", "initial")
	return dir
}

// Init initialises an empty repository with the identity and settings tests
// need: commit signing off, and pushes to the checked-out branch permitted so a
// non-bare directory can serve as an origin.
func Init(t *testing.T, dir, branch string) {
	t.Helper()
	Run(t, dir, "init", "--quiet", "--initial-branch="+branch)
	Run(t, dir, "config", "user.email", "test@example.com")
	Run(t, dir, "config", "user.name", "Test")
	Run(t, dir, "config", "commit.gpgsign", "false")
	Run(t, dir, "config", "receive.denyCurrentBranch", "ignore")
}

// Run executes git in dir, failing the test on error.
func Run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Commit writes a file and commits it.
func Commit(t *testing.T, dir, path, content, message string) {
	t.Helper()
	WriteFile(t, filepath.Join(dir, path), content)
	Run(t, dir, "add", ".")
	Run(t, dir, "commit", "--quiet", "-m", message)
}

// WriteFile writes content to path, creating parent directories.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ReadFile reads path, failing the test on error.
func ReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// HEAD returns the repository's current commit sha.
func HEAD(t *testing.T, dir string) string {
	t.Helper()
	return Run(t, dir, "rev-parse", "HEAD")
}
