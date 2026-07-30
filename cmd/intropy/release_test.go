package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/release"
)

// resetReleaseState restores the flag-backing globals between tests. These
// tests execute the real rootCmd in-process, so without this a flag set by one
// test leaks into the next.
func resetReleaseState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetRootIO(t, stdout, stderr)
	reset := func() {
		releaseCreateOpts = releaseCreateFlags{output: release.OutputPlain}
		releaseShowOpts = releaseShowFlags{output: release.OutputPlain}
		releaseListOpts = releaseListFlags{output: release.OutputPlain}
	}
	reset()
	t.Cleanup(reset)
}

func runRelease(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	resetReleaseState(t, stdout, stderr)
	rootCmd.SetArgs(append([]string{"release"}, args...))
	return stdout, stderr, rootCmd.Execute()
}

func TestReleaseCreateRequiresComponent(t *testing.T) {
	_, _, err := runRelease(t, "create", "--version", "1.4.2")
	if err == nil {
		t.Fatal("expected an error when no component is given")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestReleaseCreateRequiresVersion(t *testing.T) {
	_, _, err := runRelease(t, "create", "order-extractor")
	if err == nil {
		t.Fatal("expected an error when --version is missing")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestReleaseShowRequiresComponentAndVersion(t *testing.T) {
	for _, args := range [][]string{
		{"show"},
		{"show", "order-extractor"},
		{"show", "order-extractor", "1.4.2", "extra"},
	} {
		_, _, err := runRelease(t, args...)
		if err == nil {
			t.Errorf("release %v should be a usage error", args)
			continue
		}
		if code := exitCode(err); code != 2 {
			t.Errorf("release %v: exit code = %d, want 2", args, code)
		}
	}
}

func TestReleaseListTakesExactlyOneComponent(t *testing.T) {
	for _, args := range [][]string{
		{"list"},
		{"list", "order-extractor", "1.4.2"},
	} {
		_, _, err := runRelease(t, args...)
		if err == nil {
			t.Errorf("release %v should be a usage error", args)
			continue
		}
		if code := exitCode(err); code != 2 {
			t.Errorf("release %v: exit code = %d, want 2", args, code)
		}
	}
}

func TestReleaseRejectsUnknownOutputFormat(t *testing.T) {
	for _, args := range [][]string{
		{"create", "order-extractor", "--version", "1.4.2", "--output", "yaml"},
		{"show", "order-extractor", "1.4.2", "--output", "yaml"},
		{"list", "order-extractor", "--output", "yaml"},
	} {
		_, _, err := runRelease(t, args...)
		if err == nil {
			t.Errorf("release %v should reject the output format", args)
			continue
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("error %q should be a usageError", err)
		}
		if !strings.Contains(err.Error(), "json") {
			t.Errorf("error %q should list the supported formats", err)
		}
	}
}

func TestReleaseAcceptsBothOutputFormats(t *testing.T) {
	// These fail later, on configuration — the point is that the flag itself
	// is accepted and does not produce a usage error.
	for _, format := range []string{release.OutputPlain, release.OutputJSON} {
		_, _, err := runRelease(t, "create", "order-extractor", "--version", "1.4.2", "--output", format)
		var ue *usageError
		if errors.As(err, &ue) {
			t.Errorf("--output %s should be accepted, got usage error %q", format, err)
		}
	}
}

// Cobra runs only the closest PersistentPreRunE, so one here would shadow the
// root's and silently break -C/--directory for these commands alone.
func TestReleaseDoesNotShadowRootPersistentPreRun(t *testing.T) {
	if releaseCmd.PersistentPreRunE != nil || releaseCmd.PersistentPreRun != nil {
		t.Error("release must not define PersistentPreRunE; it would shadow the root's and break -C")
	}
	for _, c := range releaseCmd.Commands() {
		if c.PersistentPreRunE != nil || c.PersistentPreRun != nil {
			t.Errorf("release %s must not define PersistentPreRunE", c.Name())
		}
	}
}

func TestReleaseIsRegistered(t *testing.T) {
	var parent bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "release" {
			parent = true
		}
	}
	if !parent {
		t.Fatal("release is not registered on the root command")
	}

	want := map[string]bool{"create": false, "show": false, "list": false}
	for _, c := range releaseCmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("release %s is not registered", name)
		}
	}
}
