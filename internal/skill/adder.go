package skill

import (
	"context"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

// Adder resolves a skill ref, installs it, and records it in both the
// manifest and the lockfile. It is the engine behind 'intropy skills add'.
type Adder struct {
	registry  Registry
	installer *Installer
	project   *Project
}

// NewAdder wires an Adder from its three collaborators.
func NewAdder(r Registry, i *Installer, p *Project) *Adder {
	return &Adder{registry: r, installer: i, project: p}
}

// AddOptions carries the knobs for Add. Empty today; reserved so a future
// option does not change the signature.
type AddOptions struct{}

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
	// Closed again immediately: the install below re-pulls rather than
	// reusing this reader, and a leaked body holds a connection open.
	artifact.Content.Close()

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
		Name:    skillName,
		Source:  parsed.Registry + "/" + parsed.Repository,
		Version: parsed.Tag,
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
