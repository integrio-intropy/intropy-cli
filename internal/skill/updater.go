package skill

import (
	"context"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

type Updater struct {
	registry  Registry
	installer *Installer
	project   *Project
}

func NewUpdater(r Registry, i *Installer, p *Project) *Updater {
	return &Updater{registry: r, installer: i, project: p}
}

// UpdateResult describes the outcome of a single skill update.
type UpdateResult struct {
	Name       string
	OldVersion string
	NewVersion string
	Changed    bool
}

// Update reconciles a single installed skill against the ref pinned by the
// registered collections' cached indexes. If the resolved ref matches the
// manifest entry already, returns Changed=false and leaves disk untouched.
// Otherwise pulls the new content, replaces .agents/skills/<name>, and
// rewrites skills.json and skills.lock.json.
//
// Refresh the relevant collection first (via `intropy skills collection
// update`) if you want to pick up changes the registry has since published.
func (u *Updater) Update(ctx context.Context, name string) (UpdateResult, error) {
	manifest, err := u.project.LoadManifest()
	if err != nil {
		return UpdateResult{}, fmt.Errorf("load manifest: %w", err)
	}

	existingIdx := -1
	for i, e := range manifest.Skills {
		if e.Name == name {
			existingIdx = i
			break
		}
	}
	if existingIdx == -1 {
		return UpdateResult{}, fmt.Errorf("skill %q is not in the manifest", name)
	}
	existing := manifest.Skills[existingIdx]

	resolution, err := ResolveSkillName(u.project, name, "")
	if err != nil {
		return UpdateResult{}, err
	}
	if resolution.Entry.Ref == "" {
		return UpdateResult{}, fmt.Errorf("collection entry for %q has no ref annotation", name)
	}

	parsed, err := oci.ParseReference(resolution.Entry.Ref)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("parse resolved ref: %w", err)
	}
	if parsed.Tag == "" {
		return UpdateResult{}, fmt.Errorf("resolved ref for %q has no tag", name)
	}

	newSource := parsed.Registry + "/" + parsed.Repository
	newVersion := parsed.Tag

	if existing.Source == newSource && existing.Version == newVersion {
		return UpdateResult{
			Name:       name,
			OldVersion: existing.Version,
			NewVersion: newVersion,
			Changed:    false,
		}, nil
	}

	newEntry := ManifestEntry{
		Name:                name,
		Source:              newSource,
		Version:             newVersion,
		AdditionalBasePaths: existing.AdditionalBasePaths,
	}

	lockEntry, err := u.installer.Install(ctx, newEntry)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("install %s: %w", name, err)
	}

	manifest.Skills[existingIdx] = newEntry
	if err := u.project.SaveManifest(manifest); err != nil {
		return UpdateResult{}, fmt.Errorf("save manifest: %w", err)
	}

	lockfile, err := u.project.LoadLockfile()
	if err != nil {
		return UpdateResult{}, fmt.Errorf("load lockfile: %w", err)
	}
	lockfile.Skills = upsertLockEntry(lockfile.Skills, lockEntry)
	if err := u.project.SaveLockfile(lockfile); err != nil {
		return UpdateResult{}, fmt.Errorf("save lockfile: %w", err)
	}

	return UpdateResult{
		Name:       name,
		OldVersion: existing.Version,
		NewVersion: newVersion,
		Changed:    true,
	}, nil
}
