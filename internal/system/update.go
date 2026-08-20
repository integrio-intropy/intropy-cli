package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// ErrNoOrphans reports an update that found nothing to add. Callers can
// match it to suppress the message; the update itself prints the empty
// state and returns nil.
var ErrNoOrphans = errors.New("no orphaned components found")

// UpdateOptions configures Update. StartDir is the invocation directory —
// the workspace root, by the command's contract — and every writer has the
// same defaults as CreateOptions.
type UpdateOptions struct {
	StartDir   string
	Force      bool
	DryRun     bool
	OutputJSON string // "-" means stdout
	Stdout     io.Writer
	Stderr     io.Writer
	HTTP       *http.Client
	UserAgent  string

	// Template, Owner, Repo and Version override the pin the host's
	// scaffold record carries. Zero values render with exactly what
	// sys create pinned. GitHubBaseURL is a test-only seam.
	Template      string
	Owner         string
	Repo          string
	Version       string
	GitHubBaseURL string
}

// UpdateResult is the machine-readable summary --output json writes.
// Field names are stable and additive-only.
type UpdateResult struct {
	HostDir  string                 `json:"hostDir"`
	System   string                 `json:"system"`
	Added    []string               `json:"added"`
	Kept     []string               `json:"kept,omitempty"` // declared components whose scaffold is gone
	Files    []template.FileOutcome `json:"files,omitempty"`
	DryRun   bool                   `json:"dryRun"`
	Template string                 `json:"template,omitempty"`
	Owner    string                 `json:"owner,omitempty"`
	Repo     string                 `json:"repo,omitempty"`
	Version  string                 `json:"version,omitempty"`
}

// updatePlan is the pure half of an update: the located host, the baseline
// the host record stores, the orphans a workspace scan found, and the
// merged values a re-render consumes.
type updatePlan struct {
	hostDir  string             // host project directory, as located
	hostRec  *template.Scaffold // the host's own record
	baseline map[string]any     // the record's values, preserved verbatim
	orphans  []Component        // assemblable scaffolds the record does not declare
	kept     []string           // baseline appIds with no scaffold left
}

