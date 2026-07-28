package dashboard

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// topologyProvider fetches the declared topologies for the workspace. Each
// entry's Path is the on-disk system directory (the handler normalizes it to
// a root-relative identifier). Per-host failures are returned as messages
// alongside the successes so one broken host cannot hide the rest.
type topologyProvider func(ctx context.Context) (entries []topology.Entry, errs []string)

// hostGraphProvider returns the default provider: it finds every scaffolded
// system host under root and asks each one to print its declared topology.
// Hosts scaffolded before the graph verb existed simply fail, which surfaces as
// an error the UI renders.
//
// The mechanics live in topology.RunGraph, which deploy init shares. What stays
// here is the dashboard's policy: never fail, report per host.
func hostGraphProvider(root string) topologyProvider {
	return func(ctx context.Context) ([]topology.Entry, []string) {
		hosts, _ := template.ListSystemHosts(root)
		var entries []topology.Entry
		var errs []string
		for _, h := range hosts {
			t, err := topology.RunGraph(ctx, h.Path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", displayPath(root, h.Path), err))
				continue
			}
			// The system directory is the workspace directory holding the
			// host: components are scaffolded as its siblings.
			entries = append(entries, topology.Entry{Path: filepath.Dir(h.Path), Topology: *t})
		}
		return entries, errs
	}
}

// displayPath shortens a host directory to its root-relative form for error
// messages, matching the identifier space the rest of the API uses.
func displayPath(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Clean(p))
}
