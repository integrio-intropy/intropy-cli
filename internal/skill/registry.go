package skill

import (
	"context"

	"github.com/intropy/intropy-cli/internal/skill/oci"
)

type Registry interface {
	Pull(ctx context.Context, ref string) (oci.Artifact, error)
	PullIndex(ctx context.Context, ref string) (oci.Index, error)
}
