package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/skill"
	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
	"golang.org/x/term"
)

// The official Intropy skills collection offered by `int create`.
const (
	defaultSkillsCollectionAlias = "intropy"
	defaultSkillsCollectionRef   = "harbor.intropy.io/skills/index:latest"
)

// skillsCollectionRef returns the collection to offer, overridable via
// INTROPY_SKILLS_COLLECTION (useful for testing against a local registry).
func skillsCollectionRef() string {
	return cmp.Or(os.Getenv("INTROPY_SKILLS_COLLECTION"), defaultSkillsCollectionRef)
}

// decideInstallSkills is the pure gating logic for the post-scaffold skills
// install: --skip-install-skills forces a no and --install-skills forces a
// yes, both without prompting; otherwise --no-input and non-terminal stdin
// skip, and an interactive session gets a [Y/n] prompt (default yes).
func decideInstallSkills(force, skip, noInput bool, in io.Reader, errW io.Writer) (bool, error) {
	if skip {
		return false, nil
	}
	if force {
		return true, nil
	}
	if noInput {
		return false, nil
	}
	f, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return false, nil
	}
	return confirmInstallSkills(in, errW)
}

// maybeInstallSkills applies decideInstallSkills and, on yes, does the
// equivalent of `skills collection add` plus `skills add` for every skill in
// the collection. On no, it prints how to install later — unless the skip
// was the explicit --skip-install-skills, where a hint would just be noise.
func maybeInstallSkills(ctx context.Context, in io.Reader, errW io.Writer, force, skip, noInput bool, outputDir string) error {
	install, err := decideInstallSkills(force, skip, noInput, in, errW)
	if err != nil {
		return err
	}
	ref := skillsCollectionRef()
	if !install {
		if !skip {
			fmt.Fprintf(errW, "skills not installed — run 'intropy skills collection add --name %s --ref %s' in the integration later\n",
				defaultSkillsCollectionAlias, ref)
		}
		return nil
	}

	project, err := skillProjectAt(outputDir)
	if err != nil {
		return err
	}
	client, err := oci.NewClient()
	if err != nil {
		return err
	}
	installer := skill.NewInstaller(client, skill.NewTarGzExtractor(), project)
	adder := skill.NewAdder(client, installer, project)

	entries, err := skill.NewCollectionInstaller(client, adder, project).
		InstallAll(ctx, defaultSkillsCollectionAlias, ref)
	if err != nil {
		return err
	}
	for _, e := range entries {
		fmt.Fprintf(errW, "  installed %s @ %s -> %s\n", e.Name, e.Source.Tag, e.Path)
	}
	fmt.Fprintf(errW, "installed %d skill(s) from %s\n", len(entries), ref)
	return nil
}

// confirmInstallSkills renders a [Y/n] prompt (default yes) and reads the
// answer. EOF counts as a decline, not an error.
func confirmInstallSkills(in io.Reader, out io.Writer) (bool, error) {
	v, err := template.NewStdinPrompter(in, out).Prompt(template.FieldSpec{
		Name:    "install-skills",
		Title:   "Install agent skills into .agents/skills?",
		Type:    "boolean",
		Default: true,
	})
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	return ok && b, nil
}

// skillProjectAt returns the skills project rooted exactly at dir, creating an
// empty skills.json there if the blueprint didn't ship one. Unlike
// resolveOrBootstrapProject it never walks up: the scaffolded integration is
// its own skills project even when created inside a larger repo.
func skillProjectAt(dir string) (*skill.Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	p := &skill.Project{Root: abs}
	if _, err := os.Stat(p.ManifestPath()); err == nil {
		return p, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := p.SaveManifest(&skill.Manifest{Skills: []skill.ManifestEntry{}}); err != nil {
		return nil, fmt.Errorf("bootstrap skills.json: %w", err)
	}
	return p, nil
}
