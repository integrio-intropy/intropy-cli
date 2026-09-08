package dashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHostRecord writes a system-host scaffold whose record carries the
// values.components baseline sys update diffs against.
func writeHostRecord(t *testing.T, dir, name string, componentJSON string) {
	t.Helper()
	intropyDir := filepath.Join(dir, ".intropy")
	if err := os.MkdirAll(intropyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schemaVersion":1,"template":"system-host","owner":"o","repo":"r","version":"v1","role":"system-host","values":{"name":"` + name + `","components":[` + componentJSON + `]}}` + "\n"
	if err := os.WriteFile(filepath.Join(intropyDir, "scaffold.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncSystemNoOrphansIsNoop(t *testing.T) {
	root := t.TempDir()
	writeHostRecord(t, filepath.Join(root, "acme", "erp", "erp-host"), "erp", "")
	h := testHandler(t, root)

	rec := postJSON(t, h, "/api/systems/acme/erp", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got struct {
		Action      string   `json:"action"`
		Diagnostics []string `json:"diagnostics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// Everything scaffolded is already declared (here: nothing at all), so
	// the update wrote nothing — and said so.
	if got.Action != "none" {
		t.Errorf("action = %q, want none", got.Action)
	}
	if !strings.Contains(strings.Join(got.Diagnostics, "\n"), "no orphaned components") {
		t.Errorf("diagnostics = %v, want the no-orphans note", got.Diagnostics)
	}
}

func TestSyncSystemCreateBranchWithoutScaffolds(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme", "crm"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := testHandler(t, root)

	// No host and nothing assemblable: the create branch is taken and sys
	// create's local validation refuses before any network I/O.
	rec := postJSON(t, h, "/api/systems/acme/crm", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "scaffold") {
		t.Errorf("error should say the workspace has nothing to assemble: %s", rec.Body)
	}
}

func TestSyncSystemDirValidation(t *testing.T) {
	root := t.TempDir()
	h := testHandler(t, root)

	// Raw dot-segments never reach the handler (the mux 307-normalizes
	// them), so the traversal cases arrive encoded — the form a crafted
	// client would send, and the handler's own validation must refuse.
	for _, dir := range []string{"..%2Fx", "a%2F..%2Fb", ".hidden", "missing"} {
		rec := postJSON(t, h, "/api/systems/"+dir, `{}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("dir %q: status = %d, want 422: %s", dir, rec.Code, rec.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "x")); !os.IsNotExist(err) {
		t.Error("sync escaped the workspace root")
	}
}
