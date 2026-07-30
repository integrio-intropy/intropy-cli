package skill

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Lockfile is the root of skills.lock.json (§7.3).
type Lockfile struct {
	LockfileVersion int         `json:"lockfileVersion"`
	GeneratedAt     time.Time   `json:"generatedAt"`
	Skills          []LockEntry `json:"skills"`
}

// CurrentLockfileVersion is the only lockfile schema this client reads.
// A lockfile written at any other version is rejected outright rather than
// half-understood.
const CurrentLockfileVersion = 1

// LockEntry is one resolved skill in the lockfile.
type LockEntry struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	Source      LockSource `json:"source"`
	InstalledAt time.Time  `json:"installedAt"`
}

// LockSource pins exactly what was installed: the registry, repository and
// tag a human would recognise, plus the digest and the full ref that make
// the record reproducible.
type LockSource struct {
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest"`
	Ref        string `json:"ref"`
}

// LoadLockfile reads skills.lock.json. A missing file is not an error —
// it just means no install has run yet — so an empty lockfile at the
// current version is returned instead.
func LoadLockfile(path string) (*Lockfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Missing lockfile is normal — first install hasn't run yet.
			return &Lockfile{LockfileVersion: CurrentLockfileVersion}, nil
		}
		return nil, fmt.Errorf("read lockfile: %w", err)
	}

	var l Lockfile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("parse lockfile: %w", err)
	}

	// §7.3: clients must reject lockfiles whose version they don't understand.
	if l.LockfileVersion != CurrentLockfileVersion {
		return nil, fmt.Errorf("unsupported lockfile version %d (this client supports %d)",
			l.LockfileVersion, CurrentLockfileVersion)
	}

	return &l, nil
}

// SaveLockfile writes the lockfile with the current version and a fresh
// timestamp. The lockfile is machine-managed; human edits are not expected
// to survive the next install.
func SaveLockfile(path string, l *Lockfile) error {
	l.LockfileVersion = CurrentLockfileVersion
	l.GeneratedAt = time.Now().UTC()

	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lockfile: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}
