package release

import (
	"context"
	"encoding/json"
	"fmt"
)

// View reads a published release manifest and renders it.
//
// It makes no source-repository, GitOps-remote, or environment change. It does
// refresh the local cached GitOps checkout to locate the component metadata;
// that cache is an implementation detail, not deployment state. View exists so
// generated notes can be sanity-checked before anything is deployed from them.
func View(ctx context.Context, opts Options) error {
	opts.applyDefaults()

	if opts.Version == "" {
		return fmt.Errorf("a version is required")
	}

	t, err := openTarget(ctx, opts)
	if err != nil {
		return err
	}
	defer t.repo.Close()

	reg, err := NewRegistry(opts.UserAgent)
	if err != nil {
		return err
	}

	ref := Ref(t.releasesRepo, opts.Version)
	m, err := Pull(ctx, reg, ref)
	if err != nil {
		return err
	}
	// The requested OCI tag and the manifest's self-described version must
	// agree. A retagged artifact must not be presented as a different release.
	if m.Version != opts.Version {
		return fmt.Errorf("%s is tagged as release %s, but its manifest declares version %s", ref, opts.Version, m.Version)
	}

	if opts.OutputFormat == OutputJSON {
		enc := json.NewEncoder(opts.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	}
	return renderManifest(opts.Stdout, m)
}
