package skill

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
)

type Project struct {
	Root string
}

var ErrProjectNotFound = errors.New("no skills.json found in current directory or any parent")

// FindProject walks up from startDir looking for skills.json.
// Returns ErrProjectNotFound if no manifest is found before reaching the
// filesystem root.
func FindProject(startDir string) (*Project, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return nil, fmt.Errorf("absolute path: %w", err)
	}

	for {
		candidate := filepath.Join(abs, "skills.json")
		if _, err := os.Stat(candidate); err == nil {
			return &Project{Root: abs}, nil
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat %s: %w", candidate, err)
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			//Reached file system root.
			return nil, ErrProjectNotFound
		}
		abs = parent
	}
}

// ManifestPath returns the absolute path to skills.json.
func (p *Project) ManifestPath() string {
	return filepath.Join(p.Root, "skills.json")
}

// LockfilePath returns the absolute path to skills.lock.json.
func (p *Project) LockfilePath() string {
	return filepath.Join(p.Root, "skills.lock.json")
}

// SkillsDir returns the canonical install directory: <root>/.agents/skills.
// Per §9 of the spec.
func (p *Project) SkillsDir() string {
	return filepath.Join(p.Root, ".agents", "skills")
}

// SkillDir returns the canonical install directory for a named skill:
// <root>/.agents/skills/<name>.
func (p *Project) SkillDir(name string) string {
	return filepath.Join(p.SkillsDir(), name)
}

// SkillRelPath returns the install directory relative to the project root,
// in forward-slash form. This is the value to write to the lockfile.
func (p *Project) SkillRelPath(name string) string {
	return path.Join(".agents", "skills", name)
}

// AdditionalDirs returns the absolute paths from a manifest entry's
// additionalBasePaths, with skill name appended to each. Returns an empty
// slice if the entry has no additionalBasePaths.
func (p *Project) AdditionalDirs(entry ManifestEntry) []string {
	out := make([]string, 0, len(entry.AdditionalBasePaths))
	for _, base := range entry.AdditionalBasePaths {
		out = append(out, filepath.Join(p.Root, base, entry.Name))
	}

	return out
}

func (p *Project) AdditionalRelPaths(entry ManifestEntry) []string {
	out := make([]string, 0, len(entry.AdditionalBasePaths))
	for _, base := range entry.AdditionalBasePaths {
		out = append(out, path.Join(filepath.ToSlash(base), entry.Name))
	}
	return out
}

// LoadManifest reads and parses skills.json from the project root.
func (p *Project) LoadManifest() (*Manifest, error) {
	return LoadManifest(p.ManifestPath())
}

// SaveManifest writes the manifest to skills.json at the project root.
func (p *Project) SaveManifest(m *Manifest) error {
	return SaveManifest(p.ManifestPath(), m)
}

// LoadLockfile reads and parses skills.lock.json from the project root.
// Returns an empty lockfile (not an error) if the file doesn't exist yet.
func (p *Project) LoadLockfile() (*Lockfile, error) {
	return LoadLockfile(p.LockfilePath())
}

// SaveLockfile writes the lockfile to skills.lock.json at the project root.
func (p *Project) SaveLockfile(l *Lockfile) error {
	return SaveLockfile(p.LockfilePath(), l)
}
