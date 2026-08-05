package dashboard

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// Graph status values CatalogEntry.GraphStatus takes.
const (
	graphMatched       = "matched"
	graphNoTopology    = "no-topology"
	graphNotInGraph    = "not-in-graph"
	graphTopologyError = "topology-error"
	graphPending       = "pending"
)

// Check severity values.
const (
	severityWarn = "warn"
	severityInfo = "info"
)

// CatalogEntry is the /api/catalog/{path} payload: the integration's header
// facts and its checks, assembled from the cached topology report and the
// scaffold walk. Deployment state is NOT embedded — it stays on
// /api/deploy/{path}, which costs a GitOps checkout refresh.
type CatalogEntry struct {
	// Component is the header identity: the graph's name for the component
	// when GraphStatus is "matched", the scaffold record's name otherwise.
	Component string `json:"component"`

	// Kind is the topology block kind. Empty in every non-matched state.
	Kind string `json:"kind,omitempty"`

	// System is the declared system name, when the integration has one.
	System string `json:"system,omitempty"`

	// Contracts the component publishes/subscribes on, resolved against the
	// topology's contracts registry. Absent in every non-matched state.
	Publishes  []ContractEdge `json:"publishes,omitempty"`
	Subscribes []ContractEdge `json:"subscribes,omitempty"`

	// Repository is "owner/repo" from the scaffold record — provenance only.
	// The record can go stale, so identity and contracts come from the graph.
	Repository string `json:"repository,omitempty"`

	// GraphStatus: "matched" | "no-topology" | "not-in-graph" |
	// "topology-error" | "pending".
	GraphStatus string `json:"graphStatus"`

	// TopologyPending is true while the first topology computation is in
	// flight. The header renders from scaffold data; contracts are unknown.
	TopologyPending bool `json:"topologyPending,omitempty"`

	// Checks are graph-derived findings, warns before infos.
	Checks []Check `json:"checks,omitempty"`
}

// ContractEdge is one end of a pub/sub wire a component sits on.
type ContractEdge struct {
	Pubsub string `json:"pubsub"`
	Topic  string `json:"topic"`

	// Contract is the topic's contract shortName when the registry carries it,
	// and the topic's raw contract name otherwise — the same lookup the flow
	// view's detail panel makes.
	Contract string `json:"contract,omitempty"`
}

