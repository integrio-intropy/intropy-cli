package argocd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Terminal operation phases. A sync that reached one of these will not proceed
// on its own, so there is nothing to wait for.
const (
	PhaseFailed = "Failed"
	PhaseError  = "Error"
)

// DefaultPollInterval is how often the application is re-read while waiting.
const DefaultPollInterval = 2 * time.Second

// DefaultTimeout bounds the wait.
const DefaultTimeout = 5 * time.Minute

// WaitOptions configures Wait.
type WaitOptions struct {
	// App is the Application name.
	App string

	// Revision is the git sha that was pushed. Waiting is defined entirely in
	// terms of it.
	Revision string

	Timeout      time.Duration
	PollInterval time.Duration

	// Contains reports whether the revision ArgoCD applied includes ours —
	// equal to it, or a descendant of it. Supplied by the caller because
	// answering it requires git, which this package deliberately knows nothing
	// about. When nil, only exact equality counts.
	Contains func(ctx context.Context, mine, reported string) (bool, error)

	Stderr io.Writer
}

func (o *WaitOptions) applyDefaults() {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.PollInterval <= 0 {
		o.PollInterval = DefaultPollInterval
	}
	if o.Contains == nil {
		o.Contains = func(_ context.Context, mine, reported string) (bool, error) {
			return mine == reported, nil
		}
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
}

// TimeoutError reports that the application did not converge in time.
type TimeoutError struct {
	App      string
	Revision string
	Timeout  time.Duration
	Last     *Application
	Events   []Event
}

func (e *TimeoutError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s did not converge on %s within %s", e.App, shortSHA(e.Revision), e.Timeout)
	if e.Last != nil {
		fmt.Fprintf(&b, "\n  sync:      %s at %s", or(e.Last.Status.Sync.Status, "unknown"), or(shortSHA(e.Last.Status.Sync.Revision), "no revision"))
		fmt.Fprintf(&b, "\n  health:    %s", or(e.Last.Status.Health.Status, "unknown"))
		if msg := e.Last.Status.OperationState.Message; msg != "" {
			fmt.Fprintf(&b, "\n  operation: %s", msg)
		} else if msg := e.Last.Status.Health.Message; msg != "" {
			fmt.Fprintf(&b, "\n  health:    %s", msg)
		}
	}
	for _, ev := range e.Events {
		fmt.Fprintf(&b, "\n  event:     %s: %s", ev.Reason, strings.TrimSpace(ev.Message))
	}
	return b.String()
}

// SyncFailedError reports a sync that reached a terminal failure. Distinct from
// a timeout: this will not improve by waiting.
type SyncFailedError struct {
	App     string
	Phase   string
	Message string
	Events  []Event
}

func (e *SyncFailedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s sync %s", e.App, strings.ToLower(e.Phase))
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	for _, ev := range e.Events {
		fmt.Fprintf(&b, "\n  event: %s: %s", ev.Reason, strings.TrimSpace(ev.Message))
	}
	return b.String()
}

// Wait blocks until the application has applied the given revision and become
// healthy.
//
// The revision check is the whole point. Polling for Synced/Healthy alone reads
// the *previous* revision's perfectly healthy state on the first poll and
// declares success before ArgoCD has done anything at all.
//
// Equality is not enough either: another deployment landing after ours makes
// ArgoCD sync a descendant commit, and our sha would then never appear. So the
// question is whether the revision ArgoCD applied *contains* ours — which is
// why Contains exists rather than a string comparison.
func (c *Client) Wait(ctx context.Context, opts WaitOptions) (*Application, error) {
	opts.applyDefaults()

	// Ask ArgoCD to re-read git now rather than waiting out its own
	// reconciliation interval, which is three minutes by default.
	if err := c.Refresh(ctx, opts.App); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()

	var last *Application
	announced := false

	for {
		app, err := c.Get(ctx, opts.App)
		if err != nil {
			// A cancelled parent context is an interrupt; a deadline is our own
			// timeout, and that is reported with the last state we saw.
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, err
		}
		last = app

		applied, err := opts.Contains(ctx, opts.Revision, app.Status.Sync.Revision)
		if err != nil {
			return nil, err
		}

		if applied {
			if !announced {
				fmt.Fprintf(opts.Stderr, "argocd picked up %s; waiting for it to become healthy\n", shortSHA(opts.Revision))
				announced = true
			}
			if app.Synced() {
				return app, nil
			}
			// A terminal phase will not improve by waiting.
			if phase := app.Status.OperationState.Phase; phase == PhaseFailed || phase == PhaseError {
				return app, &SyncFailedError{
					App:     opts.App,
					Phase:   phase,
					Message: app.Status.OperationState.Message,
					Events:  c.eventsQuietly(ctx, opts.App),
				}
			}
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				goto timedOut
			}
			// The parent was cancelled: the user interrupted.
			return last, ctx.Err()
		case <-ticker.C:
		}
	}

timedOut:
	return last, &TimeoutError{
		App:      opts.App,
		Revision: opts.Revision,
		Timeout:  opts.Timeout,
		Last:     last,
		Events:   c.eventsQuietly(context.WithoutCancel(ctx), opts.App),
	}
}

// eventsQuietly fetches events for a diagnostic message. Failing to read them
// must not replace the error being explained.
func (c *Client) eventsQuietly(ctx context.Context, app string) []Event {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	events, err := c.Events(ctx, app)
	if err != nil {
		return nil
	}
	// Warnings first: they are what explains a stuck sync.
	var warnings, rest []Event
	for _, e := range events {
		if e.Type == "Warning" {
			warnings = append(warnings, e)
		} else {
			rest = append(rest, e)
		}
	}
	out := append(warnings, rest...)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func shortSHA(rev string) string {
	if len(rev) >= 40 {
		return rev[:7]
	}
	return rev
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
