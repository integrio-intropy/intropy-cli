package main

import (
	"github.com/integrio-intropy/intropy-cli/internal/config"
)

// resolveTemplateRepo layers --template-repo over INTROPY_TEMPLATE_REPO and
// templateRepo in the config file. An empty owner/repo pair means the
// official template library; internal/template fills in its default.
func resolveTemplateRepo(flagValue string) (owner, repo string, err error) {
	cfg, err := config.Load()
	if err != nil {
		return "", "", err
	}
	resolved := cfg.Resolve(config.Flags{TemplateRepo: flagValue})
	return config.ParseTemplateRepo(resolved.TemplateRepo)
}
