package oci

import "io"

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

type Artifact struct {
	Config  Config
	Content io.ReadCloser
	Digest  string
	Tag     string
}

type Descriptor struct {
	MediaType    string
	ArtifactType string
	Digest       string
	Size         int64
	Annotations  map[string]string
}

type Index struct {
	Annotations map[string]string `json:"annotations,omitempty"`
	Manifests   []IndexEntry      `json:"manifests"`
}

type IndexEntry struct {
	Name        string `json:"name"`                  // from io.agentskills.skill.name
	Ref         string `json:"ref"`                   // from io.agentskills.skill.ref
	Version     string `json:"version,omitempty"`     // from io.agentskills.skill.Version
	Description string `json:"description,omitempty"` // from io.agentskills.skill.description
	Digest      string `json:"digest"`
	Size        int64  `json:"size,omitempty"`
}
