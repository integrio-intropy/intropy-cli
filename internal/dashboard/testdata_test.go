package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// seedTopology is the graph of a system with one extractor consuming the
// in-port "erp-source", whose development definition resolves it to
// ./test/erp-source under the system directory.
func seedTopology(path string) topology.Entry {
	return topology.Entry{
		Path: path,
		Topology: topology.Topology{
			APIVersion: topology.APIVersion,
			Kind:       topology.Kind,
			System:     "order-flow",
			Components: []topology.Component{{
				Name:  "order-extractor",
				Kind:  "extractor",
				Ports: []topology.PortUse{{Port: "erp-source", Direction: "in"}},
			}},
			Ports: []topology.Port{{
				Name:       "erp-source",
				Directions: []string{"in"},
				UsedBy:     []string{"order-extractor"},
			}},
			Development: &topology.Development{
				Files: []topology.FilePort{{Port: "erp-source", RootPath: "./test/erp-source"}},
			},
		},
	}
}

// seedWorkspace builds a workspace holding one system ("orders") with a
// test-file library: two files for erp-source, one hidden (never listed).
// The system host is scaffolded in a nested directory (orders/orders-host),
// matching the `sys create` layout — the development rootPath anchors there,
// not at the system dir.
func seedWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	lib := filepath.Join(root, "orders", testdataDirName, "erp-source")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"orders-2024-01.csv": "sku;price\nA1;10\n",
		"orders-minimal.csv": "sku;price\n",
		".hidden.csv":        "never listed\n",
	} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeScaffoldRole(t, filepath.Join(root, "orders", "orders-host"), "system-host", "v0.1.0", "system-host")
	return root
}

func seedHandler(t *testing.T, root string) http.Handler {
	t.Helper()
	return testHandlerWithTopo(t, root, topoOnce([]topology.Entry{seedTopology("orders")}, nil))
}

func seedBody(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/seed", strings.NewReader(body))
	h.ServeHTTP(rec, req)
	return rec
}

func TestListTestData(t *testing.T) {
	root := seedWorkspace(t)
	h := seedHandler(t, root)

	rec := get(t, h, "/api/testdata/orders")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var lib testdataLibrary
	if err := json.Unmarshal(rec.Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	files := lib.Ports["erp-source"]
	if len(files) != 2 || files[0] != "orders-2024-01.csv" || files[1] != "orders-minimal.csv" {
		t.Errorf("files = %v, want the two csv files sorted, hidden file excluded", files)
	}
}

// A system without a testdata directory has an empty library, not an error.
func TestListTestDataAbsent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "orders"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := seedHandler(t, root)

	rec := get(t, h, "/api/testdata/orders")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var lib testdataLibrary
	if err := json.Unmarshal(rec.Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	if lib.Ports == nil || len(lib.Ports) != 0 {
		t.Errorf("ports = %v, want an empty (non-nil) object", lib.Ports)
	}
}

// A symlinked library entry is not offered: the POST would refuse it.
func TestListTestDataSkipsSymlinks(t *testing.T) {
	root := seedWorkspace(t)
	outside := filepath.Join(t.TempDir(), "secret.csv")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "orders", testdataDirName, "erp-source", "linked.csv")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	h := seedHandler(t, root)

	rec := get(t, h, "/api/testdata/orders")
	var lib testdataLibrary
	if err := json.Unmarshal(rec.Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	for _, f := range lib.Ports["erp-source"] {
		if f == "linked.csv" {
			t.Errorf("symlinked file listed: %v", lib.Ports["erp-source"])
		}
	}
}

func TestListTestDataEscapesRejected(t *testing.T) {
	root := seedWorkspace(t)
	h := seedHandler(t, root)
	// Dot segments are normalized away by ServeMux before the handler runs
	// (307 redirect), which is also a rejection — the wildcard never carries
	// a raw "..". The one that reaches the handler verbatim is a
	// dot-prefixed segment.
	for _, p := range []string{"/api/testdata/.hidden"} {
		if rec := get(t, h, p); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400", p, rec.Code)
		}
	}
	for _, p := range []string{"/api/testdata/..", "/api/testdata/a/.."} {
		if rec := get(t, h, p); rec.Code == http.StatusOK {
			t.Errorf("GET %s: status = %d, must not serve", p, rec.Code)
		}
	}
}