func (o *UpdateOptions) applyDefaults() {
	if o.StartDir == "" {
		o.StartDir = "."
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
}

// Update folds orphaned components — scaffolded integrations the host's
// record does not declare — into the workspace's system host. The declared
// baseline is the host record's stored values, never a re-scan, so a
// component whose scaffold disappeared stays declared. The record is
// rewritten last, after every declaration file landed, and never on a
// conflict, so a failed update retries against an honest baseline.
func Update(ctx context.Context, opts UpdateOptions) error {
	opts.applyDefaults()

	plan, err := planUpdate(opts)
	if err != nil {
		return err
	}
	if len(plan.orphans) == 0 {
		fmt.Fprintln(opts.Stderr, ErrNoOrphans.Error())
		return nil
	}
	for _, appID := range plan.kept {
		fmt.Fprintf(opts.Stderr, "note: component %s declared but no scaffold found — kept as declared\n", appID)
	}

	// The payload and the record rewrite both start from the record's own
	// values, so hand-recorded keys survive the round-trip untouched.
	merged := make(map[string]any, len(plan.baseline))
	for k, v := range plan.baseline {
		merged[k] = v
	}
	mergedComponents, err := mergedComponentEntries(plan)
	if err != nil {
		return err
	}
	merged["components"] = mergedComponents
	if err := mergeWiring(plan, merged); err != nil {
		return err
	}

	names := make([]string, len(plan.orphans))
	for i, c := range plan.orphans {
		names[i] = c.AppID
	}
	fmt.Fprintf(opts.Stderr, "updating %s: adding components %s\n", plan.hostDir, strings.Join(names, ", "))

	prep, err := template.PrepareCreate(ctx, template.CreateOptions{
		Template:      orDefault(opts.Template, plan.hostRec.Template),
		Version:       orDefault(opts.Version, plan.hostRec.Version),
		SetValues:     merged,
		NoInput:       true,
		OnManifest:    requireFactsPayload,
		Stdin:         strings.NewReader(""),
		Stdout:        opts.Stdout,
		Stderr:        opts.Stderr,
		HTTP:          opts.HTTP,
		UserAgent:     opts.UserAgent,
		Owner:         orDefault(opts.Owner, plan.hostRec.Owner),
		Repo:          orDefault(opts.Repo, plan.hostRec.Repo),
		GitHubBaseURL: opts.GitHubBaseURL,
	})
	if err != nil {
		return err
	}
	defer prep.Cleanup()

	outcomes, err := template.RenderUpdate(prep.SkeletonRoot, plan.hostDir, prep.Values, prep.Manifest.Spec.Files, template.RenderUpdateOptions{
		Force:  opts.Force,
		DryRun: opts.DryRun,
	})
	if err != nil {
		return err
	}
	if conflicts := conflictPaths(outcomes); len(conflicts) > 0 {
		return fmt.Errorf("declaration files differ from what the update would render: %s\nreconcile them by hand, or re-run with --force to overwrite", strings.Join(conflicts, ", "))
	}

	// The record goes last: a rerun after a partial failure must recompute
	// the same orphan set, which only an un-rewritten baseline guarantees.
	if !opts.DryRun {
		updated := *plan.hostRec
		updated.Values = merged
		if err := template.WriteScaffold(plan.hostDir, updated); err != nil {
			return err
		}
		fmt.Fprintf(opts.Stderr, "updated %s: added %d component(s)\n", plan.hostDir, len(plan.orphans))
		warnTopologyDrift(ctx, opts, plan, mergedComponents)
	}

	return maybeWriteUpdateResult(opts, plan, names, outcomes, prep)
}

// planUpdate locates the workspace's single system host and computes the
// orphan diff. Nothing is written; the scan root is the host's parent so a
// sibling scaffold is always seen, and running from inside the host
// directory is a clear error rather than a silently empty scan.
func planUpdate(opts UpdateOptions) (*updatePlan, error) {
	hosts, warnings := template.ListSystemHosts(opts.StartDir)
	for _, w := range warnings {
		fmt.Fprintf(opts.Stderr, "warning: %v\n", w)
	}
	switch len(hosts) {
	case 0:
		// A scaffold record in the invocation directory itself means the
		// user is standing in a project, not above one.
		if _, root, err := template.FindScaffold(opts.StartDir); err == nil {
			return nil, fmt.Errorf("%s is a scaffolded project, not a workspace\nrun 'intropy sys update' from the workspace root — the host's parent directory", root)
		}
		return nil, errors.New("no system host found in this directory\nrun 'intropy sys create' first, or run 'intropy sys update' from the workspace root")
	case 1:
	default:
		dirs := make([]string, len(hosts))
		for i, h := range hosts {
			dirs[i] = h.Path
		}
		return nil, fmt.Errorf("found %d system hosts (%s); 'sys update' updates exactly one — run it from a workspace with a single host", len(hosts), strings.Join(dirs, ", "))
	}
	host := hosts[0]

	// Running from inside the host means the sibling scan below sees
	// nothing; refuse rather than report a phantom no-op.
	absStart, err := filepath.Abs(opts.StartDir)
	if err != nil {
		return nil, err
	}
	absHost, err := filepath.Abs(host.Path)
	if err != nil {
		return nil, err
	}
	if rel, err := filepath.Rel(absHost, absStart); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s is inside the system host %s\nrun 'intropy sys update' from the workspace root — the host's parent directory", opts.StartDir, host.Path)
	}

	// The baseline is the record's stored values: components, topics and
	// ports arrive as []any of map[string]any after the JSON round-trip.
	recordPath := filepath.Join(host.Path, filepath.FromSlash(template.ScaffoldRelPath))
	rawComponents, ok := host.Values["components"].([]any)
	if !ok {
		return nil, fmt.Errorf("%s: values.components is missing or not a list — the record does not match what sys create writes", recordPath)
	}
	declared := map[string]bool{}
	for _, c := range rawComponents {
		m, ok := c.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: values.components entry has type %T, expected object", recordPath, c)
		}
		appID, _ := m["appId"].(string)
		if appID == "" {
			return nil, fmt.Errorf("%s: values.components entry has no appId", recordPath)
		}
		declared[appID] = true
	}

	// Candidates come from the sibling scan; the host's parent is the
	// system root regardless of where below it the command was invoked.
	scanRoot := filepath.Dir(host.Path)
	entries, scanWarnings := template.ListScaffolds(scanRoot)
	for _, w := range scanWarnings {
		fmt.Fprintf(opts.Stderr, "warning: %v\n", w)
	}
	warnf := func(format string, args ...any) {
		fmt.Fprintf(opts.Stderr, "warning: "+format+"\n", args...)
	}
	model, err := assembleCandidates(entries, warnf)
	if err != nil {
		return nil, err
	}

	var orphans []Component
	for _, c := range model.Components {
		if !declared[c.AppID] {
			orphans = append(orphans, c)
		}
	}
	var kept []string
	present := map[string]bool{}
	for _, c := range model.Components {
		present[c.AppID] = true
	}
	for appID := range declared {
		if !present[appID] {
			kept = append(kept, appID)
		}
	}
	sort.Strings(kept)

	return &updatePlan{
		hostDir:  host.Path,
		hostRec:  &host.Scaffold,
		baseline: host.Values,
		orphans:  orphans,
		kept:     kept,
	}, nil
}

