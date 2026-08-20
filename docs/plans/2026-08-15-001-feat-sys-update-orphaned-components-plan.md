---
title: feat: Add sys update to fold orphaned components into an existing system host
type: feat
status: active
date: 2026-08-15
deepened: 2026-08-15
---

# feat: Add sys update to fold orphaned components into an existing system host

## Overview

`sys create` assembles a system host from the workspace's scaffold records, but it only runs once: any integration scaffolded *after* the host exists is invisible to it, and re-running `sys create` would overwrite the whole directory (`--force`). This plan adds `intropy sys update`, which scans the workspace for **orphaned components** — scaffold records with a block kind that the host's own scaffold record does not mention — and re-renders the host's declaration files to include them, without touching hand-edited or otherwise divergent files and without ever removing a component the host already declares.

---

## Problem Frame

A user's workflow today:

1. Scaffold some integrations (`intropy int create …`) → each leaves a `.intropy/scaffold.json` with a block kind.
2. Run `intropy sys create -n OrderFlow` → renders a host declaring those components.
3. Scaffold another integration later.
4. **Dead end.** The new component is "orphaned": it exists on disk with a valid scaffold record, but the host declares nothing about it. The only recourse is hand-editing `Topics.cs`/`Ports.cs`/the system definition, or blowing the host away and re-creating it.

`sys update` closes that gap. It must be **idempotent** (a second run is a no-op) and **non-destructive** (accept existing identical files, never replace or delete without `--force`, never drop a declared component).

The design was settled in conversation before this plan was written, and hardened by a code review pass (see the review log at the end):

- **Authority:** the host's own scaffold record (`.intropy/scaffold.json` in the host dir), not the topology. The record stores the full payload (`components`, `topics`, `ports`) under `values` — written by `template.WriteScaffold` at create time — so the CLI can compute orphans from files alone, with no `dotnet build`.
- **Baseline merge, not re-derivation:** the update's payload starts from the host record's stored component entries and *appends* orphans. It never rebuilds the declared set from the current workspace scan, so a component whose scaffold has disappeared stays declared (review finding 1).
- **Topology as advisory check only:** `topology.RunGraph` may run as a best-effort post-update validation warning, never as a gate. (It also cannot rebuild the payload — it drops the scaffold `values` the wiring needs — so it was never a candidate for authority.)
- **Removed components are never deleted.** A component declared in the host record whose scaffold has disappeared (a "reverse orphan") is preserved verbatim; when its scaffold is gone the update emits a stderr note so the user knows the workspace no longer backs it, but the declaration stays.

---

## Requirements Trace

