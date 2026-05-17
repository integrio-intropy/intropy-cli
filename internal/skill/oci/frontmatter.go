package oci

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

var frontMatterRe = regexp.MustCompile(`(?s)\A---\s*\n(.*?)\n---\s*(\n|\z)`)

type frontmatter struct {
	Name          string         `yaml:"name"`
	Version       string         `yaml:"version,omitempty"`
	Description   string         `yaml:"description,omitempty"`
	License       string         `yaml:"license,omitempty"`
	Compatibility string         `yaml:"compatibility,omitempty"`
	AllowedTools  []string       `yaml:"allowedTools,omitempty"`
	Metadata      map[string]any `yaml:"metadata,omitempty"`
}

func parseFrontMatter(skillMD []byte) (Config, error) {
	m := frontMatterRe.FindSubmatch(skillMD)
	if m == nil {
		return Config{}, fmt.Errorf("SKILL.md missing YAML frontmatter")
	}
	var fm frontmatter
	//Index 0 contains the full match and index 1+ contains group matches.
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return Config{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	cfg := Config{
		SchemaVersion: SupportedSchemaVersion,
		Name:          fm.Name,
		Version:       fm.Version,
		Description:   fm.Description,
		License:       fm.License,
		Compatibility: fm.Compatibility,
		AllowedTools:  fm.AllowedTools,
		Metadata:      fm.Metadata,
	}

	return cfg, cfg.Validate()
}