// assembleCandidates is Assemble without the zero-component refusal: an
// update against a workspace whose only scaffolds are already declared is
// a no-op, not an error.
func assembleCandidates(entries []template.ScaffoldEntry, warnf func(format string, args ...any)) (*Model, error) {
	model, err := Assemble(entries, warnf)
	if errors.Is(err, ErrNoComponents) {
		return &Model{}, nil
	}
	return model, err
}

// mergedComponentEntries appends each orphan's payload entry to the
// record's stored component list. The stored entries pass through
// untouched — that is what makes a vanished scaffold unable to remove its
// component.
func mergedComponentEntries(plan *updatePlan) ([]any, error) {
	stored, _ := plan.baseline["components"].([]any)
	merged := make([]any, 0, len(stored)+len(plan.orphans))
	merged = append(merged, stored...)
	for _, c := range plan.orphans {
		entry := map[string]any{
			"appId": c.AppID,
			"kind":  c.Kind,
		}
		if c.Topic != nil {
			entry["topic"] = map[string]any{
				"pubsub": c.Topic.Pubsub,
				"name":   c.Topic.Name,
			}
		}
		switch len(c.Ports) {
		case 0:
		case 1:
			entry["port"] = c.Ports[0]
		default:
			entry["fromPort"] = c.Ports[0]
			entry["toPort"] = c.Ports[1]
		}
		merged = append(merged, entry)
	}
	return merged, nil
}