- R1. `sys update` locates exactly one system host in the workspace and errors when there are zero or several.
- R2. It computes orphaned components as the set difference between the workspace's assemblable scaffold records and the components named in the host record's `values.components`, with no `dotnet build` on the happy path.
- R3. It re-renders the host template with a payload built as **host-record entries ∪ orphaned entries** in an **update mode**: identical file → skip; missing file → write; differing file → error naming it, unless `--force`.
- R4. It is idempotent: after a successful update it rewrites the host record with the merged values, so a second run reports `no orphaned components found` and exits 0.
- R5. It offers `--dry-run` to print the plan (components to add, files that would change) without writing, and `--output json` for a machine-readable summary.
- R6. After a successful update it runs a best-effort topology check that warns (stderr, never an error) when the host's `graph` output disagrees with the model just written; a build failure or timeout downgrades to a note.
- R7. Help text follows the house style in `AGENTS.md` and passes `cmd/intropy/helptext_test.go` (Short is a fragment, Long ≤ 220 words, Use matches the pattern).
- R8. The update renders with the **template pinned in the host record** (`template`/`owner`/`repo`/`version`) by default, not the latest official release; the `--template-*` flags are explicit overrides (review finding 3).
- R9. A declared component whose scaffold is missing from the workspace is **preserved** in the payload and the record, with a stderr note; it is never silently dropped (review finding 1).
- R10. The workspace scan root is unambiguous: `sys update` requires running from the system/workspace root (the host's parent), and errors clearly when invoked from inside the host directory (review finding 5).

---

## Scope Boundaries

- **No deletion, ever.** Reverse orphans are preserved with a note (R9); removing a declared component is a separate, future command's job.
- **No reconciliation of hand edits by default.** A differing declaration file is an error, not a silent overwrite; `--force` is the explicit escape hatch.
- **No template changes.** The system-host template is unchanged; the merge logic lives entirely in the CLI (`internal/system`). Template-side merging was considered and rejected — it would push workspace knowledge into the template library, inverting the split that package's doc comment states ("The template library owns all generated content; this package owns workspace knowledge only").
- **No topology-driven rebuild.** The update never derives the model from `RunGraph`; that probe is a post-update validation only (R6).
- **Drift of existing components is surfaced, not silently applied.** When an already-declared component's scaffold values changed since the host was rendered, the regenerated file differs; that lands in the standard conflict path (error without `--force`), so drift is never silently folded in (review finding 6).

---

## Context & Research

### Relevant Code and Patterns

- `internal/system/create.go` — the command's engine. `Create` scans scaffolds (`template.ListScaffolds`), calls `Assemble`, builds the payload (`buildPayload`), renders via `template.Create` with `OnManifest: requireFactsPayload`, then reads the record back to fill `ProjectName`/`SystemClass` and prints the summary to stderr. `sys update` mirrors the shape but splits prepare from run (below).
- `internal/system/assemble.go` — `Assemble(entries, warnf)` classifies records: `RoleSystemHost` → skip with a warning, `RoleSharedLibrary` → the contracts project, block kinds via `blockParsers`. Reuse it to classify *candidate* scaffolds; the declared baseline comes from the host record, not from `Assemble`'s output (see Key Technical Decisions).
- `internal/system/payload.go` — `buildPayload(model, outputDir, kebab)` produces the facts-only payload from a `Model`. For update, the model's component entries are the merged set (host-record entries + orphans), so `buildPayload` runs unchanged on that merged model.
- `internal/template/prepare.go` — **`PrepareCreate` / `RunCreate` (the key seam).** `PrepareCreate` fetches the library, resolves values, and returns a `PreparedCreate` (manifest, values, skeleton root, owner/repo/version, cleanup) with *nothing written*. `RunCreate` renders, processes dependencies, and writes the scaffold record. The dashboard already uses this split to control the render itself; `sys update` uses it the same way — `PrepareCreate`, then a new update-mode render, then its own record write — instead of the monolithic `template.Create`, which writes the record mid-flow and would break the record-last invariant (review finding 2).
- `internal/template/scaffold_list.go` — `ListScaffolds(root)` returns all records with warnings; `ListSystemHosts(root)` returns only `RoleSystemHost` records. The host locator for R1 — already exists and is tested in `internal/template/hosts_test.go`.
- `internal/template/scaffold.go` — `Scaffold` struct, `LoadScaffold(path)`, `WriteScaffold(projectRoot, s)`. The host record's `Values` map carries `components`/`topics`/`ports` from create time — the baseline for the orphan diff (R2), the merge source for the payload (R3), the template pin (R8), and the thing rewritten in R4.
- `internal/template/render.go` — `RenderFiltered(srcDir, destDir, values, rules)` walks the skeleton and writes each file through `renderTemplate`/`copyFile`, both of which end in `writeAtomically(dst, mode, func(io.Writer))`. Per-file write happens inline against the destination — there is no bytes-returning path today, which is why dry-run and per-file outcomes need a small refactor (review finding 4; see U2).
- `internal/topology/graph.go` — `RunGraph(ctx, hostDir)` builds and runs the host's `graph` verb, decoding a `topology.Topology`. Used by the dashboard today; reused here only for the post-update validation (R6).
- `cmd/intropy/sys_create.go` — the cobra plumbing pattern: flags struct + package-level values, `RunE` doing output-flag validation and calling into `internal/system`, `init` registering flags and adding to `sysCmd`.
- `cmd/intropy/helptext_test.go` — walks the whole command tree and enforces the AGENTS.md writing rules. The new command must satisfy it.

### Institutional Learnings

- The non-destructive-update precedent is stated in `AGENTS.md` for manifest creation: "accepts existing identical files but never replaces or deletes a file." `sys update` adopts the same contract, relaxed only by an explicit `--force`.
- The `PrepareCreate`/`RunCreate` split exists precisely because a prior caller (the dashboard) needed render control the monolithic `Create` could not give. Reaching for it here is following an established pattern, not inventing one.
- House voice for runtime output (AGENTS.md): progress and completion are lowercase, present tense, no trailing period, on stderr; results go to stdout. E.g. `updating order-flow: adding components a, b` then `no orphaned components found` for the empty state.

---

## Key Technical Decisions

- **Host record is the baseline, not the topology.** The record's `values.components` is exactly what the host was rendered from, is always readable (no build), and is comparable like-for-like with sibling scaffold records. The topology was rejected as authority because it is a projection that drops the scaffold `values` the payload needs and because requiring a buildable host would block updates on a mid-refactor workspace.
- **Orphan = assemblable scaffold whose `appId` is absent from the host record's `values.components`.** "Assemblable" means: has a block kind in the `blockParsers` registry and is not the host or the shared library. `Assemble` classifies the candidates; the *declared* set comes from the record.
- **Payload = host-record entries ∪ orphan entries, never a re-scan of the declared set.** The merged `Model` passed to `buildPayload` takes its existing components/topics/ports from the host record's stored values and appends the orphans' entries from `Assemble`. This is what makes the never-delete rule structural (R9): a vanished scaffold cannot remove its component, because the component's entry never came from the scan (review finding 1). As a side effect, unchanged declared components re-render byte-identical — which is exactly the "skip" path in update mode.
- **Prepare/render/record as three explicit steps, not one `template.Create` call.** `template.Create` renders and writes the record in one flow; `sys update` needs per-file outcomes, dry-run, and the record written *last*. `PrepareCreate` gives the fetch-and-resolve half; the update owns the render (update mode, U2) and the record write (R4) itself, skipping `RunCreate` (review finding 2).
- **The template pin comes from the host record.** `Owner`/`Repo`/`Version`/`Template` default to the record's stored values so an update never silently upgrades the host's template; the `--template-*` flags override explicitly (R8, review finding 3).
- **Update mode lives at the per-file write in the render layer**, and dry-run is a first-class outcome of the same refactor: rendering produces bytes and a per-file outcome (`created`/`unchanged`/`updated`/`conflict`) *before* anything touches disk, so `--dry-run` is "compute outcomes, don't persist" rather than a separate code path (review finding 4).
- **The host record is rewritten only after every file write succeeds** (R4 idempotence). Writing it last means a mid-update failure leaves the baseline honest — a rerun recomputes the same orphan set.
- **The scan root is the workspace root, enforced.** `sys update` derives the scan root from the located host's parent (the system root) rather than trusting `.`, so running it from inside the host directory errors clearly instead of silently missing sibling components (R10, review finding 5).
- **The topology check runs after the update, not before.** Run pre-update it would warn on every legitimate orphan (the graph necessarily lacks the components being added); run post-update it validates that what was written matches what the host now declares (review finding 7).
- **Verb is `update`** per the AGENTS.md verb table ("reconciles something installed against the ref its source now pins"). Here the "ref" is the workspace's set of scaffold records.

---

## Open Questions

### Resolved During Planning

- Should `sys update` overwrite a differing file by default? → **No** (user decision). Differing file is an error naming the file; `--force` opts into overwrite.
- Authority: topology or scaffolds? → **Scaffolds**, topology advisory only (user decision, design above).
- Removed components deleted? → **No** (user decision, hardened by review): preserved with a note, never dropped (R9).
- Merge strategy for the payload → **host-record entries ∪ orphans**, not a full re-scan (review finding 1).
- Renderer → **`PrepareCreate` + update-owned render/record**, not monolithic `template.Create` (review finding 2).
- Template version → **pinned from the host record by default** (review finding 3).
- Topology check timing → **post-update validation**, not pre-update diff (review finding 7).

### Deferred to Implementation

- Exact signature of the update-mode render option and the per-file outcome type (an `Outcome` enum + `[]FileOutcome` return, or a callback). The plan fixes the *behavior contract* and the bytes-before-disk requirement, not the Go shape.
- How the scan-root enforcement reads the host location (likely: `ListSystemHosts` from `.`, then scan siblings from the host's parent dir; or refuse when `.` is itself the host). The behavior (R10) is fixed; the mechanism is not.
- Whether existing-component drift beyond a file conflict deserves its own stderr note in addition to the conflict error (review finding 6 names the behavior; whether to surface it earlier is an implementation-time UX call).
- The advisory check's comparison granularity (component-name set vs. deeper wiring). Component names are the minimum; deeper comparison is optional.

---

## Implementation Units

- [ ] U1. **Locate the host, load the baseline, and compute the orphan diff in `internal/system`**

**Goal:** A pure, testable function that, given a workspace root, finds the single system host, loads its stored component entries as the baseline, and returns the orphaned components plus the *merged* model (baseline ∪ orphans).

**Requirements:** R1, R2, R9, R10

**Dependencies:** None

**Files:**
- Create: `internal/system/update.go` (the `Update` entry point grows here across units; this unit adds host location, baseline load, and the diff/merge)
- Test: `internal/system/update_test.go`

**Approach:**
- Enforce the scan root (R10): locate hosts with `template.ListSystemHosts` from the invocation dir; zero → error telling the user to run `sys create` (or, when the invocation dir *is* a host dir, a clear "run from the workspace root" error); more than one → error naming them. Derive the sibling scan root from the located host's parent so orphans outside the host dir are seen.
- `template.LoadScaffold` the host's record. Extract from `Values`: the `components` entries (baseline, preserved verbatim for the merge), and the template pin (`Template`, `Owner`, `Repo`, `Version`) for U3. Values arrive as `[]any` of `map[string]any` after JSON round-trip — extract `appId` per element defensively; a malformed record is a clear error naming the record path.
- Run `template.ListScaffolds(scanRoot)` + `Assemble(entries, warnf)` to classify candidates. `Assemble` already skips the host record and the shared library.
- Orphans = assembled components whose `AppID` is absent from the baseline's `appId` set.
- Build the merged model: declared components/topics/ports from the host record's stored entries, **plus** orphan entries from `Assemble`. A baseline `appId` with no matching scaffold (reverse orphan) is kept as-is and flagged for a stderr note (R9) — never dropped.

**Patterns to follow:**
- `Assemble`'s error messages (name the record path, tell the user which project to fix).
- `stringValue` in `assemble.go` for defensive value extraction.

**Test scenarios:**
- Happy path: host record declaring a, b + scaffolds for a, b, c → orphan set [c]; merged model has a, b (baseline entries) and c (orphan entry).
- Happy path: baseline covers every scaffold → empty orphan set (the R4 no-op case).
- Happy path (R9): baseline declares a, b but b's scaffold is gone → orphan set as computed from present scaffolds, **b still in the merged model**, flagged for the note. Assert b is not dropped.
- Error path: no host → error mentions `sys create`.
- Error path: invocation dir is the host dir itself → error says to run from the workspace root (R10).
- Error path: two hosts → error names both.
- Error path: malformed `values.components` → error names the record path.
- Edge case: scaffold with no block kind → not in the orphan set (skipped by `Assemble` with a warning).

**Verification:**
- `go test ./internal/system/ -run Update` passes with the scenarios above; the reverse-orphan preservation case is the load-bearing one.

---

- [ ] U2. **Add a bytes-producing, outcome-reporting update render to `internal/template`**

**Goal:** A render path that, for each skeleton file, computes the rendered bytes and compares them to the destination *before* writing, returning per-file outcomes (`created`/`unchanged`/`updated`/`conflict`) — and only persisting when asked. This one mechanism serves both the real update and `--dry-run`.

**Requirements:** R3, R5 (dry-run half)

**Dependencies:** None (independent of U1; can land in either order)

**Files:**
- Modify: `internal/template/render.go`
- Test: `internal/template/render_test.go`

**Approach:**
- Today's render writes inline: `renderTemplate`/`copyFile` go straight to `writeAtomically(dst, …)`. Refactor so rendering/copying first produces bytes (or a reader), then a separate step decides: destination absent → `created`; bytes equal → `unchanged`; bytes differ → `conflict` (no force) or `updated` (force). Persist only when not in dry-run mode.
- Return `[]FileOutcome` (path + outcome) for the caller's summary and JSON. Keep `RenderFiltered`'s existing signature/behavior for `sys create` — the new path is additive (a sibling function or an options struct), not a breaking change.
- Do not route update mode through `ensureOutputDir`'s non-empty refusal — the host dir is by definition non-empty.

**Patterns to follow:**
- The dependency-processing precedent in `internal/template/dependencies.go`: reports per-item outcomes (`created`/`exists`) without breaking the create flow. The outcome-list shape mirrors that.

**Test scenarios:**
- Happy path: dest identical → outcome `unchanged`, content/mtime untouched.
- Happy path: dest absent → outcome `created`, file written.
- Error path (no force): dest differs → outcome `conflict`, original content preserved.
- Happy path (force): dest differs → outcome `updated`, new content on disk.
- Happy path (dry-run): outcomes computed, **no file created or modified** (assert the tree is untouched).
- Regression: default render still overwrites unconditionally and still refuses a non-empty dir without force (existing tests pass unchanged).

**Verification:**
- `go test ./internal/template/ -run Render` passes, including pre-existing render tests; the dry-run-leaves-no-trace assertion is explicit.

---

- [ ] U3. **Assemble the `Update` flow in `internal/system`**

**Goal:** `system.Update(ctx, UpdateOptions)`: locate host and merge (U1), prepare the template at the record's pin (R8), render in update mode (U2), rewrite the record last (R4), print the summary, honor `--dry-run`/`--output json`, and run the post-update topology validation (R6).

**Requirements:** R2–R6, R8, R9

**Dependencies:** U1, U2

**Files:**
- Modify: `internal/system/update.go` (add `UpdateOptions`, `UpdateResult`, `Update`)
- Test: `internal/system/update_test.go`

**Approach:**
- Short-circuit before any render when the orphan set is empty: print `no orphaned components found` to stderr and exit 0 (the idempotent no-op, R4).
- Derive the system name/kebab from the host record's values (`xstrings.ToKebabCase`, as `Create` does), and build the payload with `buildPayload(mergedModel, hostDir, kebab)` unchanged.
- Fetch via `template.PrepareCreate` with `Owner`/`Repo`/`Version`/`Template` defaulted from the host record (R8) and `--template-*` flag overrides applied; keep `NoInput: true` and the `requireFactsPayload` manifest gate. Do **not** call `template.Create`/`RunCreate` (they write the record mid-flow); the update owns render and record ordering (review finding 2).
- Render through U2's update render into the host dir. Conflicts with no force → error listing the differing files, telling the user to reconcile by hand or re-run with `--force`; on any conflict the record is **not** rewritten, even if other files landed.
- On full success, rewrite the host record via `template.WriteScaffold` with the merged values — **last**, after every file landed (R4).
- Reverse-orphan note (R9): for each baseline component whose scaffold is absent, one stderr line in house voice, e.g. `note: component b declared but no scaffold found — kept as declared`.
- `--dry-run`: compute and print the plan (orphan components, U2's per-file outcomes) without writing files or the record; skip the topology validation.
- `--output json`: stdout carries an `UpdateResult` (added components, per-file outcomes, host dir); progress stays on stderr in house voice: `updating order-flow: adding components billing-sync`.
- Post-update validation (R6): after a successful, non-dry-run update, attempt `topology.RunGraph(ctx, hostDir)` and compare its component names to the merged model's. Any disagreement or any error (build failure, timeout, decode failure) → a single stderr warning, never a non-zero exit. Isolate it in its own function so the happy path never depends on `dotnet`.

**Execution note:** Implement the record-rewrite ordering (files first, record last, never on conflict) test-first — it is the idempotence invariant.

**Patterns to follow:**
- `system.Create` end to end — same option struct shape, same stderr summary style (`assembled system %q…` → `updated system %q: added %d component(s)…`).
- The dashboard's use of `PrepareCreate` for caller-controlled rendering.
- `maybeWriteCreateResult` for the JSON summary plumbing.

**Test scenarios:**
- Happy path: host + one orphan → declaration files written, record rewritten; a second `Update` reports no orphans (idempotence — the R4 invariant, asserted in-test).
- Happy path (R8): the fetched template pin matches the host record's stored version, not latest (stub `PrepareCreate`'s fetch or intercept the options to assert the pin).
- Happy path: `--dry-run` reports the orphan and the files that would change; neither files nor record change on disk (uses U2's dry-run).
- Error path: differing declaration file without force → error names the file; record **not** rewritten (assert the old baseline survives).
- Happy path (force): differing file overwritten, record rewritten.
- Error path: conflict present → no partial record rewrite even when other files were written.
- Edge case (R9): baseline component with missing scaffold → merged payload still declares it; the stderr note is emitted; nothing about it is deleted.
- Edge case: orphan set empty on first run → stderr message, exit 0, no writes.
- Integration (R6): `RunGraph` failure (no `dotnet`, or stubbed) → warning, command still succeeds.
- Integration: JSON output carries added components + per-file outcomes; stderr carries the progress lines.

**Verification:**
- `go test ./internal/system/` passes; idempotence and reverse-orphan preservation are the load-bearing assertions.

---

- [ ] U4. **Wire the `sys update` cobra command**

**Goal:** `intropy sys update` on the command tree, flags plumbed, help text conforming to house style.

**Requirements:** R5, R7, R8

**Dependencies:** U3

**Files:**
- Create: `cmd/intropy/sys_update.go`
- Test: `cmd/intropy/sys_update_test.go`

**Approach:**
- Mirror `sys_create.go`: flags struct + package-level values, `RunE` validating the output flag and calling `system.Update`, `init` registering flags and `sysCmd.AddCommand(sysUpdateCmd)`.
- `Use: "update"`, `Args: cobra.NoArgs`. Flags: `--output` (reuse `flagUsageOutputJSONOnly` from `flagtext.go`), `--dry-run`, `--force`, and the template-selection flags (`--template-version`, `--template-repo`) as **overrides** to the record's pin (R8) — help text says they override the host's recorded template, not that they select it.
- `Short`: one line, imperative, no period — e.g. `Fold orphaned components into the system host`.
- `Long`: ≤ 150 words. Cover: what it scans, what it renders, that identical files are kept and differing files are an error without `--force`, that it never deletes or removes a declared component, and that it must run from the workspace root. No rationale — that lives in this plan / docs.
- Context handling identical to `sys create` (`signal.NotifyContext`).

**Patterns to follow:**
- `cmd/intropy/sys_create.go` line for line, minus `--name`/`--out-dir` (the host record supplies both).

**Test scenarios:**
- Error path: `--output yaml` → usage error (mirrors `sys create`'s validation).
- Happy path: command registered under `sys` with the expected flags.
- Regression: `go test ./cmd/intropy/ -run TestHelpText` passes for the new command (Short style, Long length, Use pattern).

**Verification:**
- `go test ./cmd/intropy/` passes, including the tree-walking help-text tests.
- `go build ./...` and a manual `intropy sys update --help` reads per house style.

---

## System-Wide Impact

- **Interaction graph:** `sys update` adds an additive render path (U2) and uses the existing `PrepareCreate` seam; `sys create`'s flow and the monolithic `template.Create`/`RunCreate` are untouched.
- **Error propagation:** conflicts and missing/multiple hosts are hard errors naming paths; the post-update topology probe is the only advisory path (stderr note, exit code unaffected).
- **State lifecycle risks:** the one partial-write hazard is the host record vs. the declaration files. The ordering invariant (record rewritten last, never on conflict) keeps a failed update retryable — pinned by U3's execution note and tests.
- **Unchanged invariants:** `sys create`'s overwrite semantics, the template library, the topology schema (`topology.intropy.io/v1`), the scaffold record schema, and the set of declared components all stay as they are. The record gains no new fields — it is rewritten with the same `values` shape `sys create` writes, now including the orphans.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Reverse-orphan silently deleted by a re-scan-based payload | Payload is host-record ∪ orphans (structural, U1); R9 test pins preservation |
| Update mode accidentally changes `sys create`'s render behavior | Additive path only; U2's regression scenario pins the default render |
| Host record rewritten despite a failed render → false baseline | Record written last, never on conflict; U3's execution note makes it test-first |
| Silent template upgrade during an update | Pin defaults to the host record's `Template`/`Owner`/`Repo`/`Version` (R8); flags are explicit overrides |
| Running from inside the host dir misses sibling orphans | Scan root derived from the host's parent; clear error when invoked in the host dir (R10) |
| Advisory check becomes a blocker (build failure, no dotnet) | Post-update only, isolated, every failure → stderr note, exit unaffected; tested in U3 |
| Dry-run leaves artifacts | U2 produces bytes/outcomes before any write; dry-run never persists — asserted in-test |

---

## Review Log

Reviewed by a second agent (pi, senior-Go-engineer persona) against the code on 2026-08-15. Verdict was "needs changes"; all findings were verified against the source and folded in:

1. **[blocking] Reverse-orphan deletion** — a re-scan-derived payload would drop a declared component whose scaffold is gone. Fixed: payload is host-record ∪ orphans (Key Technical Decisions, R9, U1).
2. **[blocking] `template.Create` writes the record mid-flow** — conflicts with record-last. Fixed: use `PrepareCreate` + update-owned render/record (R3, U3).
3. **[blocking] Template version drift** — empty pin resolves to latest. Fixed: default to the record's pin (R8, U3/U4).
4. **[should-fix] Dry-run needs a bytes-producing render** — `RenderFiltered` writes inline. Fixed: U2 refactors to bytes-then-decide, dry-run as non-persist mode.
5. **[should-fix] Ambiguous scan root** — running inside the host dir misses siblings. Fixed: R10, U1.
6. **[should-fix] Existing-component drift unhandled** — named as the standard conflict path (Scope Boundaries, review finding 6; optional earlier note deferred to implementation).
7. **[nit] Topology check timing** — pre-update it would always warn. Fixed: post-update validation only (R6, U3).

---

## Sources & References

- Pre-planning design discussion (authority decision, no-overwrite default, deletion out of scope) and the review pass above.
- Related code: `internal/system/create.go`, `internal/system/assemble.go`, `internal/system/payload.go`, `internal/template/prepare.go`, `internal/template/scaffold_list.go`, `internal/template/scaffold.go`, `internal/template/render.go`, `internal/topology/graph.go`, `cmd/intropy/sys_create.go`, `cmd/intropy/helptext_test.go`.
- House style and verb table: `AGENTS.md`.
