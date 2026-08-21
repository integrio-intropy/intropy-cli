//go:build integration

package kustomize

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
)

// writeOverlay writes a kustomization file with the given trailing block.
func writeOverlay(t *testing.T, dir, extra string) {
	t.Helper()
	body := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../../base\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadKustomization(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	dir := t.TempDir()
	writeOverlay(t, dir,
		"images:\n  - name: "+image+"\n    newTag: v1.2.3\ncommonAnnotations:\n  "+AnnotationSourceCommit+": deadbeef\n")

	k, path, err := ReadKustomization(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "kustomization.yaml" {
		t.Errorf("path = %q", path)
	}
	img, ok := k.FindImage(image)
	if !ok {
		t.Fatalf("FindImage(%s) not found in %+v", image, k.Images)
	}
	if img.NewTag != "v1.2.3" || img.Pinned() != ":v1.2.3" {
		t.Errorf("image = %+v, Pinned() = %q", img, img.Pinned())
	}
	if k.CommonAnnotations[AnnotationSourceCommit] != "deadbeef" {
		t.Errorf("CommonAnnotations = %v", k.CommonAnnotations)
	}
}

// The round trip promote depends on: a version written here has to be readable
// back, and removable, through the kustomize binary rather than a hand edit.
func TestSetAndRemoveTheReleaseAnnotation(t *testing.T) {
	requireKustomize(t)

	dir := t.TempDir()
	writeOverlay(t, dir, "commonAnnotations:\n  "+AnnotationSourceCommit+": deadbeef\n")
	k := Client{Runner: command.ExecRunner{}}
	ctx := context.Background()

	if err := k.SetAnnotation(ctx, dir, AnnotationRelease, "1.4.2"); err != nil {
		t.Fatal(err)
	}
	got, _, err := ReadKustomization(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.CommonAnnotations[AnnotationRelease] != "1.4.2" {
		t.Fatalf("CommonAnnotations = %v", got.CommonAnnotations)
	}

	if err := k.RemoveAnnotation(ctx, dir, AnnotationRelease); err != nil {
		t.Fatal(err)
	}
	got, _, err = ReadKustomization(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := got.CommonAnnotations[AnnotationRelease]; found {
		t.Errorf("release annotation should be gone, got %v", got.CommonAnnotations)
	}
	// Removing one annotation must not take the other with it.
	if got.CommonAnnotations[AnnotationSourceCommit] != "deadbeef" {
		t.Errorf("source-commit annotation was lost: %v", got.CommonAnnotations)
	}
}

func requireKustomize(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize is not installed")
	}
}

func TestKustomizationPathMissing(t *testing.T) {
	if _, err := KustomizationPath(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory with no kustomization file")
	}
}

func TestKustomizeImagePinned(t *testing.T) {
	cases := []struct {
		img  KustomizeImage
		want string
	}{
		{KustomizeImage{Digest: "sha256:abc"}, "sha256:abc"},
		{KustomizeImage{NewTag: "1.2.3"}, ":1.2.3"},
		{KustomizeImage{}, "(unpinned)"},
		// A digest wins over a tag, matching how kustomize resolves them.
		{KustomizeImage{Digest: "sha256:abc", NewTag: "1.2.3"}, "sha256:abc"},
	}
	for _, tc := range cases {
		if got := tc.img.Pinned(); got != tc.want {
			t.Errorf("Pinned(%+v) = %q, want %q", tc.img, got, tc.want)
		}
	}
}
