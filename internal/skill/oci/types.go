package oci

import (
	"io"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// Media types and annotations defined by the Agent Skills OCI spec.
const (
	MediaTypeSkillArtifact = "application/vnd.agentskills.skill.v1"
	MediaTypeSkillConfig   = "application/vnd.agentskills.skill.config.v1+json"
	MediaTypeSkillContent  = "application/vnd.agentskills.skill.content.v1.tar+gzip"
	MediaTypeCollection    = "application/vnd.agentskills.collection.v1"

	AnnotationSkillName          = "io.agentskills.skill.name"
	AnnotationSkillCompatibility = "io.agentskills.skill.compatibility"
	AnnotationSkillRef           = "io.agentskills.skill.ref"
	AnnotationCollectionName     = "io.agentskills.collection.name"
)

// Artifact is a pulled skill: its parsed config, its content layer as a
// stream, and the registry coordinates it came from.
type Artifact struct {
	Config  Config
	Content io.ReadCloser
	Digest  string
	Tag     string
}

// Descriptor aliases the generic registry descriptor so skill callers have
// one import.
type Descriptor = registry.Descriptor

// Index is the parsed form of a published collection: an OCI Image Index
// whose manifests describe the member skills.
type Index struct {
	Annotations map[string]string `json:"annotations,omitempty"`
	Manifests   []IndexEntry      `json:"manifests"`
}

// IndexEntry is one skill in a collection Index. Name and Ref are what a
// user types; Digest and Size are what the registry resolved them to.
type IndexEntry struct {
	Name        string `json:"name"`                  // from io.agentskills.skill.name
	Ref         string `json:"ref"`                   // from io.agentskills.skill.ref
	Version     string `json:"version,omitempty"`     // from io.agentskills.skill.Version
	Description string `json:"description,omitempty"` // from io.agentskills.skill.description
	Digest      string `json:"digest"`
	Size        int64  `json:"size,omitempty"`
}
