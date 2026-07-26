package system

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// ErrNoComponents is returned when the workspace scan finds no scaffold
// record `sys create` can assemble into a component.
var ErrNoComponents = errors.New("no assemblable integration scaffolds found")

// Assemble classifies workspace scaffold records into a system model:
// shared-library records become the referenced contract project,
// extractor/loader records become components, and everything else is
// skipped through warnf. The returned model's Name/ProjectName/SystemClass
// are left empty — the caller fills them around the template render.
func Assemble(entries []template.ScaffoldEntry, warnf func(format string, args ...any)) (*Model, error) {
	var (
		components  []Component
		shared      []SharedLibrary
		byAppID     = map[string]string{}   // appId -> scaffold dir
		byTopic     = map[TopicKey]Topic{}  // key -> first-seen topic
		firstDir    = map[TopicKey]string{} // key -> dir that defined it
		byConnector = map[string]string{}   // connector -> scaffold dir
	)

	for _, e := range entries {
		switch {
		case e.Role == template.RoleSharedLibrary:
			name, err := stringValue(e, "name")
			if err != nil {
				return nil, err
			}
			shared = append(shared, SharedLibrary{Path: e.Path, Name: name})
			continue
		case e.Role == template.RoleSystemHost:
			warnf("skipping %s: an existing system host", e.Path)
			continue
		case e.BlockKind == "":
			warnf("skipping %s: scaffold record has no block kind — re-scaffold with a newer template release to include it", e.Path)
			continue
		case e.BlockKind != template.BlockKindExtractor && e.BlockKind != template.BlockKindLoader:
			warnf("skipping %s: unsupported block kind %q (sys create assembles extractors and loaders only)", e.Path, e.BlockKind)
			continue
		}

		appID, err := stringValue(e, "appId")
		if err != nil {
			return nil, err
		}
		topic, err := stringValue(e, "topic")
		if err != nil {
			return nil, err
		}
		contract, err := stringValue(e, "contract")
		if err != nil {
			return nil, fmt.Errorf("%w\nRe-scaffold this integration with a template release that records the contract type, or add \"contract\": \"<TypeName>\" to the record's values.", err)
		}
		pubsub, err := stringValueDefault(e, "pubsub", "pubsub")
		if err != nil {
			return nil, err
		}
		// No CLI-side fallback for the connector: a default would describe a
		// binding the rendered code doesn't use. A component without one is
		// valid topology — it just gets no From/To.
		connector := ""
		if _, ok := e.Values["connector"]; ok {
			if connector, err = stringValue(e, "connector"); err != nil {
				return nil, err
			}
		} else {
			warnf("%s: scaffold record has no connector — re-scaffold with a newer template release to wire From/To", e.Path)
		}

		if prev, ok := byAppID[appID]; ok {
			return nil, fmt.Errorf("duplicate component name %q: declared by both %s and %s (component names must be unique in a system)", appID, prev, e.Path)
		}
		byAppID[appID] = e.Path

		if connector != "" {
			if prev, ok := byConnector[connector]; ok {
				return nil, fmt.Errorf("duplicate connector %q: declared by both %s and %s (each edge block gets its own port; rename one in the record's values)", connector, prev, e.Path)
			}
			byConnector[connector] = e.Path
		}

		key := TopicKey{Pubsub: pubsub, Name: topic}
		if seen, ok := byTopic[key]; ok {
			if seen.Contract != contract {
				return nil, fmt.Errorf("topic %q on pubsub %q has conflicting contracts: %q (%s) vs %q (%s)", topic, pubsub, seen.Contract, firstDir[key], contract, e.Path)
			}
		} else {
			byTopic[key] = Topic{TopicKey: key, Contract: contract, Field: pascalIdent(topic)}
			firstDir[key] = e.Path
		}

		components = append(components, Component{AppID: appID, Kind: e.BlockKind, Topic: key, Connector: connector, Path: e.Path})
	}

	if len(components) == 0 {
		return nil, fmt.Errorf("%w in this directory\nScaffold extractors and loaders first ('intropy int create extractor -n <Name> -s topic=<topic> ...'), then run 'intropy sys create' from the workspace root.", ErrNoComponents)
	}
	switch {
	case len(shared) == 0:
		return nil, fmt.Errorf("no shared contracts project found (template role %q): Topics.cs needs the contract types\nComponents scaffolded from the official extractor/loader templates create a sibling Contracts/ project automatically.", template.RoleSharedLibrary)
	case len(shared) > 1:
		dirs := make([]string, len(shared))
		for i, s := range shared {
			dirs[i] = s.Path
		}
		return nil, fmt.Errorf("found %d shared contract projects (%s); a system host references exactly one — remove or consolidate before re-running", len(shared), strings.Join(dirs, ", "))
	}

	topics := make([]Topic, 0, len(byTopic))
	for _, t := range byTopic {
		topics = append(topics, t)
	}
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Pubsub != topics[j].Pubsub {
			return topics[i].Pubsub < topics[j].Pubsub
		}
		return topics[i].Name < topics[j].Name
	})

	fields := map[string]TopicKey{}
	for _, t := range topics {
		if prev, ok := fields[t.Field]; ok {
			return nil, fmt.Errorf("topics %q (pubsub %q) and %q (pubsub %q) both map to field %s in Topics.cs; rename one topic", prev.Name, prev.Pubsub, t.Name, t.Pubsub, t.Field)
		}
		fields[t.Field] = t.TopicKey
	}

	connectors := make([]Connector, 0, len(byConnector))
	for name := range byConnector {
		connectors = append(connectors, Connector{Name: name, Field: pascalIdent(name)})
	}
	sort.Slice(connectors, func(i, j int) bool { return connectors[i].Name < connectors[j].Name })
	connectorFields := map[string]string{}
	for _, c := range connectors {
		if prev, ok := connectorFields[c.Field]; ok {
			return nil, fmt.Errorf("connectors %q and %q both map to field %s in Connectors.cs; rename one in the record's values", prev, c.Name, c.Field)
		}
		connectorFields[c.Field] = c.Name
	}

	return &Model{Components: components, Topics: topics, Connectors: connectors, Shared: shared[0]}, nil
}

// stringValue returns values[key] as a non-empty string, with errors that
// name the record so the user knows which project to fix.
func stringValue(e template.ScaffoldEntry, key string) (string, error) {
	record := filepath.Join(e.Path, filepath.FromSlash(template.ScaffoldRelPath))
	v, ok := e.Values[key]
	if !ok {
		return "", fmt.Errorf("%s: values.%s is missing", record, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: values.%s has type %T, expected string", record, key, v)
	}
	if s == "" {
		return "", fmt.Errorf("%s: values.%s is empty", record, key)
	}
	return s, nil
}

// stringValueDefault is stringValue with a fallback for records that
// predate the value being recorded.
func stringValueDefault(e template.ScaffoldEntry, key, fallback string) (string, error) {
	if _, ok := e.Values[key]; !ok {
		return fallback, nil
	}
	return stringValue(e, key)
}

// pascalIdent converts a DNS-ish name to a C# identifier: segments split
// on '-', '.' and '_' are title-cased and joined ("order-events" →
// "OrderEvents").
func pascalIdent(s string) string {
	segments := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '.' || r == '_'
	})
	var b strings.Builder
	for _, seg := range segments {
		b.WriteString(strings.ToUpper(seg[:1]))
		b.WriteString(seg[1:])
	}
	return b.String()
}
