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
// shared-library records become the referenced contract project, records
// with a kind in the blockParsers registry become components, and
// everything else is skipped through warnf. The returned model's
// Name/ProjectName/SystemClass are left empty — the caller fills them
// around the template render.
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
		}

		parse, ok := blockParsers[e.BlockKind]
		if !ok {
			warnf("skipping %s: unsupported block kind %q (sys create assembles %s)", e.Path, e.BlockKind, strings.Join(supportedKinds(), ", "))
			continue
		}

		appID, err := stringValue(e, "appId")
		if err != nil {
			return nil, err
		}
		c := Component{AppID: appID, Kind: e.BlockKind, Path: e.Path}
		if err := parse(e, &c); err != nil {
			return nil, err
		}
		if c.missingConnector {
			warnf("%s: scaffold record has no connector — re-scaffold with a newer template release to wire From/To", e.Path)
		}

		if prev, ok := byAppID[appID]; ok {
			return nil, fmt.Errorf("duplicate component name %q: declared by both %s and %s (component names must be unique in a system)", appID, prev, e.Path)
		}
		byAppID[appID] = e.Path

		for _, connector := range c.Connectors {
			if prev, ok := byConnector[connector]; ok {
				return nil, fmt.Errorf("duplicate connector %q: declared by both %s and %s (each block gets its own port; rename one in the record's values)", connector, prev, e.Path)
			}
			byConnector[connector] = e.Path
		}

		if c.Topic != nil {
			key := *c.Topic
			if seen, ok := byTopic[key]; ok {
				if seen.Contract != c.topicContract {
					return nil, fmt.Errorf("topic %q on pubsub %q has conflicting contracts: %q (%s) vs %q (%s)", key.Name, key.Pubsub, seen.Contract, firstDir[key], c.topicContract, e.Path)
				}
			} else {
				byTopic[key] = Topic{TopicKey: key, Contract: c.topicContract, Field: pascalIdent(key.Name)}
				firstDir[key] = e.Path
			}
		}

		components = append(components, c)
	}

	if len(components) == 0 {
		return nil, fmt.Errorf("%w in this directory\nScaffold integrations first ('intropy int create <kind> -n <Name> ...'), then run 'intropy sys create' from the workspace root.", ErrNoComponents)
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
