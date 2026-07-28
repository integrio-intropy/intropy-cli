package template

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRoleProject creates a project whose scaffold record declares a role, so
// tests can assert which of them count as system hosts.
func writeRoleProject(t *testing.T, dir, role string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := testScaffold()
	s.Template = filepath.Base(dir)
	s.Role = role
	if err := WriteScaffold(dir, s); err != nil {
		t.Fatal(err)
	}
}

func TestListSystemHostsFiltersByRole(t *testing.T) {
	root := t.TempDir()
	writeRoleProject(t, filepath.Join(root, "OrderSync.SystemHost"), RoleSystemHost)
	writeRoleProject(t, filepath.Join(root, "Contracts"), RoleSharedLibrary)
	// A block has no role at all.
	writeProject(t, filepath.Join(root, "order-extract"))

	hosts, warnings := ListSystemHosts(root)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(hosts) != 1 {
		t.Fatalf("hosts = %v, want just the system host", entryPaths(hosts))
	}
	if got, want := hosts[0].Path, filepath.Join(root, "OrderSync.SystemHost"); got != want {
		t.Errorf("host path = %q, want %q", got, want)
	}
}

// A workspace with several systems is legitimate; the caller decides what to do
// about it, so the helper must report all of them.
func TestListSystemHostsReturnsEveryHost(t *testing.T) {
	root := t.TempDir()
	writeRoleProject(t, filepath.Join(root, "ordersync", "OrderSync.SystemHost"), RoleSystemHost)
	writeRoleProject(t, filepath.Join(root, "billing", "Billing.SystemHost"), RoleSystemHost)

	hosts, _ := ListSystemHosts(root)
	if len(hosts) != 2 {
		t.Fatalf("hosts = %v, want 2", entryPaths(hosts))
	}
}

func TestListSystemHostsOnWorkspaceWithNoHost(t *testing.T) {
	root := t.TempDir()
	writeProject(t, filepath.Join(root, "order-extract"))

	hosts, warnings := ListSystemHosts(root)
	if len(hosts) != 0 {
		t.Errorf("hosts = %v, want none", entryPaths(hosts))
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
}
