package system

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	intropytemplate "github.com/integrio-intropy/intropy-cli/internal/template"
)

// The generated declaration files. Deliberately namespace-less: the host
// project is small enough that the declaration reads as one document, and
// discovery reflects over the assembly regardless.
const topicsFileTmpl = `using Intropy.Topology;
using {{ .Shared.Name }};

/// <summary>The system's topics, each defined once and shared by every component that touches it.</summary>
public static class Topics
{
{{- range $i, $t := .Topics }}
{{- if $i }}
{{ end }}
    /// <summary>{{ $t.Contract }} messages on topic '{{ $t.Name }}' (pubsub '{{ $t.Pubsub }}').</summary>
    public static readonly TopicRef<{{ $t.Contract }}> {{ $t.Field }} = TopicRef<{{ $t.Contract }}>.Define("{{ $t.Pubsub }}", "{{ $t.Name }}");
{{- end }}
}
`

const connectorsFileTmpl = `using Intropy.Topology;

/// <summary>The system's connectors: the named ports its edge blocks reach the outside world through. Each defaults to a local file folder under test/ so the system runs with zero external configuration.</summary>
public static class Connectors
{
{{- range $i, $c := .Connectors }}
{{- if $i }}
{{ end }}
    /// <summary>Local file connector '{{ $c.Name }}' (folder ./test/{{ $c.Name }}). Point it at a real external system and transport when known.</summary>
    public static readonly ConnectorRef {{ $c.Field }} = ConnectorRef.Define("{{ $c.Name }}", Transport.File("./test/{{ $c.Name }}"));
{{- end }}
}
`

const systemClassFileTmpl = `using Intropy.Topology;

/// <summary>The {{ .Name }} system: what exists, and what connects it.</summary>
public sealed class {{ .SystemClass }} : ISystemDefinition
{
    public string SystemName => "{{ .Name }}";

    public void Define(SystemBuilder builder)
    {
{{- range .Components }}
        builder.{{ .Add }}("{{ .AppID }}"){{ .From }}.{{ .Edge }}(Topics.{{ .Field }}){{ .To }}{{ .Schedule }};
{{- end }}
    }
}
`

// defaultExtractorSchedule is the cron every generated extractor starts with:
// once every minute. Extractors are run-to-completion jobs, so the host re-runs
// them on this schedule (CronJob emulation locally, a real CronJob in
// production). The generated file is the user's after `sys create` — edit the
// cron in place.
const defaultExtractorSchedule = "* * * * *"

var (
	topicsFile      = template.Must(template.New("Topics.cs").Parse(topicsFileTmpl))
	connectorsFile  = template.Must(template.New("Connectors.cs").Parse(connectorsFileTmpl))
	systemClassFile = template.Must(template.New("SystemClass.cs").Parse(systemClassFileTmpl))
)

// componentView is a Component with its builder call, topic field and
// connector calls resolved for the system class template.
type componentView struct {
	AppID    string
	Add      string // AddExtractor | AddLoader
	Edge     string // Publishes | Subscribes
	Field    string // the Topics.cs field the component touches
	From     string // `.From(Connectors.X)` for an extractor with a connector, else empty
	To       string // `.To(Connectors.X)` for a loader with a connector, else empty
	Schedule string // `.WithSchedule("...")` for an extractor, else empty
}

// writeTopicsFile overwrites <dir>/Topics.cs with the assembled topic
// declarations.
func writeTopicsFile(dir string, m *Model) error {
	return renderTo(filepath.Join(dir, "Topics.cs"), topicsFile, m)
}

// writeConnectorsFile overwrites <dir>/Connectors.cs with the assembled
// connector declarations. Like the system class it refuses to create the
// file — but here a missing placeholder means the template release predates
// connectors (and its pinned Intropy.Topology has no Transport.File), so the
// system is generated without From/To instead of failing: it reports false
// and the caller degrades the system class the same way.
func writeConnectorsFile(dir string, m *Model, warnf func(format string, args ...any)) (bool, error) {
	path := filepath.Join(dir, "Connectors.cs")
	if _, err := os.Stat(path); err != nil {
		warnf("template rendered no Connectors.cs placeholder — this release predates connectors; generating the system without From/To (upgrade with --version <newer tag> to wire them)")
		return false, nil
	}
	return true, renderTo(path, connectorsFile, m)
}

// writeSystemClassFile overwrites <dir>/<SystemClass>.cs with the assembled
// system definition. It refuses to create the file: the rendered template
// must have left the placeholder, or its name derivation disagrees with
// this CLI and the write would add a second ISystemDefinition instead of
// replacing the placeholder. withConnectors=false (a template release that
// predates Connectors.cs) omits the From/To calls.
func writeSystemClassFile(dir string, m *Model, withConnectors bool) error {
	path := filepath.Join(dir, m.SystemClass+".cs")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("template rendered no %s.cs placeholder: the template's systemClass derivation disagrees with this CLI; upgrade intropy or pass --version <compatible tag>", m.SystemClass)
	}

	views := make([]componentView, len(m.Components))
	fieldByKey := map[TopicKey]string{}
	for _, t := range m.Topics {
		fieldByKey[t.TopicKey] = t.Field
	}
	connectorField := map[string]string{}
	for _, c := range m.Connectors {
		connectorField[c.Name] = c.Field
	}
	for i, c := range m.Components {
		v := componentView{AppID: c.AppID, Field: fieldByKey[c.Topic]}
		field, hasConnector := connectorField[c.Connector]
		hasConnector = hasConnector && withConnectors
		if c.Kind == intropytemplate.BlockKindExtractor {
			v.Add, v.Edge = "AddExtractor", "Publishes"
			if hasConnector {
				v.From = ".From(Connectors." + field + ")"
			}
			// Gated on the same signal as From/To: a template release that
			// predates Connectors.cs also pins an Intropy.Topology without
			// WithSchedule, so the degraded shape must not call it.
			if withConnectors {
				v.Schedule = `.WithSchedule("` + defaultExtractorSchedule + `")`
			}
		} else {
			v.Add, v.Edge = "AddLoader", "Subscribes"
			if hasConnector {
				v.To = ".To(Connectors." + field + ")"
			}
		}
		views[i] = v
	}

	return renderTo(path, systemClassFile, struct {
		Name        string
		SystemClass string
		Components  []componentView
	}{m.Name, m.SystemClass, views})
}

func renderTo(path string, tmpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("generate %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("generate %s: %w", filepath.Base(path), err)
	}
	return nil
}