// mergeWiring folds the orphans' topics and ports into the record's stored
// lists, deduplicating by (pubsub, name) and name respectively. A stored
// entry passes through verbatim; only genuinely new names are appended.
// Component payloads build their wiring against these lists, so an orphan
// naming a topic or port the host never declared must add it here.
func mergeWiring(plan *updatePlan, merged map[string]any) error {
	recordPath := filepath.Join(plan.hostDir, filepath.FromSlash(template.ScaffoldRelPath))

	type topicKey struct{ pubsub, name string }
	seenTopics := map[topicKey]bool{}
	storedTopics, _ := plan.baseline["topics"].([]any)
	topics := make([]any, 0, len(storedTopics))
	for _, t := range storedTopics {
		m, ok := t.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: values.topics entry has type %T, expected object", recordPath, t)
		}
		pubsub, _ := m["pubsub"].(string)
		name, _ := m["name"].(string)
		seenTopics[topicKey{pubsub, name}] = true
		topics = append(topics, t)
	}

	seenPorts := map[string]bool{}
	storedPorts, _ := plan.baseline["ports"].([]any)
	ports := make([]any, 0, len(storedPorts))
	for _, p := range storedPorts {
		m, ok := p.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: values.ports entry has type %T, expected object", recordPath, p)
		}
		name, _ := m["name"].(string)
		seenPorts[name] = true
		ports = append(ports, p)
	}

	// Each orphan contributes the wiring its scaffold record declared;
	// Assemble already deduplicated and cross-checked these against every
	// other scanned scaffold.
	for _, c := range plan.orphans {
		if c.Topic != nil {
			key := topicKey{c.Topic.Pubsub, c.Topic.Name}
			if !seenTopics[key] {
				seenTopics[key] = true
				topics = append(topics, map[string]any{
					"pubsub":   c.Topic.Pubsub,
					"name":     c.Topic.Name,
					"contract": c.topicContract,
				})
			}
		}
		for _, p := range c.Ports {
			if !seenPorts[p] {
				seenPorts[p] = true
				ports = append(ports, map[string]any{"name": p})
			}
		}
	}

	merged["topics"] = topics
	merged["ports"] = ports
	return nil
}

// conflictPaths lists the files the update refused to overwrite.
func conflictPaths(outcomes []template.FileOutcome) []string {
	var conflicts []string
	for _, o := range outcomes {
		if o.Outcome == template.OutcomeConflict {
			conflicts = append(conflicts, o.Path)
		}
	}
	return conflicts
}

// warnTopologyDrift runs the post-update advisory check: the host's graph
// output should now name every component the update declares. Every
// failure mode — no dotnet, a broken build, a disagreeing graph — is a
// stderr warning; the update itself already succeeded.
func warnTopologyDrift(ctx context.Context, opts UpdateOptions, plan *updatePlan, mergedComponents []any) {
	t, err := topology.RunGraph(ctx, plan.hostDir)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "warning: topology check skipped: %v\n", err)
		return
	}
	graphed := map[string]bool{}
	for _, c := range t.Components {
		graphed[c.Name] = true
	}
	var missing []string
	for _, c := range mergedComponents {
		m := c.(map[string]any)
		appID, _ := m["appId"].(string)
		if !graphed[appID] {
			missing = append(missing, appID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		fmt.Fprintf(opts.Stderr, "warning: topology check: host graph does not declare %s — rebuild or check the rendered definitions\n", strings.Join(missing, ", "))
	}
}

func maybeWriteUpdateResult(opts UpdateOptions, plan *updatePlan, added []string, outcomes []template.FileOutcome, prep *template.PreparedCreate) error {
	if opts.OutputJSON == "" {
		return nil
	}
	absHost, err := filepath.Abs(plan.hostDir)
	if err != nil {
		absHost = plan.hostDir
	}
	result := UpdateResult{
		HostDir:  absHost,
		System:   prep.Values["name"].(string),
		Added:    added,
		Kept:     plan.kept,
		Files:    outcomes,
		DryRun:   opts.DryRun,
		Template: prep.Template,
		Owner:    prep.Owner,
		Repo:     prep.Repo,
		Version:  prep.Version,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("write --output: %w", err)
	}
	data = append(data, '\n')
	if opts.OutputJSON == "-" {
		if _, err := opts.Stdout.Write(data); err != nil {
			return fmt.Errorf("write --output: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(opts.OutputJSON, data, 0o644); err != nil {
		return fmt.Errorf("write --output: %w", err)
	}
	return nil
}

// orDefault picks the first non-empty string: an explicit flag over the
// record's pin.
func orDefault(flag, recorded string) string {
	if flag != "" {
		return flag
	}
	return recorded
}
