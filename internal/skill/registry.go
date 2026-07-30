package skill

import (
	"context"

	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

// Registry is the subset of the OCI registry client the skill package
// needs. It exists so tests can fake registry access without standing up
// an OCI server.
type Registry interface {
	Pull(ctx context.Context, ref string) (oci.Artifact, error)
	PullIndex(ctx context.Context, ref string) (oci.Index, error)
	Resolve(ctx context.Context, ref string) (oci.Descriptor, error)
	Push(ctx context.Context, ref string, art oci.Artifact) (oci.Descriptor, error)
	PushIndex(ctx context.Context, ref string, idx oci.Index) (oci.Descriptor, error)
}
