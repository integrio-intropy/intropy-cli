package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
)

// deployProvider reports what one integration has deployed, where.
//
// Like topologyProvider it never fails: a component that cannot be described
// comes back with Error set rather than an error return, because "we could not
// find out" and "it is not deployed" must not render the same. Which of the two
// it was is the command's business to say, not this package's to guess.
type deployProvider func(ctx context.Context, s integrationSummary) deployState

// deployState is one integration's deployment state, or the reason it has none.
type deployState struct {
	// Status is deploy status's own result, passed through unchanged so that the
	// dashboard and the command line cannot come to different conclusions about
	// the same overlays.
	Status *deploy.StatusResult `json:"status,omitempty"`

	// Error is the command's own message, verbatim — an unconfigured GitOps
	// repository, a component name that matches several, a checkout another
	// deploy is holding. Served rather than restated: those messages already
	// name every way to resolve them, and a paraphrase here would be a second
	// thing to keep true.
	//
	// None of them is a statement about the integration. Only the environments
	// inside Status say whether something is deployed.
	Error string `json:"error,omitempty"`

	// Diagnostics is what the command wrote to stderr, one entry per line:
	// which repository it refreshed, and any environment ArgoCD could not be
	// read for. Provenance for what Status shows, not a failure.
	Diagnostics []string `json:"diagnostics,omitempty"`

	// ReadAt is when the command ran, so a reader can tell how current this is.
	ReadAt time.Time `json:"readAt"`
}

// statusCommandProvider is the default provider: it runs the deploy status
// command with a JSON writer and decodes what it prints.
//
// Calling the command rather than reading the GitOps tree here is deliberate.
// Status already owns every refusal that matters — an unset gitopsRepo, an
// ambiguous component, an overlay that pins a tag, an environment nobody has
// onboarded, an unreachable ArgoCD — and each of those is a case where the
// honest answer is subtle and already written down. A second implementation
// would have to get all of them right again.
//
// Domain and System travel with the component name for the same reason
// deploy status takes them as flags: component names are not unique across a
// GitOps tree, and the command refuses an ambiguous one rather than picking.
func statusCommandProvider(version string) deployProvider {
	userAgent := "intropy-cli/" + version
	return func(ctx context.Context, s integrationSummary) deployState {
		var out, logs bytes.Buffer
		err := deploy.Status(ctx, deploy.StatusOptions{
			Component:    s.Name,
			Domain:       s.Domain,
			System:       s.System,
			OutputFormat: deploy.OutputJSON,
			UserAgent:    userAgent,
			Stdout:       &out,
			Stderr:       &logs,
		})

		state := deployState{Diagnostics: diagnosticLines(logs.String()), ReadAt: time.Now()}
		if err != nil {
			state.Error = err.Error()
			return state
		}

		var res deploy.StatusResult
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			// The command succeeded but printed something this cannot read —
			// a version skew rather than anything about the deployment.
			state.Error = fmt.Sprintf("read deploy status output: %v", err)
			return state
		}
		state.Status = &res
		return state
	}
}

// diagnosticLines splits captured stderr into entries, dropping blanks. Nil for
// no output, so the field is omitted rather than served as an empty array.
func diagnosticLines(stderr string) []string {
	var out []string
	for line := range strings.SplitSeq(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
