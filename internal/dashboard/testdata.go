package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// testdataDirName is the per-system directory holding the test-file library:
// one subfolder per port (testdata/<port>/), keyed by port name like the
// messages/ docs. The name deliberately avoids "test" — that is the dev
// inbox the localstorage binding watches (RootPath("./test/<port>")), and a
// library folder of the same name would collide with it.
const testdataDirName = "testdata"

// maxSeedBytes caps one seeded file. Test payloads are small; the cap keeps
// a misdirected multi-GB drop from streaming through the copy.
const maxSeedBytes = 64 << 20

// testdataLibrary is the GET /api/testdata/{systemPath...} payload: the
// system's test files keyed by port. Ports is always an object (never null)
// even when the system has no library.
type testdataLibrary struct {
	Ports map[string][]string `json:"ports"`
}

// seedRequest is the POST /api/seed body. SystemPath is the root-relative
// system identifier; Port and Component must be wired in the system's
// declared topology (component uses port, direction "in"); File is one entry
// of the port's testdata folder; Force replaces an existing inbox file.
type seedRequest struct {
	SystemPath string `json:"systemPath"`
	Port       string `json:"port"`
	Component  string `json:"component"`
	File       string `json:"file"`
	Force      bool   `json:"force,omitempty"`
}

// listTestData serves the system's test-file library. Confinement reuses
// resolveCreate's mechanism (segment validation plus an absolute-path
// equality check), but not its must-exist rule: for a read endpoint an
// absent library is an empty object, not an error. Only seedable entries are
// listed — regular files with single-segment, non-hidden names — so the UI
// never offers a file the seed endpoint would refuse.
func (s *apiServer) listTestData(w http.ResponseWriter, r *http.Request) {
	dir, err := s.confinedSystemDir(r.PathValue("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lib := testdataLibrary{Ports: map[string][]string{}}
	ports, err := os.ReadDir(filepath.Join(dir, testdataDirName))
	if err != nil {
		writeJSON(w, http.StatusOK, lib)
		return
	}
	for _, port := range ports {
		if !port.IsDir() || !seedableName(port.Name()) {
			continue
		}
		ents, err := os.ReadDir(filepath.Join(dir, testdataDirName, port.Name()))
		if err != nil {
			continue
		}
		var files []string
		for _, ent := range ents {
			info, err := ent.Info()
			if err != nil || !info.Mode().IsRegular() || !seedableName(ent.Name()) {
				continue
			}
			files = append(files, ent.Name())
		}
		if len(files) > 0 {
			sort.Strings(files)
			lib.Ports[port.Name()] = files
		}
	}
	writeJSON(w, http.StatusOK, lib)
}

// seedTestFile copies one library file into the dev inbox the host's
// development definition configures for the port (development.files[].rootPath
// in the topology record). Everything about the request is validated against
// the declared topology: the system exists, the component uses the port with
// direction "in", and the port has a dev folder. The copy is atomic per
// destination (see linkTemp): without force an existing inbox file is a 409,
// with force it is replaced.
func (s *apiServer) seedTestFile(w http.ResponseWriter, r *http.Request) {
	var req seedRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxCreateBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Port == "" || req.Component == "" || req.File == "" {
		writeError(w, http.StatusUnprocessableEntity, "port, component and file are required")
		return
	}
	if !seedableName(req.File) {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("invalid file %q (must be a single, non-hidden path segment)", req.File))
		return
	}
	sysDir, err := s.confinedSystemDir(req.SystemPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entry, err := s.topologyForSystem(r, req.SystemPath)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if !componentUsesPortIn(&entry.Topology, req.Component, req.Port) {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("component %q does not use port %q with direction \"in\" in system %q", req.Component, req.Port, entry.System))
		return
	}
	rootPath, ok := devRootPath(&entry.Topology, req.Port)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf(
			"no dev folder configured for port %q — add development.Files(Ports.X).RootPath(...) in the host and refresh the topology (hosts on Intropy.Topology older than v0.4.2 never emit one)", req.Port))
		return
	}

	src := filepath.Join(sysDir, testdataDirName, req.Port, req.File)
	srcInfo, err := os.Lstat(src)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no test file %q for port %q", req.File, req.Port))
		return
	}
	// A symlink is refused, not followed: a link under testdata/ must not
	// become a read oracle for files outside the workspace.
	if !srcInfo.Mode().IsRegular() {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("test file %q is not a regular file", req.File))
		return
	}
	if srcInfo.Size() > maxSeedBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("test file %q is %d bytes, over the %d MiB cap", req.File, srcInfo.Size(), maxSeedBytes>>20))
		return
	}

	// The rootPath is declared relative to the SystemHost project directory,
	// which is not necessarily the system directory: `sys create` nests the
	// host in a subdirectory (<system>/<host>/), and the host library anchors
	// discovery at the host's own directory. Anchoring at the system dir
	// would seed a folder the extractor never polls.
	hostDir, err := systemHostDir(sysDir)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("dev folder for port %q: %v", req.Port, err))
		return
	}
	destDir, err := confineUnder(hostDir, rootPath)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("dev folder for port %q: %v", req.Port, err))
		return
	}
	// destDisplay is the same directory in the form API responses use for
	// identifiers: joined from the root as handed to the server, so the 201
	// path is a clean root-relative identifier even when confineUnder
	// canonicalized symlinks out of the absolute form.
	destDisplay, err := displayDir(s.root, sysDir, hostDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "create dev folder: "+err.Error())
		return
	}
	dest := filepath.Join(destDir, req.File)
	destOut := filepath.Join(destDisplay, rootPath, req.File)
	if err := copySeedFile(src, dest, req.Force); err != nil {
		switch {
		case errors.Is(err, errSeedExists):
			writeError(w, http.StatusConflict, fmt.Sprintf("%q already exists in the dev folder", req.File))
		case errors.Is(err, errSeedTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("test file %q is over the %d MiB cap", req.File, maxSeedBytes>>20))
		default:
			writeError(w, http.StatusInternalServerError, "seed: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": s.relPath(destOut)})
}

