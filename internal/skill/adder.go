package skill

import (
	"context"
	"fmt"

	"github.com/intropy/intropy-cli/internal/skill/oci"
)

type Adder struct {
	registry  Registry
	installer *Installer
	project   *Project
}

func NewAdder(r Registry, i *Installer, p *Project) *Adder {
	return &Adder{registry: r, installer: i, project: p}
}

type AddOptions struct {
	AdditionalBasePaths []string
}

// Add adds a skill to the project, installs it, and persists both files.
// The ref must include a tag (e.g. ghcr.io/.../skill:1.0.0).
func (a *Adder) Add(ctx context.Context, ref string, opts AddOptions) (LockEntry, error) {
	parsed, err := oci.ParseReference(ref)
	if err != nil {
		return LockEntry{}, fmt.Errorf("parse ref: %w", err)
	}
	if parsed.Tag == "" {
		return LockEntry{}, fmt.Errorf("ref must include a tag")
	}

	artifact, err := a.registry.Pull(ctx, ref)
	if err != nil {
		return LockEntry{}, fmt.Errorf("pull %s: %w", ref, err)
	}

	skillName := artifact.Config.Name
	artifact.Content.Close() // we'll re-pull during installer.Install

	// Check the manifest for duplicates.
	manifest, err := a.project.LoadManifest()
	if err != nil {
		return LockEntry{}, fmt.Errorf("load manifest: %w", err)
	}
	for _, e := range manifest.Skills {
		if e.Name == skillName {
			return LockEntry{}, fmt.Errorf("skill %q is already in the manifest", skillName)
		}
	}

	entry := ManifestEntry{
		Name:                skillName,
		Source:              parsed.Registry + "/" + parsed.Repository,
		Version:             parsed.Tag,
		AdditionalBasePaths: opts.AdditionalBasePaths,
	}

	lockEntry, err := a.installer.Install(ctx, entry)
	if err != nil {
		return LockEntry{}, fmt.Errorf("install %s: %w", skillName, err)
	}

	//Append to the manifest
	manifest.Skills = append(manifest.Skills, entry)
	if err := a.project.SaveManifest(manifest); err != nil {
		return LockEntry{}, fmt.Errorf("save manifest: %w", err)
	}

	lockfile, err := a.project.LoadLockfile()
	if err != nil {
		return LockEntry{}, fmt.Errorf("load lockfile: %w", err)
	}
	lockfile.Skills = upsertLockEntry(lockfile.Skills, lockEntry)
	if err := a.project.SaveLockfile(lockfile); err != nil {
		return LockEntry{}, fmt.Errorf("save lockfile: %w", err)
	}

	return lockEntry, nil
}

func upsertLockEntry(entries []LockEntry, e LockEntry) []LockEntry {
	for i, existing := range entries {
		if existing.Name == e.Name {
			entries[i] = e
			return entries
		}
	}

	return append(entries, e)
}
