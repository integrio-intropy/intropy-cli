package deploy

import (
	"context"
	"fmt"
)

// runManifestPipeline gives shared rendering primitives broad coverage across
// both destinations. Production enters through the three manifests operations.
func runManifestPipeline(ctx context.Context, opts manifestRunOptions) error {
	opts.applyDefaults()
	found, lib, err := prepareManifests(ctx, opts)
	if err != nil {
		return err
	}
	defer lib.Close()
	if opts.Mode == modeLocal {
		built, err := renderLocalManifests(ctx, opts, found, lib)
		if err != nil {
			return err
		}
		if _, err := opts.Stdout.Write(built); err != nil {
			return fmt.Errorf("write manifests: %w", err)
		}
		return nil
	}
	opts.reviewEnv = "all"
	return initGitOps(ctx, opts, found, lib)
}
