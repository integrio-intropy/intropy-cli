package deploy

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// The Kubernetes workload a block runs as.
//
// An extractor is scheduled: it wakes, pulls from its source and exits, which is
// a CronJob. Every other block reacts to messages or requests and must stay
// resident. This is what the hand-written customer repos already do — fluxia and
// entrovia both run their extractors as CronJobs — so deriving it from the
// topology is reproducing an existing convention, not inventing one.
const (
	WorkloadCronJob    = "cronjob"
	WorkloadDeployment = "deployment"
)

// blockKindExtractor is the topology's block kind for a scheduled extractor.
// Records spell kinds inconsistently (camelCase from one emitter, kebab-case
// after the frontend normalises), so the comparison folds case.
const blockKindExtractor = "extractor"

// InitModel is the topology view a manifest skeleton sees under the reserved
// "topology" key: derived, flattened and sorted.
//
// Raw topology.Topology is unsuitable for two reasons. Its APIs, Provides and
// Consumes are json.RawMessage whose element shape is not finalised, so a
// skeleton ranging over them would break when it is. And neither Go map
// iteration nor host emission order is stable, while idempotent scaffolding
// depends on a re-render being byte-identical — so everything here is sorted.
type InitModel struct {
	System     string          `json:"system"`
	Components []InitComponent `json:"components"`
	PubSubs    []InitPubSub    `json:"pubsubs"`
	Topics     []InitTopic     `json:"topics"`
	Connectors []InitConnector `json:"connectors"`
}

// InitComponent is one block as the manifests need it.
type InitComponent struct {
	// Name is the topology component name, which is also the GitOps component
	// directory segment.
	Name string `json:"name"`

	// Kind is the topology block kind, carried through verbatim for labels.
	Kind string `json:"kind"`

	// AppID is the Dapr app-id: what a Component's scopes list and what the
	// generated C# resolves components by. Falls back to Name when no scaffold
	// record declares one.
	AppID string `json:"appId"`

	// Workload is WorkloadCronJob or WorkloadDeployment.
	Workload string `json:"workload"`

	// Dir is the component's source directory relative to the system root, when
	// a scaffold record was matched. Empty otherwise — a missing record is a
	// warning, not a failure, so the manifests still generate.
	Dir string `json:"dir,omitempty"`

	Topics     []string `json:"topics,omitempty"`
	Connectors []string `json:"connectors,omitempty"`
}

// InitPubSub is one Dapr pub/sub component the system needs.
type InitPubSub struct {
	Name   string   `json:"name"`
	Topics []string `json:"topics,omitempty"`

	// AppIDs is what belongs in the Component's scopes: every app-id the
	// topology says publishes to or subscribes from one of its topics.
	AppIDs []string `json:"appIds,omitempty"`
}

// InitTopic is a declared topic, with the ends resolved to app-ids.
type InitTopic struct {
	PubSub      string   `json:"pubsub"`
	Topic       string   `json:"topic"`
	Contract    string   `json:"contract,omitempty"`
	Publishers  []string `json:"publishers,omitempty"`
	Subscribers []string `json:"subscribers,omitempty"`
}

// InitConnector is an external integration point. The topology mints only
// its name: the deployed Dapr binding type, address, host and credential are
// environment-owned deployment configuration, which is exactly why they are
// placeholders in the rendered manifests.
type InitConnector struct {
	Name           string   `json:"name"`
	ExternalSystem string   `json:"externalSystem,omitempty"`
	Directions     []string `json:"directions,omitempty"`
	AppIDs         []string `json:"appIds,omitempty"`
}

// newInitModel derives the model from a decoded topology record and whatever
// scaffold records were found in the workspace.
func newInitModel(t *topology.Topology, scaffolds []template.ScaffoldEntry) InitModel {
	appIDs, dirs := joinScaffolds(t.Components, matchScaffolds(t.Components, scaffolds))

	m := InitModel{System: t.System}
	m.Components = buildComponents(t.Components, appIDs, dirs)
	m.Topics = buildTopics(t.Topics, appIDs)
	m.PubSubs = buildPubSubs(t, appIDs)
	m.Connectors = buildConnectors(t.Connectors, appIDs)
	return m
}

// asMap renders the model as plain maps and slices.
//
// Injected reserved values go through this so a skeleton's `index`, `range` and
// sprig calls see uniform map[string]any — a Go struct behaves differently
// under `index` and would make the template author's life needlessly subtle.
func (m InitModel) asMap() (map[string]any, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode topology model: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode topology model: %w", err)
	}
	return out, nil
}

// matchScaffolds pairs each topology component with its scaffold record.
//
// The join is by convention, and the toolchain uses two conventions: a
// component's directory basename, and its scaffold record's values.appId. Both
// are matched — directory first — because a record found by either is better
// than manifests generated without one. A component with no record is simply
// absent from the result: that is a warning, not a failure.
func matchScaffolds(components []topology.Component, scaffolds []template.ScaffoldEntry) map[string]template.ScaffoldEntry {
	byDir := make(map[string]template.ScaffoldEntry, len(scaffolds))
	byAppID := make(map[string]template.ScaffoldEntry, len(scaffolds))
	for _, s := range scaffolds {
		byDir[filepath.Base(s.Path)] = s
		if id := scaffoldString(s, "appId"); id != "" {
			byAppID[id] = s
		}
	}

	out := make(map[string]template.ScaffoldEntry, len(components))
	for _, c := range components {
		if match, found := byDir[c.Name]; found {
			out[c.Name] = match
			continue
		}
		if match, found := byAppID[c.Name]; found {
			out[c.Name] = match
		}
	}
	return out
}

