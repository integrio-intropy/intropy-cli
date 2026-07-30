package registry

// Descriptor is what the registry knows about a pushed or resolved object:
// its identity (digest, size), its shape (media and artifact types), and
// any annotations the publisher attached.
type Descriptor struct {
	MediaType    string
	ArtifactType string
	Digest       string
	Size         int64
	Annotations  map[string]string
}

// Blob is a sized piece of content with its media type. Contents are held in
// memory; artifacts pushed and pulled through this package are expected to
// be small.
type Blob struct {
	MediaType string
	Data      []byte
}

// Artifact is a manifest under construction or just pulled: a config blob
// and ordered content layers, plus the annotations that ride on the
// manifest itself.
type Artifact struct {
	ArtifactType string
	Config       Blob
	Layers       []Blob
	Annotations  map[string]string
}

// Index is an OCI Image Index under construction: a list of manifests
// (already pushed elsewhere) bound together under one ref.
type Index struct {
	ArtifactType string
	Annotations  map[string]string
	Manifests    []IndexManifest
}

// IndexManifest is one entry of an Index. When SourceRef is set, PushIndex
// copies the referenced manifest from that repository into the index's
// target repository before pushing the index — an index is only as durable
// as the repository it lives in, so its members must live there too.
type IndexManifest struct {
	Descriptor Descriptor
	SourceRef  string
}
