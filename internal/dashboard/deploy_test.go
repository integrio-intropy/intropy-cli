package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
)

// deployWorkspace writes the layout `sys create` produces inside a domain
// folder, so the served summary carries a domain and a system as well as a name
// — the three things the deploy status command needs to find a component
// unambiguously.
//
// It returns the root-relative path of the one integration in it.
func deployWorkspace(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "sales", "ordersync", "order-extract"), "extractor", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "sales", "ordersync", "system-host"), "ordersync")
	return "sales/ordersync/order-extract"
}

// stubStatus is a StatusResult shaped like the state the release process is
// meant to end in, with the prose the command would have printed beneath it.
func stubStatus() *deploy.StatusResult {
	return &deploy.StatusResult{
		Component: "order-extract",
		Domain:    "sales",
		System:    "ordersync",
		Kind:      "service",
		Environments: []deploy.EnvironmentStatus{{
			Environment: "dev",
			AppName:     "sales-ordersync-order-extract-dev",
			Onboarded:   true,
			Release:     "1.4.2",
			Pins:        []deploy.ResultPin{{Image: "reg/order-extract", Digest: "sha256:ad22d6f2ecbc"}},
			SyncPolicy:  "auto",
			SyncStatus:  "Synced",
		}},
		Consistent: true,
		Summary:    "only dev is onboarded, so there is nothing to compare it with",
		Notes:      []string{"dev pins nothing for reg/other, so it has never been deployed there"},
	}
}