func TestSeedHappyPath(t *testing.T) {
	root := seedWorkspace(t)
	h := seedHandler(t, root)

	rec := seedBody(t, h, `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"orders-2024-01.csv"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	// The destination anchors at the nested host directory, not the system
	// dir — the folder the extractor's localstorage binding actually polls.
	dest := filepath.Join(root, "orders", "orders-host", "test", "erp-source", "orders-2024-01.csv")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("seeded file: %v", err)
	}
	if string(data) != "sku;price\nA1;10\n" {
		t.Errorf("content = %q", data)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["path"] != "orders/orders-host/test/erp-source/orders-2024-01.csv" {
		t.Errorf("path = %q", out["path"])
	}
}

// The destination directory is created on demand.
func TestSeedCreatesInbox(t *testing.T) {
	root := seedWorkspace(t)
	h := seedHandler(t, root)
	rec := seedBody(t, h, `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"orders-minimal.csv"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if info, err := os.Stat(filepath.Join(root, "orders", "orders-host", "test", "erp-source")); err != nil || !info.IsDir() {
		t.Errorf("inbox dir: %v", err)
	}
}

// A system whose host scaffold is gone cannot be seeded: without the host
// directory there is no anchor for the declared rootPath.
func TestSeedNoHost(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "orders", testdataDirName, "erp-source")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "a.csv"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := seedHandler(t, root)
	rec := seedBody(t, h, `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"a.csv"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "no system host") {
		t.Errorf("error should name the missing host: %s", rec.Body)
	}
}

// An existing inbox file is a conflict; force replaces it.
func TestSeedConflictThenForce(t *testing.T) {
	root := seedWorkspace(t)
	h := seedHandler(t, root)
	body := `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"orders-2024-01.csv"}`
	if rec := seedBody(t, h, body); rec.Code != http.StatusCreated {
		t.Fatalf("first seed: %d", rec.Code)
	}

	if rec := seedBody(t, h, body); rec.Code != http.StatusConflict {
		t.Fatalf("second seed: status = %d, want 409", rec.Code)
	}

	// Force overwrites with the library content.
	dest := filepath.Join(root, "orders", "orders-host", "test", "erp-source", "orders-2024-01.csv")
	if err := os.WriteFile(dest, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	forced := strings.Replace(body, `}`, `,"force":true}`, 1)
	if rec := seedBody(t, h, forced); rec.Code != http.StatusCreated {
		t.Fatalf("forced seed: %d: %s", rec.Code, rec.Body)
	}
	if data, _ := os.ReadFile(dest); string(data) != "sku;price\nA1;10\n" {
		t.Errorf("content after force = %q", data)
	}
}

func TestSeedValidation(t *testing.T) {
	root := seedWorkspace(t)
	h := seedHandler(t, root)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"missing fields", `{"systemPath":"orders"}`, http.StatusUnprocessableEntity},
		{"unknown system", `{"systemPath":"nope","port":"erp-source","component":"order-extractor","file":"orders-2024-01.csv"}`, http.StatusUnprocessableEntity},
		{"wrong component", `{"systemPath":"orders","port":"erp-source","component":"other","file":"orders-2024-01.csv"}`, http.StatusUnprocessableEntity},
		{"wrong port", `{"systemPath":"orders","port":"erp-dest","component":"order-extractor","file":"orders-2024-01.csv"}`, http.StatusUnprocessableEntity},
		{"unknown file", `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"nope.csv"}`, http.StatusNotFound},
		{"hidden file refused even though it exists", `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":".hidden.csv"}`, http.StatusUnprocessableEntity},
		{"file with separator", `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"a/b.csv"}`, http.StatusUnprocessableEntity},
		{"bad system path", `{"systemPath":"..","port":"erp-source","component":"order-extractor","file":"orders-2024-01.csv"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := seedBody(t, h, tc.body); rec.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// A port the development definition does not resolve is a 422 naming the
// fix, not a guess at a folder.
func TestSeedNoDevFolder(t *testing.T) {
	entry := seedTopology("orders")
	entry.Components[0].Ports = append(entry.Components[0].Ports, topology.PortUse{Port: "raw", Direction: "in"})
	entry.Ports = append(entry.Ports, topology.Port{Name: "raw", Directions: []string{"in"}, UsedBy: []string{"order-extractor"}})
	root := seedWorkspace(t)
	h := testHandlerWithTopo(t, root, topoOnce([]topology.Entry{entry}, nil))

	rec := seedBody(t, h, `{"systemPath":"orders","port":"raw","component":"order-extractor","file":"orders-2024-01.csv"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "development.Files") {
		t.Errorf("error should name the fix: %s", rec.Body)
	}
}