// joinScaffolds reduces the matched records to the two facts the model needs.
func joinScaffolds(components []topology.Component, matched map[string]template.ScaffoldEntry) (appIDs, dirs map[string]string) {
	appIDs = make(map[string]string, len(components))
	dirs = make(map[string]string, len(components))
	for _, c := range components {
		match, found := matched[c.Name]
		if !found {
			continue
		}
		dirs[c.Name] = filepath.Base(match.Path)
		if id := scaffoldString(match, "appId"); id != "" {
			appIDs[c.Name] = id
		}
	}
	return appIDs, dirs
}

func scaffoldString(s template.ScaffoldEntry, key string) string {
	v, _ := s.Values[key].(string)
	return v
}

// appIDOf resolves a component name to an app-id.
//
// A name with no entry is returned unchanged. That covers two cases: a component
// with no scaffold record, and a name that is not a component of this system at
// all — Topic.Publishers and Topic.Subscribers may reference a block deployed
// elsewhere, and denying it access to the broker would be worse than scoping a
// name we cannot resolve.
func appIDOf(appIDs map[string]string, name string) string {
	if id, ok := appIDs[name]; ok {
		return id
	}
	return name
}

func buildComponents(components []topology.Component, appIDs, dirs map[string]string) []InitComponent {
	out := make([]InitComponent, 0, len(components))
	for _, c := range components {
		ic := InitComponent{
			Name:     c.Name,
			Kind:     c.Kind,
			AppID:    appIDOf(appIDs, c.Name),
			Workload: workloadFor(c.Kind),
			Dir:      dirs[c.Name],
		}

		topics := map[string]bool{}
		for _, s := range c.Subscribes {
			topics[s.Topic] = true
		}
		for _, p := range c.Publishes {
			topics[p.Topic] = true
		}
		ic.Topics = sortedKeys(topics)

		conns := map[string]bool{}
		for _, u := range c.Connectors {
			conns[u.Connector] = true
		}
		ic.Connectors = sortedKeys(conns)

		out = append(out, ic)
	}
	slices.SortFunc(out, func(a, b InitComponent) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// workloadFor maps a block kind to its Kubernetes workload. Anything that is not
// an extractor stays resident, which is the safe default: a block wrongly run as
// a CronJob would silently stop consuming.
func workloadFor(kind string) string {
	if strings.EqualFold(kind, blockKindExtractor) {
		return WorkloadCronJob
	}
	return WorkloadDeployment
}

func buildTopics(topics []topology.Topic, appIDs map[string]string) []InitTopic {
	out := make([]InitTopic, 0, len(topics))
	for _, t := range topics {
		out = append(out, InitTopic{
			PubSub:      t.PubSub,
			Topic:       t.Topic,
			Contract:    t.Contract,
			Publishers:  sortedAppIDs(appIDs, t.Publishers),
			Subscribers: sortedAppIDs(appIDs, t.Subscribers),
		})
	}
	slices.SortFunc(out, func(a, b InitTopic) int {
		if c := strings.Compare(a.PubSub, b.PubSub); c != 0 {
			return c
		}
		return strings.Compare(a.Topic, b.Topic)
	})
	return out
}

// buildPubSubs collects the distinct pub/sub components the system needs.
//
// Names are gathered from the top-level topics *and* from each component's
// inline references, because a component may publish to a topic the record's
// topics[] table does not list — and a pub/sub nobody declared is still one the
// sidecar will try to resolve.
func buildPubSubs(t *topology.Topology, appIDs map[string]string) []InitPubSub {
	topics := map[string]map[string]bool{}
	scopes := map[string]map[string]bool{}

	note := func(pubsub, topic string, users ...string) {
		if pubsub == "" {
			return
		}
		if topics[pubsub] == nil {
			topics[pubsub] = map[string]bool{}
			scopes[pubsub] = map[string]bool{}
		}
		if topic != "" {
			topics[pubsub][topic] = true
		}
		for _, u := range users {
			if u != "" {
				scopes[pubsub][appIDOf(appIDs, u)] = true
			}
		}
	}

	for _, top := range t.Topics {
		note(top.PubSub, top.Topic, append(slices.Clone(top.Publishers), top.Subscribers...)...)
	}
	for _, c := range t.Components {
		for _, s := range c.Subscribes {
			note(s.PubSub, s.Topic, c.Name)
		}
		for _, p := range c.Publishes {
			note(p.PubSub, p.Topic, c.Name)
		}
	}

	out := make([]InitPubSub, 0, len(topics))
	for _, name := range slices.Sorted(maps.Keys(topics)) {
		out = append(out, InitPubSub{
			Name:   name,
			Topics: sortedKeys(topics[name]),
			AppIDs: sortedKeys(scopes[name]),
		})
	}
	return out
}

func buildConnectors(connectors []topology.Connector, appIDs map[string]string) []InitConnector {
	out := make([]InitConnector, 0, len(connectors))
	for _, c := range connectors {
		out = append(out, InitConnector{
			Name:           c.Name,
			ExternalSystem: c.ExternalSystem,
			Directions:     slices.Sorted(slices.Values(c.Directions)),
			AppIDs:         sortedAppIDs(appIDs, c.UsedBy),
		})
	}
	slices.SortFunc(out, func(a, b InitConnector) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func sortedAppIDs(appIDs map[string]string, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[appIDOf(appIDs, n)] = true
	}
	return sortedKeys(set)
}

// sortedKeys returns a set's members in sorted order, or nil when empty so the
// omitempty JSON tags elide the field rather than emitting [].
func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(set))
}