// errSeedExists marks the no-force race loss; errSeedTooLarge marks a file
// that grew past the cap between stat and copy.
var (
	errSeedExists   = errors.New("destination exists")
	errSeedTooLarge = errors.New("test file over the size cap")
)

// copySeedFile copies src to dest atomically per destination: the content is
// written to a temp file in the destination directory, then linked (no
// force — a hard link fails with EEXIST when the target exists, where a
// rename would silently replace) or renamed (force — an intentional atomic
// replace). Two concurrent no-force copies of the same file can no longer
// both win: the loser's link fails.
func copySeedFile(src, dest string, force bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".seed-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, io.LimitReader(in, maxSeedBytes+1)); err != nil {
		tmp.Close()
		return err
	}
	if info, err := tmp.Stat(); err != nil {
		tmp.Close()
		return err
	} else if info.Size() > maxSeedBytes {
		tmp.Close()
		return errSeedTooLarge
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if force {
		return os.Rename(tmpName, dest)
	}
	if err := os.Link(tmpName, dest); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errSeedExists
		}
		return err
	}
	return nil
}

// systemHostDir locates the system-host project directory under sysDir —
// the anchor every development rootPath is declared against. The host is
// rediscovered on each seed, the same freshness rule resolveRun applies to
// start/stop: a sys update between refreshes must not strand the lookup.
func systemHostDir(sysDir string) (string, error) {
	hosts, err := template.ListSystemHosts(sysDir)
	if err != nil || len(hosts) == 0 {
		return "", fmt.Errorf("no system host under %s — create one with the system's host sync or 'intropy sys create'", sysDir)
	}
	return hosts[0].Path, nil
}

// displayDir expresses hostDir in the root-joined (non-canonicalized) form
// relPath turns into a clean API identifier: the workspace root as handed to
// the server, plus the host's path relative to it. filepath.Rel handles the
// whole traversal — both inputs resolve to the same tree, and absolute roots
// produced by Abs keep the result free of the leading ".." a relative-root
// mismatch would invent.
func displayDir(root, sysDir, hostDir string) (string, error) {
	sysAbs, err := filepath.Abs(sysDir)
	if err != nil {
		return "", err
	}
	hostAbs, err := filepath.Abs(hostDir)
	if err != nil {
		return "", err
	}
	hostUnderSys, err := filepath.Rel(sysAbs, hostAbs)
	if err != nil || hostUnderSys == ".." || strings.HasPrefix(hostUnderSys, ".."+string(os.PathSeparator)) || filepath.IsAbs(hostUnderSys) {
		return "", fmt.Errorf("host directory %q is not under its system directory", hostDir)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	sysUnderRoot, err := filepath.Rel(rootAbs, sysAbs)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, sysUnderRoot, hostUnderSys), nil
}