// A symlinked library file is refused at copy time too — the read oracle
// stays closed even when a caller bypasses the listing.
func TestSeedSymlinkRefused(t *testing.T) {
	root := seedWorkspace(t)
	outside := filepath.Join(t.TempDir(), "secret.csv")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "orders", testdataDirName, "erp-source", "linked.csv")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	h := seedHandler(t, root)
	rec := seedBody(t, h, `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"linked.csv"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

// A host-declared rootPath that escapes the system directory is refused at
// seed time (the host library already rejects it at graph time; this is the
// defense-in-depth check).
func TestSeedRootPathEscapeRefused(t *testing.T) {
	entry := seedTopology("orders")
	entry.Development.Files[0].RootPath = "../../elsewhere"
	root := seedWorkspace(t)
	h := testHandlerWithTopo(t, root, topoOnce([]topology.Entry{entry}, nil))

	rec := seedBody(t, h, `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"orders-2024-01.csv"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "escapes") {
		t.Errorf("error should name the escape: %s", rec.Body)
	}
}

// A naive strings.HasPrefix confinement would accept a sibling whose name
// extends the system dir's ("orders-evil" against "orders"); filepath.Rel
// does not.
func TestConfineUnderPrefixSibling(t *testing.T) {
	base := t.TempDir()
	if _, err := confineUnder(base, "../"+filepath.Base(base)+"-evil/x"); err == nil {
		t.Fatal("sibling-prefix path accepted")
	}
	// The result is the canonicalized base (symlinks resolved, as on macOS
	// /tmp) joined with the declared path — the same form the confinement
	// check compares.
	canonical, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := confineUnder(base, "./test/erp-source"); err != nil ||
		got != filepath.Join(canonical, "test", "erp-source") {
		t.Errorf("happy path = %q, %v", got, err)
	}
}

func TestSeedSizeCap(t *testing.T) {
	root := seedWorkspace(t)
	big := filepath.Join(root, "orders", testdataDirName, "erp-source", "big.csv")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), maxSeedBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	h := seedHandler(t, root)
	rec := seedBody(t, h, `{"systemPath":"orders","port":"erp-source","component":"order-extractor","file":"big.csv"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

// Two concurrent no-force seeds of the same file cannot both win: the loser's
// link fails EEXIST and maps to 409.
func TestCopySeedFileRace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.csv")
	if err := os.WriteFile(src, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest.csv")
	if err := copySeedFile(src, dest, false); err != nil {
		t.Fatalf("first copy: %v", err)
	}
	if err := copySeedFile(src, dest, false); !errorsIs(err, errSeedExists) {
		t.Fatalf("second copy: %v, want errSeedExists", err)
	}
	if err := copySeedFile(src, dest, true); err != nil {
		t.Fatalf("forced copy: %v", err)
	}
	// A failed no-force copy leaves no temp litter in the destination dir.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range ents {
		if strings.HasPrefix(ent.Name(), ".seed-") {
			t.Errorf("temp file left behind: %s", ent.Name())
		}
	}
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
