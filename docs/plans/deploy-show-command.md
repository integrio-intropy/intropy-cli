# Plan: `intropy deploy show` — describe the GitOps repository, and surface it on the dashboard

## Intent

The GitOps repository is the source of truth for what environments exist, how they
promote, what platform they target, which systems/components are onboarded where,
and what holds secret material. None of that is currently inspectable as one thing:
`deploy status` answers "one component across environments, enriched with ArgoCD
liveness", and nothing answers "the repository as a whole, as declared in git".

This plan adds:

1. `intropy deploy show` — a read-only command that prints the GitOps repository's
   declared state: registry, ArgoCD location, platform, environments (in promotion
   order), systems/components, and secret-bearing resources.
2. A dashboard section that consumes the same operation as JSON, so the web UI can
   render "what environments exist, which secrets we use, what systems we connect
   to" without re-deriving any of it.

Explicitly out of scope: ArgoCD liveness enrichment (that is `deploy status`'s job),
per-secret introspection of live clusters, and any mutation. The command reads git
and nothing else.

## Design decisions (settled — do not relitigate without reason)

- **Verb is `show`, parent is `deploy`.** House verb table (AGENTS.md): `show`
  prints the details of exactly one thing. The one thing here is the GitOps
  repository. It sits under `deploy` because every sibling operates on the same
  checkout and shares the `--gitops-repo` resolution logic. No positional argument.
- **Declaration-only, no rendering.** Do NOT run `kustomize build`. The `deploy diff`
  Long help already records why: a local build is not what ArgoCD applies, and an
  answer that is not the truth is worse than none. Rendering would also make the
  command network-dependent (remote bases, helm inflation) and slow. Secret
  discovery instead walks the kustomize *inputs* — the YAML files under each
  component's `base/` and `overlays/<env>/` directories — offline and with
  per-file provenance. What this avoids is a *secret-naming* convention; the
  scan is still scoped to the known layout (component dirs, `base/`,
  `overlays/`, `kustomization.yaml`), not every YAML file in the repo.
- **Secrets are reported, not enumerated from a schema.** There is no secrets schema
  in `deploy.yaml`. What exists: `platform.secretStore` (repo-wide store name) and
  `kind: shared` components that own secret material. `show` reports the store, plus
  the secret-ish resources found by scanning shared components' YAML: `Secret`,
  `ExternalSecret`, `SealedSecret`, and `secretGenerator` entries in
  `kustomization.yaml`. It never decrypts, never reads values.
- **No ArgoCD calls.** The command must work with only the git checkout. Diagnostics
  (checkout refresh, unreadable overlay) go to stderr; results to stdout.
- **Never fails the dashboard.** The dashboard provider pattern (see
  `internal/dashboard/deploy.go`) passes the command's error message through
  verbatim with `Error` set, rendered distinctly from "nothing declared".

## House-style constraints (from AGENTS.md — the reviewer will check these)

- `Short:` one line, imperative verb, no trailing period.
- `Long:` ≤ ~200 words. What it does, what it reads, what it writes (nothing), what
  can go wrong that the user can prevent. No philosophy, no GitOps advocacy.
- Reuse flag constants from `cmd/intropy/flagtext.go`: `flagUsageGitopsRepo`,
  `flagUsageOutput`. This command takes neither `--env` nor `--domain`/`--system` —
  it is the whole repository. Do not add new shared constants for flags only this
  command has.
- stdout = results, stderr = progress/diagnostics. Progress messages lowercase,
  present tense, no trailing period. `openSession` already prints `refreshing %s`
  (internal/deploy/session.go:53) *before* the fetch; nothing in the codebase
  prints a post-refresh revision, so if the header block shows the revision (it
  does — see below), the command prints its own post-refresh stderr line, e.g.
  `refreshed <url> at <short-sha>`. Do not improvise other wording.
- A commit SHA is shown short (7 chars) in plain output; the JSON contract
  carries the full value.
- Doc comments on every exported symbol; explain why, not what. No TODO/FIXME.
- Exit 0 even when *tree contents* are surprising (a half-onboarded component,
  an overlay that failed to load). This command reports; it does not gate. The
  fatal cases — the ones that return a non-nil error — are exactly: unresolved
  GitOps repo, checkout/lock failure, missing or invalid `deploy.yaml` (all
  surfaced by `openSession`), and an invalid `--output` value.

## Layout of the change

### 1. `internal/deploy/show.go` (new) + `internal/deploy/show_test.go` (new)

Exported entry point, mirroring the shape of `Status` in `internal/deploy/status.go`:

```go
// Show reports what the GitOps repository declares: ...
func Show(ctx context.Context, opts ShowOptions) error
```

`ShowOptions` fields (mirror `StatusOptions` minus component/env/argocd):
`GitopsRepo`, `OutputFormat` (`OutputPlain` | `OutputJSON`), `UserAgent`,
`Stdout`, `Stderr`, and the same session/checkout resolution plumbing `Status`
uses via `openSession(ctx, opts.session(), "git")` — note the `"git"` binary only,
no kustomize, matching the comment in `Status` ("demanding a binary it never runs
would be a prerequisite for no reason").

Result type `ShowResult` (JSON contract). Follow the precedent in
`internal/deploy/result.go`: `ShowResult` gets a doc comment stating field names
are stable and additive-only. The sub-structs are spelled out in full below —
field names and `omitempty` here are the contract, not sketches:

```go
type ShowResult struct {
    Repository   RepositoryInfo    `json:"repository"`
    Registry     string            `json:"registry"`
    Argocd       ArgocdInfo        `json:"argocd"`
    Platform     PlatformInfo      `json:"platform"`
    Environments []EnvironmentInfo `json:"environments"` // promotion order
    Systems      []SystemInfo      `json:"systems"`
}

type RepositoryInfo struct {
    URL      string `json:"url"`
    Revision string `json:"revision"` // full SHA in JSON; plain output shortens it
}

type ArgocdInfo struct {
    Server       string `json:"server"`
    AppNamespace string `json:"appNamespace"`
}

// PlatformConfig values are deliberately unvalidated (config.go:71-75) and any
// of the three may be empty, so every field is omitempty: absent means "not
// declared", which the UI must be able to tell apart from an empty string.
type PlatformInfo struct {
    Provider    string `json:"provider,omitempty"`
    Pubsub      string `json:"pubsub,omitempty"`
    SecretStore string `json:"secretStore,omitempty"`
}

type EnvironmentInfo struct {
    Name                 string   `json:"name"`
    Sync                 string   `json:"sync"` // "auto" | "manual"
    PromotesFrom         []string `json:"promotesFrom,omitempty"`
    RequireSourceHealthy bool     `json:"requireSourceHealthy,omitempty"`
    // Scratch is carried verbatim from deploy.yaml, where its own doc says
    // the semantics are not yet defined (config.go:124-127). Report it in
    // JSON; do not give it presence in the plain table.
    Scratch bool `json:"scratch,omitempty"`
}

type SystemInfo struct {
    Domain     string          `json:"domain"`
    System     string          `json:"system"`
    Components []ComponentInfo `json:"components"`
}

type ComponentInfo struct {
    Name   string   `json:"name"`
    Kind   string   `json:"kind"` // "service" | "shared"
    Images []string `json:"images,omitempty"`
    // Environments is the component.yaml declared list verbatim, NOT reordered
    // into promotion order: this command describes declarations, and reordering
    // would hide what the file actually says.
    Environments []string    `json:"environments"`
    Secrets      []SecretRef `json:"secrets,omitempty"` // shared components only, in practice
    Error        string      `json:"error,omitempty"`   // unreadable component.yaml etc.
}

type SecretRef struct {
    Kind string `json:"kind"` // Secret | ExternalSecret | SealedSecret | secretGenerator
    Name string `json:"name"`
    // Component is the owning component's coordinate rendered with
    // gitops.Coordinate.String() ("domain/system/component", layout.go:44).
    // It is redundant with the nesting but lets the dashboard render a flat
    // secrets list without walking the systems tree.
    Component string `json:"component"`
    File      string `json:"file,omitempty"` // repo-relative file; empty for secretGenerator
}
```

Assembly logic:

1. Open the session; `s.repo` and `s.deployCfg` as in `Status`. The repository
   URL comes from session resolution; the revision comes from
   `s.repo.Git.HEAD(ctx)` (internal/git/client.go:99) — there is no revision
   field on `gitops.Repository`. Store the full SHA in `RepositoryInfo.Revision`;
   shorten for plain output with `git.ShortSHA` (used at status.go:304).
2. `Registry`, `Argocd`, `Platform` map straight off `gitops.DeployConfig`.
3. Environments: iterate `deployCfg.PromotionOrder()` (NOT alphabetical — see the
   comment on `PromotionOrder`; "dev, prod, staging tells the wrong story"). Pull
   `Sync`, `PromotesFrom`, `RequireSourceHealthy`, `Scratch` from each
   `EnvironmentConfig`.
4. Systems/components: `gitops.ListComponents(root)` already returns every
   `Coordinate` sorted. Group by `{Domain, System}`. For each, `LoadComponentConfig`
   on the component dir. A component whose `component.yaml` fails to load or
   validate is reported inline with `Error` set — it must not fail the whole listing
   (precedent: `status.go` reports a bad overlay rather than erroring).
5. Secrets: for every component (shared ones are where these live, but scan all —
   a service component that declares a Secret is worth surfacing too), walk the
   multi-doc YAML files under its `base/` directory and under **every directory
   that exists** in its `overlays/` (not the component's declared environment
   list: an overlay directory nobody declared is exactly the half-onboarded state
   this command exists to surface), plus the `kustomization.yaml` in each of
   those directories.

   Write NEW scanning code for this — do not try to reuse
   `internal/kustomize/manifest.go`. Its splitter (`splitDocuments`) is
   unexported, its `Identities` returns pre-formatted display strings rather
   than structured data, and it hard-fails on the first malformed document,
   which contradicts the scanner contract. Instead:

   - Split each file into YAML documents (split on `^---` lines; the format is
     simple enough that a small local splitter is fine, or export a helper from
     internal/kustomize if that reads better — decide in the PR).
   - Decode each document into a minimal struct
     `{apiVersion, kind, metadata{name}}` — no full schema, unknown fields
     ignored. Record a hit for `kind: Secret`, `kind: ExternalSecret` (any
     apiVersion), `kind: SealedSecret`.
   - A malformed document is skipped with a stderr diagnostic naming the file,
     not fatal. This is a scanner, not a validator.
   - Parse `kustomization.yaml` files separately (they are kustomize directives,
     not resource documents): decode into a struct with a `secretGenerator`
     slice and record each entry's `name`, with `File` left empty.

   Deduplicate: the same secret resource appearing in both `base/` and an
   overlay patch should be reported once per location, with `File` saying where
   it was seen — do not try to resolve patch semantics; this reports files, not
   rendered state.

Plain rendering: `text/tabwriter` like `reportStatus`. Header block (repo, registry,
argocd, platform), then environments as a table in promotion order with sync policy
and promotes-from, then systems with their components indented (name, kind,
environments), then a secrets section listing store + each `SecretRef` grouped by
owning component. Empty secrets section renders as `secrets  none declared` — an
explicit empty state, lowercase, not an absent section.

### 2. `cmd/intropy/deploy_show.go` (new) + `cmd/intropy/deploy_show_test.go` (new)

Mirror `deploy_status.go`:

- `Use: "show"`, `Args: cobra.NoArgs`.
- `Short: "Show what the GitOps repository declares"` (one line, imperative, no
  period — finalize wording).
- `Long:` cover: reads `deploy.yaml` + every `component.yaml` from a refreshed
  checkout; prints registry, ArgoCD, platform, environments in promotion order,
  systems/components, and secret-bearing resources; writes nothing; never calls
  ArgoCD; exit 0 even when something in the tree is broken. State plainly that
  secrets are reported by name/kind/owning component and never decrypted.
- Flags: `--gitops-repo` (`flagUsageGitopsRepo`), `-o/--output`
  (`flagUsageOutput`, plain|json). No `--env`, no `--domain`/`--system`, no
  `--argocd-server` (never contacted).
- Register on `deployCmd`; add a `show` line to the `deployCmd.Long` subcommand list
  in `cmd/intropy/deploy.go` keeping the aligned two-column layout. Also update the
  doc comment above `deployCmd` (deploy.go:9-12): it lists the subcommand names
  that can never shadow a component ("diff, init, pin, promote, status or sync")
  and goes stale the moment `show` exists without being named there.
- `RunE`: `validateOutputFlag`, `signal.NotifyContext`, call `deploy.Show` with
  `UserAgent: "intropy-cli/" + version`, stdout/stderr wired as in status.

### 3. Dashboard backend

`internal/dashboard/gitops.go` (new) + `gitops_test.go`:

- A `gitopsProvider func(ctx context.Context) gitopsState` following the
  `deployProvider` precedent exactly: the default implementation runs
  `deploy.Show` with `OutputFormat: deploy.OutputJSON` into a `bytes.Buffer`,
  decodes stdout, captures stderr as `Diagnostics`, sets `Error` verbatim on
  failure (same reasoning as the comment on `statusCommandProvider` — the command
  owns the refusals; never re-derive).
- `gitopsState` mirrors `deployState`: `Show *deploy.ShowResult`, `Error string`,
  `Diagnostics []string`, `ReadAt time.Time`. Reuse `diagnosticLines` from
  `deploy.go`.

`internal/dashboard/handlers.go`:

- Add `gitops gitopsProvider` to `providers` (tests replace it).
- Cache on `apiServer` with explicit fields, following the `cachedTopologies`
  three-field pattern (handlers.go:49-52): `gitopsMu sync.Mutex`,
  `gitopsLoaded bool`, `gitopsState gitopsState`. NOT keyed by path — this is
  one repo-level read per dashboard, unlike per-integration deploy state.
  Serialize provider calls under the mutex and apply the same one-minute
  timeout as `deployStateTimeout`, for the same reason: the underlying command
  holds the exclusive GitOps checkout lock for its whole duration (see the
  comment at handlers.go:33-39).
- First-load latency is a real cost here (a checkout refresh blocks the first
  GET), so apply the warm-up pattern too: a `warmGitops()` mirroring
  `warmTopologies` (handlers.go:294-323) with a `gitopsWarming bool` guard,
  triggered from whichever early endpoint the frontend hits first (catalog or
  integrations), so the GitOps view opens warm instead of blocking.
- Routes: `GET /api/gitops` serves the cached state; `POST /api/gitops/refresh`
  recomputes. Add a "deliberately not X" comment per house style: the deploy
  endpoints avoid a `/refresh` segment because their `{path...}` wildcard must
  be last (handlers.go:115-117); these routes have no wildcard, so the segment
  form is available and reads better — say so in the comment.

`internal/dashboard/server.go`: wire `gitops: showCommandProvider(opts.Version)`
into `providers` in `Serve`. `showCommandProvider` derives its user agent the
same way `statusCommandProvider` does (dashboard/deploy.go:67):
`userAgent := "intropy-cli/" + version`. Update the package doc comment — it
currently says deploy status is "the only part of this package that touches the
network"; that is no longer true.

### 4. Dashboard frontend (`web/src/`)

- `web/src/api.ts`: add TypeScript types mirroring `deploy.ShowResult` field-for-field
  (the file's header comment already says types mirror the Go JSON contract — keep
  that true) and a `fetchGitops()` / `refreshGitops()` pair matching how existing
  endpoints are called.
- New component, e.g. `web/src/components/GitopsView.tsx`: render the environments
  pipeline (promotion order, sync policy badges, promotes-from arrows), the platform
  line, and the systems/components tree with kind badges; a secrets panel listing
  `SecretRef`s grouped by owning component (the flat `SecretRef.Component`
  coordinate is there for this list), styled distinctly when `Error` is set.
- Wire into `App.tsx` navigation. Note two specifics: the view switch is a
  `View = 'catalog' | 'flow'` union type (App.tsx:11-15) that needs a third
  member — trivial but easy to miss; and the closest *structural* sibling for a
  nav-level view fed by a single repo-wide endpoint is `FlowView.tsx` (fed by
  `/api/flow`), not `CatalogEnvironments.tsx` (which is a per-integration
  ladder fed by `/api/deploy/{path}`). Match its fetch/loading/error shape.
- Run the web build (`make web` / `npm run build` in `web/`). `web/dist` is a
  gitignored build artifact with a placeholder mechanism (web/embed.go:1-15) —
  do NOT commit it; the build is a local/CI verification step only.

## Testing

- `internal/deploy/show_test.go`: build fixture GitOps trees (see
  `internal/gitops/gitopstest` and existing `status_test.go` fixtures). Cover:
  promotion-order rendering; a component with a broken `component.yaml` reported
  inline while others still list; a shared component with a `Secret`, an
  `ExternalSecret`, and a `secretGenerator` all detected; an overlay directory
  nobody declared still scanned for secrets; a malformed YAML document skipped
  with a diagnostic; a repo with no secret resources rendering the explicit
  empty state; JSON round-trip of `ShowResult`.
- **Build tag:** tests that go through `openSession` with real fixture repos
  carry `//go:build integration` (13 of 21 test files in internal/deploy,
  including status_test.go:1) and run in a separate CI job with
  `-tags integration` (.github/workflows/test.yaml:48-88). Put the
  session-level tests in that suite; keep pure unit tests (the YAML scanner,
  grouping, rendering) untagged so they run in the default `go test ./...`.
- `cmd/intropy/deploy_show_test.go`: help text shape (`helptext_test.go`
  enforces the shared flag constants and the documented-verb list — `show`
  must pass `TestHelpTextCommandVerbsAreDocumented`), flag validation, wiring.
- `internal/dashboard/gitops_test.go`: provider success, command failure →
  `Error` verbatim, caching (second GET does not re-run the provider), POST
  refresh recomputes, warm-up fires once.
- Web: `npm run build` in `web/` must succeed; no new lint errors.
- Full gate before PR: `go test ./...`, `go test -tags integration ./internal/deploy/...`,
  and `go build ./...`. Note that `go test ./...` alone does NOT run the
  integration-tagged tests — a green default run proves nothing about them.

## Acceptance criteria

- `intropy deploy show` against a fixture repo prints environments in promotion
  order, the platform/secretStore line, the grouped systems tree, and the secrets
  section; writes nothing; needs no ArgoCD. It fails only on the fatal cases
  listed under house-style constraints (unresolved repo, checkout/lock failure,
  missing/invalid deploy.yaml, bad --output).
- `intropy deploy show -o json` emits the `ShowResult` contract above.
- `intropy deploy show --help` passes the repo's help-text conventions.
- `GET /api/gitops` returns the decoded `ShowResult` with `readAt`; `POST
  /api/gitops/refresh` re-runs the command.
- The dashboard renders an environments/systems/secrets view fed only by
  `/api/gitops`.
- `go test ./...` green; `go test -tags integration ./internal/deploy/...` green;
  web build green (artifact not committed).

## Notes for the implementing agent

- Read `internal/deploy/status.go` and `internal/dashboard/deploy.go` first. Every
  pattern this feature needs — session handling, JSON-via-buffer command reuse,
  verbatim error pass-through, tabwriter rendering — already exists there; copy the
  shape, don't invent a new one. The one exception is the YAML secret scanner,
  which is new code by design (see assembly step 5 for why manifest.go cannot be
  reused).
- Do not add `kustomize build` anywhere in this feature. If you find yourself
  wanting rendered manifests, stop — that decision is recorded above with its
  reasons.
- This plan was reviewed once against the codebase; the findings that were
  folded in include the scanner-is-new-code decision, the integration build tag,
  the revision source (`repo.Git.HEAD`), and the warm-up caching shape. If you
  deviate from any of those, say why in the PR description.
- Git workflow: branch, specific `git add` paths (never `git add .`), descriptive
  commit, PR against main. Follow the repo's commit conventions. The plan file
  itself (docs/plans/deploy-show-command.md) is not part of the feature PR —
  leave it out unless the repo convention says plans get committed.
