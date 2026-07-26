package registry

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

type Artifact struct {
	ArtifactType string
	Config       Blob
	Layers       []Blob
	Annotations  map[string]string
}

type Index struct {
	ArtifactType string
	Annotations  map[string]string
	Manifests    []IndexManifest
}

// IndexManifest is one entry of an Index. When SourceRef is set, PushIndex
// copies the referenced manifest from that repository into the index's
// target repository before pushing the index.
type IndexManifest struct {
	Descriptor Descriptor
	SourceRef  string
}
