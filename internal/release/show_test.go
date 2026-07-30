//go:build integration

package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func (f *createFixture) show(t *testing.T, version, format string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Show(context.Background(), Options{
		Component:    "order-extractor",
		Version:      version,
		CacheRoot:    f.cacheRoot,
		OutputFormat: format,
		Stdout:       &stdout,
		Stderr:       &stderr,
	})
	return stdout.String(), err
}

func TestShowRendersAPublishedRelease(t *testing.T) {
	f := newCreateFixture(t)
	f.create(t, "1.0.0")
	f.commitAndPush(t, "b.cs", "Handle empty payloads")
	r, _ := f.create(t, "1.0.1")

	out, err := f.show(t, "1.0.1", OutputPlain)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"order-extractor",
		"1.0.1",
		r.Manifest.Source.Commit,
		r.Manifest.Images[0].Digest,
		"Handle empty payloads",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output should contain %q:\n%s", want, out)
		}
	}
	// The basis is what makes the notes interpretable.
	if !strings.Contains(out, "since 1.0.0") {
		t.Errorf("output should say what the notes are measured against:\n%s", out)
	}
}

// An initial release must say why it has no changes, not show a blank space.
func TestShowExplainsAnInitialRelease(t *testing.T) {
	f := newCreateFixture(t)
	f.create(t, "1.0.0")

	out, err := f.show(t, "1.0.0", OutputPlain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "initial release") {
		t.Errorf("output should explain there was no predecessor:\n%s", out)
	}
	if !strings.Contains(out, InitialNotes) {
		t.Errorf("output should carry the initial-release notes:\n%s", out)
	}
}

// JSON is the manifest as stored, so it can be compared with what a later
// deploy resolves.
func TestShowJSONIsTheManifest(t *testing.T) {
	f := newCreateFixture(t)
	created, _ := f.create(t, "1.0.0")

	out, err := f.show(t, "1.0.0", OutputJSON)
	if err != nil {
		t.Fatal(err)
	}

	var got Manifest
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("show output is not a manifest: %v\n%s", err, out)
	}
	if !got.SameRelease(created.Manifest) {
		t.Errorf("show returned a different release:\n got %+v\nwant %+v", got, created.Manifest)
	}
}

func TestShowRejectsManifestWhoseVersionDiffersFromRequestedTag(t *testing.T) {
	f := newCreateFixture(t)
	created, _ := f.create(t, "1.0.0")

	// Publish a valid manifest under another OCI tag. Show must not present a
	// retagged artifact as the release version its caller requested.
	wrong := *created.Manifest
	wrong.Version = "1.0.1"
	requested := "1.0.0-retagged"
	if _, err := Push(context.Background(), f.reg, Ref(ReleasesRepo(f.image), requested), &wrong); err != nil {
		t.Fatal(err)
	}

	_, err := f.show(t, requested, OutputPlain)
	if err == nil {
		t.Fatal("a manifest whose version differs from its requested tag must be refused")
	}
	for _, want := range []string{requested, wrong.Version} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%v", want, err)
		}
	}
}

func TestShowMissingReleaseIsNotFound(t *testing.T) {
	f := newCreateFixture(t)

	_, err := f.show(t, "9.9.9", OutputPlain)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error %v should be ErrNotFound", err)
	}
}

// Show refreshes only its local GitOps cache. It must not change the source
// repository, the GitOps remote, or any environment.
func TestShowDoesNotChangeSourceOrGitopsRemote(t *testing.T) {
	f := newCreateFixture(t)
	f.create(t, "1.0.0")

	gitopsBefore := headOf(t, f.gitopsOrigin)
	sourceBefore := headOf(t, f.sourceDir)

	if _, err := f.show(t, "1.0.0", OutputPlain); err != nil {
		t.Fatal(err)
	}

	if got := headOf(t, f.gitopsOrigin); got != gitopsBefore {
		t.Error("show moved the GitOps repository")
	}
	if got := headOf(t, f.sourceDir); got != sourceBefore {
		t.Error("show moved the source repository")
	}
}
