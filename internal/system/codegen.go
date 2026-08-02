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

/// <summary>The system's connectors: the named ports its edge blocks reach the outside world through. Each declares its deployed transport; the development definition resolves it to a local folder under test/ so the system runs with zero external configuration.</summary>
public static class Connectors
{
{{- range $i, $c := .Connectors }}
{{- if $i }}
{{ end }}
    /// <summary>Deployed as SFTP; locally resolved to <c>./test/{{ $c.Name }}</c>.</summary>
    public static readonly ConnectorRef {{ $c.Field }} = ConnectorRef.Define("{{ $c.Name }}", Transport.Sftp());
{{- end }}
}
`

// connectorsFileLegacyTmpl is the pre-development shape: a template release
// without the Development.cs placeholder pins an Intropy.Topology whose
// connectors carry a local file transport directly.
const connectorsFileLegacyTmpl = `using Intropy.Topology;

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

// The mock artifacts are static skeleton files, shipped by the same template
// release that ships the Development.cs placeholder — the paths never vary.
const developmentFileTmpl = `using Intropy.Topology.Generation;

/// <summary>Local OpenAPI-backed substitutes and connector file resolutions for {{ .Name }}.</summary>
public sealed class {{ .ProjectName }}Development : IDevelopmentDefinition
{
    /// <inheritdoc />
    public void Define(DevelopmentBuilder development)
    {
        development.Mock(Services.Idempotency).FromOpenApi("mocks/idempotency-service.openapi.yaml");
        development.Mock(Services.BusinessIncidents).FromOpenApi("mocks/business-incident-service.openapi.yaml");
{{- range .Connectors }}
        development.Files(Connectors.{{ .Field }}).RootPath("./test/{{ .Name }}");
{{- end }}
    }
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
        builder.{{ .Add }}("{{ .AppID }}")
{{- range .Calls }}
            {{ . }}
{{- end }};
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
	topicsFile           = template.Must(template.New("Topics.cs").Parse(topicsFileTmpl))
	connectorsFile       = template.Must(template.New("Connectors.cs").Parse(connectorsFileTmpl))
	connectorsFileLegacy = template.Must(template.New("Connectors.cs").Parse(connectorsFileLegacyTmpl))
	developmentFile      = template.Must(template.New("Development.cs").Parse(developmentFileTmpl))
	systemClassFile      = template.Must(template.New("SystemClass.cs").Parse(systemClassFileTmpl))
)

// componentView is a Component with its builder call and chained wiring calls
// resolved for the system class template. Each call renders on its own line.
type componentView struct {
	AppID string
	Add   string   // AddExtractor | AddLoader
	Calls []string // the chained calls in order, e.g. `.From(...)`, `.Publishes(...)`, `.WithSchedule(...)`
}

// writeTopicsFile overwrites <dir>/Topics.cs with the assembled topic
// declarations.
func writeTopicsFile(dir string, m *Model) error {
	return renderTo(filepath.Join(dir, "Topics.cs"), topicsFile, m)
}

// writeDevelopmentFile overwrites <dir>/<ProjectName>Development.cs with the
// assembled development definition: the platform-service mocks plus one file
// resolution per connector. Like the system class it refuses to create the
// file — a missing placeholder means the template release predates
// development definitions (and its pinned Intropy.Topology has no
// IDevelopmentDefinition), so it reports false and the caller degrades the
// connector and system-class shapes to that era instead of failing.
func writeDevelopmentFile(dir string, m *Model, warnf func(format string, args ...any)) (bool, error) {
	path := filepath.Join(dir, m.ProjectName+"Development.cs")
	if _, err := os.Stat(path); err != nil {
		warnf("template rendered no %sDevelopment.cs placeholder — this release predates development definitions; generating connectors with local file transports (upgrade with --version <newer tag> for deployed transports and local service mocks)", m.ProjectName)
		return false, nil
	}
	return true, renderTo(path, developmentFile, m)
}

// writeConnectorsFile overwrites <dir>/Connectors.cs with the assembled
// connector declarations. Like the system class it refuses to create the
// file — but here a missing placeholder means the template release predates
// connectors entirely, so the system is generated without From/To instead of
// failing: it reports false and the caller degrades the system class the
// same way. withDevelopment=false (a release with connectors but no
// development definitions) keeps the era's local file transports; otherwise
// connectors declare their deployed transport shape and the development
// definition owns the local resolution.
func writeConnectorsFile(dir string, m *Model, withDevelopment bool, warnf func(format string, args ...any)) (bool, error) {
	path := filepath.Join(dir, "Connectors.cs")
	if _, err := os.Stat(path); err != nil {
		warnf("template rendered no Connectors.cs placeholder — this release predates connectors; generating the system without From/To (upgrade with --version <newer tag> to wire them)")
		return false, nil
	}
	tmpl := connectorsFile
	if !withDevelopment {
		tmpl = connectorsFileLegacy
	}
	return true, renderTo(path, tmpl, m)
}

// writeSystemClassFile overwrites <dir>/<SystemClass>.cs with the assembled
// system definition. It refuses to create the file: the rendered template
// must have left the placeholder, or its name derivation disagrees with
// this CLI and the write would add a second ISystemDefinition instead of
// replacing the placeholder. withConnectors=false (a template release that
// predates Connectors.cs) omits the From/To calls; withDevelopment=false
// (a release that predates development definitions, whose skeleton ships no
// Services.cs) omits the Uses calls.
func writeSystemClassFile(dir string, m *Model, withConnectors, withDevelopment bool) error {
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
		v := componentView{AppID: c.AppID}
		topicField := fieldByKey[c.Topic]
		field, hasConnector := connectorField[c.Connector]
		hasConnector = hasConnector && withConnectors
		if c.Kind == intropytemplate.BlockKindExtractor {
			v.Add = "AddExtractor"
			if hasConnector {
				v.Calls = append(v.Calls, ".From(Connectors."+field+")")
			}
			v.Calls = append(v.Calls, ".Publishes(Topics."+topicField+")")
			v.Calls = append(v.Calls, usesCalls(withDevelopment)...)
			// Gated on the same signal as From/To: a template release that
			// predates Connectors.cs also pins an Intropy.Topology without
			// WithSchedule, so the degraded shape must not call it.
			if withConnectors {
				v.Calls = append(v.Calls, `.WithSchedule("`+defaultExtractorSchedule+`")`)
			}
		} else {
			v.Add = "AddLoader"
			v.Calls = append(v.Calls, ".Subscribes(Topics."+topicField+")")
			if hasConnector {
				v.Calls = append(v.Calls, ".To(Connectors."+field+")")
			}
			v.Calls = append(v.Calls, usesCalls(withDevelopment)...)
		}
		views[i] = v
	}

	return renderTo(path, systemClassFile, struct {
		Name        string
		SystemClass string
		Components  []componentView
	}{m.Name, m.SystemClass, views})
}

// usesCalls is the platform-service wiring every block gets: the framework's
// block builders require idempotency and business-incident routing, and the
// skeleton's Services.cs declares both refs. Gated on the development era —
// earlier skeletons ship no Services.cs and pin an Intropy.Topology without
// Uses.
func usesCalls(withDevelopment bool) []string {
	if !withDevelopment {
		return nil
	}
	return []string{".Uses(Services.Idempotency)", ".Uses(Services.BusinessIncidents)"}
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