// Check is one finding about the integration's place in the system graph.
type Check struct {
	// Severity: "warn" | "info".
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// catalog serves one integration's catalog entry: header identity and
// contracts from the cached topology report when it has the component, from
// the scaffold record otherwise. It never triggers the topology computation —
// the flow view owns that cost — so a request against a cold cache answers
// promptly with the component pending.
func (s *apiServer) catalog(w http.ResponseWriter, r *http.Request) {
	s.byPath(w, r, func(e template.ScaffoldEntry, systems map[string]string) any {
		sum := s.summarize(e, systems)
		loaded, entries, errs := s.topologiesLoaded()
		if !loaded {
			// Answer pending now, compute in the background: the pending answer
			// becomes a matched one on the next fetch, without the request
			// queueing behind a dotnet build per host.
			s.warmTopologies()
		}
		return buildCatalogEntry(sum, systems, loaded, entries, errs)
	})
}

// buildCatalogEntry joins the scaffold summary to the cached topology report.
// Separated from the handler so tests exercise the join without HTTP.
func buildCatalogEntry(sum integrationSummary, systems map[string]string, loaded bool, entries []topology.Entry, errs []string) CatalogEntry {
	entry := CatalogEntry{
		Component:  sum.Name,
		System:     sum.System,
		Repository: sum.Owner + "/" + sum.Repo,
	}

	// A request against a cold cache answers promptly with scaffold identity
	// rather than queueing behind a dotnet build per host.
	if !loaded {
		entry.GraphStatus = graphPending
		entry.TopologyPending = true
		return entry
	}

	if sum.SystemPath == "" {
		entry.GraphStatus = graphNoTopology
		return entry
	}

	topo, hostErr := findTopology(entries, errs, sum.SystemPath)
	switch {
	case hostErr != "":
		entry.GraphStatus = graphTopologyError
		entry.Checks = appendCheck(entry.Checks, Check{Severity: severityInfo, Message: hostErr})
	case topo == nil:
		entry.GraphStatus = graphNoTopology
	default:
		joinTopology(&entry, sum, topo, systemDeclared(systems, sum))
	}
	return entry
}

// findTopology looks up the cached record for a system path, reporting the
// host's own error message when its graph verb failed. The per-host error
// strings the provider produces are prefixed with the system directory they
// ran in, which is the same identifier space the entry paths use.
func findTopology(entries []topology.Entry, errs []string, systemPath string) (*topology.Entry, string) {
	for i := range entries {
		if entries[i].Path == systemPath {
			return &entries[i], ""
		}
	}
	return nil, hostError(errs, systemPath)
}

func hostError(errs []string, systemPath string) string {
	prefix := systemPath + ": "
	for _, err := range errs {
		if strings.HasPrefix(err, prefix) {
			return err
		}
	}
	return ""
}

// systemDeclared reports whether the integration's system membership comes
// from a sibling system-host scaffold rather than from folder convention. Only
// a declared host justifies a drift warning when the component is absent from
// the graph: a folder-derived "system" is a normal workspace state, not a
// promise that a graph exists.
func systemDeclared(systems map[string]string, sum integrationSummary) bool {
	parent := filepath.Clean(filepath.Dir(sum.ScaffoldEntry.Path))
	_, ok := systems[parent]
	return ok
}

// joinTopology resolves the component against its system's declared graph.
// The join is by directory name: a component's directory is its Name relative
// to the system root.
func joinTopology(entry *CatalogEntry, sum integrationSummary, topo *topology.Entry, declared bool) {
	var comp *topology.Component
	for i := range topo.Components {
		if topo.Components[i].Name == sum.Name {
			comp = &topo.Components[i]
			break
		}
	}
	if comp == nil {
		if !declared {
			// The folder looked like a system but no host declared one, so
			// there is no graph to be absent from — nothing to warn about.
			entry.GraphStatus = graphNoTopology
			return
		}
		entry.GraphStatus = graphNotInGraph
		entry.Checks = appendCheck(entry.Checks, Check{
			Severity: severityWarn,
			Message: fmt.Sprintf("the system graph does not declare a component named %s — renamed without re-scaffolding?", sum.Name),
		})
		return
	}

	entry.GraphStatus = graphMatched
	entry.Component = comp.Name
	entry.Kind = comp.Kind
	for _, p := range comp.Publishes {
		entry.Publishes = append(entry.Publishes, contractEdge(topo, p.PubSub, p.Topic))
	}
	for _, sub := range comp.Subscribes {
		entry.Subscribes = append(entry.Subscribes, contractEdge(topo, sub.PubSub, sub.Topic))
	}
}

// appendCheck keeps the slice severity-ordered: warns before infos.
func appendCheck(checks []Check, c Check) []Check {
	if c.Severity != severityWarn {
		return append(checks, c)
	}
	n := 0
	for n < len(checks) && checks[n].Severity == severityWarn {
		n++
	}
	return append(checks[:n], append([]Check{c}, checks[n:]...)...)
}

// contractEdge resolves one (pubsub, topic) reference against the topology's
// topics and contracts registry: the contract is the registry entry's
// shortName when the topic names a contract and the registry carries it, and
// the raw contract name otherwise — the same lookup the flow view makes.
func contractEdge(topo *topology.Entry, pubsub, topic string) ContractEdge {
	edge := ContractEdge{Pubsub: pubsub, Topic: topic}
	for _, t := range topo.Topics {
		if t.PubSub != pubsub || t.Topic != topic || t.Contract == "" {
			continue
		}
		edge.Contract = t.Contract
		for _, c := range topo.Contracts {
			if c.Name == t.Contract && c.ShortName != "" {
				edge.Contract = c.ShortName
				break
			}
		}
		break
	}
	return edge
}