// seedableName reports whether name is usable as a library entry: a single,
// non-hidden path segment (splitCreateDir's segment rules applied to one
// name). The GET listing and the POST accept the same set.
func seedableName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.HasPrefix(name, ".") &&
		!strings.ContainsAny(name, `/\`)
}

// confinedSystemDir resolves a client-supplied, root-relative system path to
// an absolute directory, confined to the workspace root. It reuses
// splitCreateDir's segment validation and resolveCreate's confinement
// mechanism (absolutize both sides, require equality with the constructed
// path), but not its must-exist rule — an absent directory is the caller's
// empty case, not a bad request.
func (s *apiServer) confinedSystemDir(systemPath string) (string, error) {
	segments, err := splitCreateDir(systemPath)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(append([]string{s.root}, segments...)...)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if want := filepath.Join(append([]string{rootAbs}, segments...)...); abs != want {
		return "", fmt.Errorf("invalid system path %q (escapes the workspace root)", systemPath)
	}
	return abs, nil
}

// topologyForSystem finds the cached topology entry whose root-relative path
// is systemPath. The POST blocks on a cold cache — cachedTopologies computes
// on demand — which is acceptable: the flow view warms the cache on visit,
// and a seed click after the flow view rendered is always warm.
func (s *apiServer) topologyForSystem(r *http.Request, systemPath string) (topology.Entry, error) {
	entries, _ := s.cachedTopologies(r.Context(), false)
	for _, e := range entries {
		if e.Path == systemPath {
			return e, nil
		}
	}
	return topology.Entry{}, fmt.Errorf("no topology found for system %q", systemPath)
}

// componentUsesPortIn reports whether the topology wires component to port
// with direction "in".
func componentUsesPortIn(t *topology.Topology, component, port string) bool {
	for _, c := range t.Components {
		if c.Name != component {
			continue
		}
		for _, u := range c.Ports {
			if u.Port == port && u.Direction == "in" {
				return true
			}
		}
	}
	return false
}

// devRootPath returns the development file resolution for port, or false
// when the host declares none (or no development section at all).
func devRootPath(t *topology.Topology, port string) (string, bool) {
	if t.Development == nil {
		return "", false
	}
	for _, f := range t.Development.Files {
		if f.Port == port {
			return f.RootPath, true
		}
	}
	return "", false
}

// confineUnder resolves a declared, base-relative path (the development
// rootPath, "./test/erp-source") against baseDir and confines the result to
// it. splitCreateDir's segment rules cannot be reused — they reject the dot
// segment the declaration convention starts with. Instead the joined path is
// absolutized, existing ancestors have symlinks resolved, and the result is
// checked with filepath.Rel: an absolute result or one starting with ".."
// escapes. (A raw strings.HasPrefix check is not sufficient — a sibling
// "app-evil" passes it against "app".) baseDir is canonicalized the same way
// first: without it the comparison mixes forms — on macOS, for instance, a
// /tmp base stays /tmp while EvalSymlinks resolves the target to
// /private/tmp, and every path reads as an escape. This mirrors the host
// library's own check at graph time; the CLI check is defense-in-depth.
func confineUnder(baseDir, declared string) (string, error) {
	if declared == "" || filepath.IsAbs(declared) {
		return "", fmt.Errorf("path %q must be relative to the system directory", declared)
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	base := baseAbs
	if withLinks, err := resolveExisting(baseAbs); err == nil {
		base = withLinks
	}
	resolved := filepath.Join(base, declared)
	if withLinks, err := resolveExisting(resolved); err == nil {
		resolved = withLinks
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q escapes the system directory", declared)
	}
	return resolved, nil
}

// resolveExisting resolves symlinks along the longest existing prefix of p,
// returning p with that prefix evaluated. A fully non-existent path is
// returned unchanged — nothing to resolve yet. p and the result are always
// absolute (EvalSymlinks requires and returns one), so callers comparing
// against an absolute base see matching forms.
func resolveExisting(p string) (string, error) {
	if _, err := os.Lstat(p); err == nil {
		return filepath.EvalSymlinks(p)
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p, nil
	}
	resolvedParent, err := resolveExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(p)), nil
}
