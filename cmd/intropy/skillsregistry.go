package main

import (
	"github.com/integrio-intropy/intropy-cli/internal/registry"
	"github.com/integrio-intropy/intropy-cli/internal/skill"
	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

// newSkillRegistry builds the registry client every skills command uses.
// Tests replace it to exercise commands without a registry.
var newSkillRegistry = func() (skill.Registry, error) {
	return oci.NewClient(registry.WithUserAgent("intropy-cli/" + version))
}
