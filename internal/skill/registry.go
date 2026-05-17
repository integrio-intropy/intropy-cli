package skill

import (
	"context"

	"github.com/intropy/intropy-cli/internal/skill/oci"
)

type Registry interface {
	Pull(ctx context.Context, ref string) (oci.Artifact, error)
	PullIndex(ctx context.Context, ref string) (oci.Index, error)
	Resolve(ctx context.Context, ref string) (oci.Descriptor, error)
	Push(ctx context.Context, ref string, art oci.Artifact) (oci.Descriptor, error)
	PushIndex(ctx context.Context, ref string, idx oci.Index) (oci.Descriptor, error)
}
