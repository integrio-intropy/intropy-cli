package skill

import (
	"context"
	"fmt"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

type Installer struct {
	registry  Registry
	extractor Extractor
	project   *Project
}

func NewInstaller(r Registry, e Extractor, p *Project) *Installer {
	return &Installer{registry: r, extractor: e, project: p}
}

// Install pulls a single skill, extracts it, and returns the lockfile entry
// that should be recorded. The caller is responsible for persisting the
// lockfile.
func (i *Installer) Install(ctx context.Context, entry ManifestEntry) (LockEntry, error) {
	ref := entry.Source
	if entry.Version != "" {
		ref = entry.Source + ":" + entry.Version
	}

	artifact, err := i.registry.Pull(ctx, ref)
	if err != nil {
		return LockEntry{}, fmt.Errorf("pull %s: %w", ref, err)
	}
	defer artifact.Content.Close()

	dests := append([]string{i.project.SkillDir(entry.Name)}, i.project.AdditionalDirs(entry)...)
	if err := i.extractor.Extract(ctx, artifact.Content, dests); err != nil {
		return LockEntry{}, fmt.Errorf("extract %s: %w", entry.Name, err)
	}

	parsed, err := oci.ParseReference(ref)
	if err != nil {
		return LockEntry{}, fmt.Errorf("parse ref %s: %w", ref, err)
	}

	additionalRels := i.project.AdditionalRelPaths(entry)

	lockEntry := LockEntry{
		Name:            entry.Name,
		Path:            i.project.SkillRelPath(entry.Name),
		AdditionalPaths: additionalRels,
		Source: LockSource{
			Registry:   parsed.Registry,
			Repository: parsed.Repository,
			Tag:        artifact.Tag,
			Digest:     artifact.Digest,
			Ref:        parsed.Registry + "/" + parsed.Repository + ":" + artifact.Tag + "@" + artifact.Digest,
		},
		InstalledAt: time.Now().UTC(),
	}

	return lockEntry, nil
}

// Sync installs every skill declared in the manifest and writes the lockfile.
// On error mid-way, any partially-completed installs remain on disk and the
// lockfile is not updated.
func (i *Installer) Sync(ctx context.Context) error {
	manifest, err := i.project.LoadManifest()
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}

	lockfile := &Lockfile{
		Skills: make([]LockEntry, 0, len(manifest.Skills)),
	}

	for _, entry := range manifest.Skills {
		lockEntry, err := i.Install(ctx, entry)
		if err != nil {
			return fmt.Errorf("install %s: %w", entry.Name, err)
		}
		lockfile.Skills = append(lockfile.Skills, lockEntry)
	}

	if err := i.project.SaveLockfile(lockfile); err != nil {
		return fmt.Errorf("save lockfile: %w", err)
	}

	return nil
}