func deployJSON(t *testing.T, h http.Handler, method, path string) map[string]any {
	t.Helper()
	var rec = get(t, h, path)
	if method == http.MethodPost {
		rec = post(t, h, path)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d, want 200: %s", method, path, rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	return body
}

// The endpoint's whole job is to hand the command's own result through without
// editing it — including the prose, which is what keeps the panel and the
// terminal from drawing different conclusions from the same overlays.
func TestDeployStateCarriesTheCommandResultThrough(t *testing.T) {
	path := deployWorkspace(t)
	h := testHandlerWithDeploy(t, ".", func(context.Context, integrationSummary) deployState {
		return deployState{Status: stubStatus(), ReadAt: time.Now()}
	})

	body := deployJSON(t, h, http.MethodGet, "/api/deploy/"+path)
	status, ok := body["status"].(map[string]any)
	if !ok {
		t.Fatalf("status missing from payload: %v", body)
	}
	if status["summary"] != "only dev is onboarded, so there is nothing to compare it with" {
		t.Errorf("summary = %v, want the command's sentence", status["summary"])
	}
	notes, ok := status["notes"].([]any)
	if !ok || len(notes) != 1 {
		t.Fatalf("notes = %v, want the command's one note", status["notes"])
	}
	if status["consistent"] != true {
		t.Errorf("consistent = %v, want true", status["consistent"])
	}
	if body["readAt"] == nil {
		t.Error("readAt should be served so a reader can tell how current this is")
	}
	if _, present := body["error"]; present {
		t.Errorf("a successful read should carry no error: %v", body["error"])
	}
}

// An unconfigured repository, an ambiguous name and a held checkout lock are all
// statements about the lookup rather than about the integration, and the command
// already words each of them well. Rewriting one here would be a second thing to
// keep true, and would risk turning "we could not find out" into "not deployed".
func TestDeployStateServesTheCommandErrorVerbatim(t *testing.T) {
	path := deployWorkspace(t)
	const msg = "no GitOps repository configured; pass --gitops-repo, set INTROPY_GITOPS_REPO, or add gitopsRepo to /home/x/.config/intropy/config.yaml"
	h := testHandlerWithDeploy(t, ".", func(context.Context, integrationSummary) deployState {
		return deployState{Error: msg, ReadAt: time.Now()}
	})

	body := deployJSON(t, h, http.MethodGet, "/api/deploy/"+path)
	if body["error"] != msg {
		t.Errorf("error = %q, want the command's message unchanged:\n%q", body["error"], msg)
	}
	if _, present := body["status"]; present {
		t.Error("an error must not come with a status: an empty ladder reads as 'not deployed'")
	}
}

// The command names the repository it refreshed and any environment ArgoCD could
// not be read for. Both are provenance for what the ladder shows, so they are
// served rather than dropped on the floor.
func TestDeployStateServesTheCommandDiagnostics(t *testing.T) {
	path := deployWorkspace(t)
	h := testHandlerWithDeploy(t, ".", func(context.Context, integrationSummary) deployState {
		return deployState{
			Status:      stubStatus(),
			Diagnostics: diagnosticLines("refreshing git@example.com:acme/gitops.git\nwarning: not reading sync or health from ArgoCD: no token\n"),
			ReadAt:      time.Now(),
		}
	})

	body := deployJSON(t, h, http.MethodGet, "/api/deploy/"+path)
	got, ok := body["diagnostics"].([]any)
	if !ok || len(got) != 2 {
		t.Fatalf("diagnostics = %v, want the two stderr lines", body["diagnostics"])
	}
	if got[0] != "refreshing git@example.com:acme/gitops.git" {
		t.Errorf("first diagnostic = %v, want the repository it refreshed", got[0])
	}
}

func TestDiagnosticLines(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
		want   []string
	}{
		{"empty is nil so the field is omitted", "", nil},
		{"blank lines only", "\n  \n\n", nil},
		{"trailing newline does not make an entry", "refreshing x\n", []string{"refreshing x"}},
		{"one entry per line, trimmed", " a \nb\n\nc\n", []string{"a", "b", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := diagnosticLines(tc.stderr); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("diagnosticLines(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

// Running the command refreshes a GitOps checkout under an exclusive lock, so a
// GET must not pay for it more than once. A POST is how you ask for a fresh
// read — after deploying from another terminal, say.
func TestDeployStateCachedUntilRefresh(t *testing.T) {
	path := deployWorkspace(t)
	calls := 0
	h := testHandlerWithDeploy(t, ".", func(context.Context, integrationSummary) deployState {
		calls++
		return deployState{Status: stubStatus()}
	})

	get(t, h, "/api/deploy/"+path)
	get(t, h, "/api/deploy/"+path)
	if calls != 1 {
		t.Fatalf("provider calls after two GETs = %d, want 1", calls)
	}
	if rec := post(t, h, "/api/deploy/"+path); rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", rec.Code)
	}
	if calls != 2 {
		t.Fatalf("provider calls after POST = %d, want 2", calls)
	}
	get(t, h, "/api/deploy/"+path)
	if calls != 2 {
		t.Fatalf("provider calls after post-refresh GET = %d, want 2", calls)
	}
}

// The cache is per integration, not one slot for the workspace: reading one
// component's state must not make the dashboard claim to know another's.
func TestDeployStateCachedPerIntegration(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "sales", "ordersync", "order-extract"), "extractor", "v0.2.0")
	writeScaffold(t, filepath.Join(tmp, "sales", "ordersync", "order-load"), "loader", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "sales", "ordersync", "system-host"), "ordersync")

	var asked []string
	h := testHandlerWithDeploy(t, ".", func(_ context.Context, s integrationSummary) deployState {
		asked = append(asked, s.Name)
		return deployState{Status: stubStatus()}
	})

	get(t, h, "/api/deploy/sales/ordersync/order-extract")
	get(t, h, "/api/deploy/sales/ordersync/order-load")
	if want := []string{"order-extract", "order-load"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("provider asked about %v, want %v", asked, want)
	}
}

// Component names are not unique across a GitOps tree, and the command refuses
// an ambiguous one rather than picking. Passing the domain and system it derived
// is what lets it answer at all, so the summary must reach the provider whole.
func TestDeployStatePassesTheFullCoordinate(t *testing.T) {
	path := deployWorkspace(t)
	var got integrationSummary
	h := testHandlerWithDeploy(t, ".", func(_ context.Context, s integrationSummary) deployState {
		got = s
		return deployState{Status: stubStatus()}
	})

	get(t, h, "/api/deploy/"+path)
	if got.Name != "order-extract" || got.Domain != "sales" || got.System != "ordersync" {
		t.Errorf("provider got %q/%q/%q, want sales/ordersync/order-extract",
			got.Domain, got.System, got.Name)
	}
}

// The command holds an exclusive lock on the shared GitOps checkout for its whole
// run, so two of them at once would have one fail on the other's lock. The
// handler serialises instead, which turns that failure into a wait.
func TestDeployStateSerialisesConcurrentReads(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "sales", "ordersync", "order-extract"), "extractor", "v0.2.0")
	writeScaffold(t, filepath.Join(tmp, "sales", "ordersync", "order-load"), "loader", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "sales", "ordersync", "system-host"), "ordersync")

	var mu sync.Mutex
	inFlight, overlapped := 0, false
	h := testHandlerWithDeploy(t, ".", func(context.Context, integrationSummary) deployState {
		mu.Lock()
		inFlight++
		if inFlight > 1 {
			overlapped = true
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return deployState{Status: stubStatus()}
	})

	var wg sync.WaitGroup
	for _, p := range []string{"order-extract", "order-load"} {
		wg.Go(func() { get(t, h, "/api/deploy/sales/ordersync/"+p) })
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if overlapped {
		t.Error("two reads ran at once; they would contend over the GitOps checkout lock")
	}
}

// The command keeps the checkout lock for as long as it runs, so an ArgoCD server
// that accepts connections and never answers would keep it — blocking the user's
// own intropy deploy. The handler bounds the run rather than trusting it.
func TestDeployStateBoundsTheProviderRun(t *testing.T) {
	path := deployWorkspace(t)
	var deadline time.Time
	var hasDeadline bool
	h := testHandlerWithDeploy(t, ".", func(ctx context.Context, _ integrationSummary) deployState {
		deadline, hasDeadline = ctx.Deadline()
		return deployState{Status: stubStatus()}
	})

	get(t, h, "/api/deploy/"+path)
	if !hasDeadline {
		t.Fatal("the provider's context should carry a deadline")
	}
	if left := time.Until(deadline); left <= 0 || left > deployStateTimeout {
		t.Errorf("deadline is %v away, want within (0, %v]", left, deployStateTimeout)
	}
}

// Not every path is an integration, and the deployment endpoint must agree with
// the detail endpoint about which ones are — including the workspace root, whose
// identifier is "." but whose "." segment never survives the trip: the mux
// normalises it away, so the request arrives with an empty path.
func TestDeployStatePathResolution(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, tmp, "extractor", "v0.2.0")
	h := testHandlerWithDeploy(t, ".", func(context.Context, integrationSummary) deployState {
		return deployState{Status: stubStatus()}
	})

	if rec := get(t, h, "/api/deploy/"); rec.Code != http.StatusOK {
		t.Errorf("GET the empty path = %d, want 200: it names the root integration", rec.Code)
	}
	// The dotted form is what a client would write; the mux redirects it onto
	// the empty form above rather than serving it, which is why that rule exists.
	if rec := get(t, h, "/api/deploy/."); rec.Code != http.StatusTemporaryRedirect {
		t.Errorf("GET the dotted path = %d, want a redirect onto /api/deploy/", rec.Code)
	}
	rec := get(t, h, "/api/deploy/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET an unknown path = %d, want 404", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("404 body not JSON: %v", err)
	}
	if body["error"] != "integration not found: nope" {
		t.Errorf("404 error = %v, want the same shape the detail endpoint uses", body["error"])
	}
}

// A support project is infrastructure rather than a catalog entry, so it has no
// detail view — and must have no deployment view either, or the two endpoints
// would disagree about what exists.
func TestDeployStateExcludesSupportProjects(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffoldRole(t, filepath.Join(tmp, "system-host"), "system-host", "v0.2.0", "system-host")
	h := testHandlerWithDeploy(t, ".", func(context.Context, integrationSummary) deployState {
		t.Error("the provider should not be asked about a support project")
		return deployState{}
	})

	if rec := get(t, h, "/api/deploy/system-host"); rec.Code != http.StatusNotFound {
		t.Errorf("GET the host = %d, want 404", rec.Code)
	}
}
