---
title: "refactor: Let the system-host template own the contracts decision"
type: refactor
status: active
date: 2026-02-10
---

# refactor: Let the system-host template own the contracts decision

**Repos involved:** this repo (`integrio-intropy/intropy-cli`) and the template library
(`integrio-intropy/intropy-templates`). The library is fetched per release tag at
scaffold time (`internal/template/library.go`), so the two sides must ship in
dependency order (Phase 1 then Phase 2) and the CLI release notes must name the
minimum template release.

---

## Overview

`sys create` currently fails any workspace that has component scaffolds but no
`shared-library` scaffold with "no shared contracts project found …"
(`internal/system/assemble.go:100`). That rejects a perfectly valid shape — a
host with only transactional integrations has neither topics nor contracts —
and it encodes a template-level decision ("every host references a contracts
project") in the CLI. This plan moves the contracts decision to where the
package doc says it belongs: the CLI keeps workspace knowledge, the template
library owns all generated content.

Two changes, in dependency order:

1. **CLI** — pass raw workspace facts only (names, kinds, wiring, the detected
   contracts sibling path when one exists) and stop requiring a shared library
   when the system has no topics. Drop the CLI-computed C# field identifiers
   (`pascalIdent`) and the csproj `include` path from the payload.
2. **Template library** — make `sharedContracts` optional in the system-host
   manifest, declare a conditional dependency on the shared-library template
   for topic-bearing systems, and derive field identifiers / the
   `ProjectReference` include path in the templates.

---

## Problem Frame

A user running `sys create` in a workspace holding only transactional
integrations (e.g. `domains/orders/trans`) gets:

```
error: no shared contracts project found (template role "shared-library"): Topics.cs needs the contract types
```

But a transactional-only system has `Topics == []`, so the rendered
`Topics.cs` carries no `TopicRef<T>` and needs no contracts namespace. The
guard fires because `Assemble` requires `len(shared) >= 1` unconditionally
(`internal/system/assemble.go:99-104`), and `buildPayload` unconditionally
computes a `ProjectReference` path into the payload
(`internal/system/payload.go:60-65`). Beyond the bug, the CLI currently owns
three template decisions:

- (a) whether a host must reference a contracts project,
- (b) the C# field identifiers in `Topics.cs` / `Connectors.cs`
  (`pascalIdent`, `internal/system/assemble.go:160`),
- (c) the csproj `ProjectReference` path (`contractsInclude`,
  `internal/system/payload.go:82`).

Per user decision: a contracts-less, topic-less host is valid; the fix moves
(a)–(c) into the template library (Q1 option 1, Q2 option 2).

---

## Requirements Trace

- R1. `sys create` succeeds for a transactional-only workspace with no
  `shared-library` scaffold, producing a host with no `Topics.cs` and no
  contracts `ProjectReference`.
- R2. `sys create` for a topic-bearing workspace with an existing
  `shared-library` scaffold behaves exactly as today (same rendered output,
  same dedupe/conflict validations).
- R3. `sys create` for a topic-bearing workspace with **no** contracts
  scaffold asks the template to scaffold one (conditional dependency) instead
  of erroring.
- R4. The payload contains workspace facts only: no CLI-derived `field`
  identifiers, no CLI-derived csproj `include` path. The template derives
  both.
- R5. An existing contracts scaffold that a topic-bearing system *should*
  reference but the host template would duplicate is still detected and
  reused (existing behavior: extractor/loader scaffolds already create the
  sibling, and `processDependencies` skips targets with a matching record).

---

## Scope Boundaries

- No change to the rendered C# of extractor/loader/transactional component
  templates; only `system-host` (and its conditional dependency edge) changes.
- No new template *engine* features beyond what the system-host template needs
  (see Open Questions — the `sharedContracts` payload must stay a
  `spec.parameters` entry for the conditional-dependency `when` expression to
  see it, which rules out `InjectReserved` for this key).
- No change to `int create` component scaffolding; the extractor/loader →
  shared-library dependency edge stays as is.
- No change to the `--output-json` schema beyond additive/compatible
  adjustments (Summary.SharedLibrary may become empty for transactional-only
  systems; it stays present).

### Deferred to Follow-Up Work

- If Phase 2 reveals the engine needs conditional-dependency support
  (`when` on `spec.dependencies`) or pre-skeleton dependency rendering, that
  engine work lands in this repo first as its own PR — it is part of this
  plan's Phase 2, not a follow-up, but it is sequenced after the Phase 1
  payload change so the CLI never emits a payload the current template
  release cannot consume.

---

## Context & Research

### Relevant Code and Patterns

- `internal/system/assemble.go` — the unconditional shared-library guard
  (lines 99-104), per-topic/per-connector `Field` derivation via
  `pascalIdent`, and the cross-component validations (topic contract
  conflicts, connector uniqueness, field collisions) that move or stay per
  this plan.
- `internal/system/payload.go` — payload shape; `contractsInclude` computes
  the csproj path; the `sharedContracts` and per-item `field` keys are the
  CLI-owned rendering knowledge being removed.
- `internal/system/blocks.go` — block-kind parsers; transactional blocks
  already carry no topic, so the model shape supports contracts-less systems
  today.
- `internal/template/dependencies.go` — `processDependencies` renders
  declared siblings after the skeleton, skipping targets whose scaffold
  record matches. The "skip if already scaffolded" behavior is what makes
  the host-declared contracts dependency reuse an existing `Contracts/`
  instead of duplicating it.
- `internal/template/manifest.go:67` — `FileRule` (`spec.files`) is the
  existing mechanism for conditionally including skeleton files; the
  system-host template uses it to drop `Topics.cs` when there are no topics.
- `internal/template/reserved.go` — `InjectReserved` exists for structured
  payload data, but its contract (inject after schema validation) means
  reserved keys are invisible to `spec.dependencies` `when` expressions;
  `sharedContracts` therefore stays a schema-declared parameter.
- Test fixture `internal/system/create_test.go:22-145` — trimmed copy of the
  system-host manifest and declaration templates; the canonical place the
  CLI-side payload contract is pinned.

### Institutional Learnings

- None found in `docs/solutions/` relevant to this seam.

---

## Key Technical Decisions

- **The shared-library requirement becomes topic-conditional, in the
  template.** Rationale: whether a host needs contracts is a function of the
  rendered artifacts (does `Topics.cs` reference contract types?), which is
  template knowledge. The CLI retains only workspace facts: how many
  `shared-library` scaffolds exist (0, 1, >1).
- **More than one shared-library scaffold remains a CLI error regardless of
  topics.** Rationale: that is workspace ambiguity the template cannot
  resolve — the CLI owns workspace knowledge, and two contract projects is a
  genuinely ambiguous workspace state.
- **Field identifier derivation moves to the template.** Rationale: the
  field names exist only inside generated C# files; with sprig available in
  skeleton rendering (and in `spec.files`/`when` expressions), the template
  can apply the same title-case-and-join transformation the CLI's
  `pascalIdent` performs. The CLI's per-field collision checks (two topics
  mapping to one field) move to the template as authoring guidance — see
  Deferred to Implementation for the collision-detection trade-off.
- **The csproj `include` path moves to the template.** Rationale: the
  reference only makes sense relative to where the template puts the host
  csproj; the CLI passes the contracts sibling's workspace-relative path and
  the template computes the relative `ProjectReference`.
- **Phase order is CLI-then-template.** Rationale: the CLI is fetched
  against a template release tag; an old CLI against a new template must
  keep working, and a new CLI against an old template must fail with a clear
  "template release too old" error rather than rendering a broken host. The
  additive payload change (facts-only keys added alongside, then replacing,
  the derived keys) keeps old-template compatibility during the transition —
  exact compatibility window is a Deferred-to-Implementation item.

---

## Open Questions

### Resolved During Planning

- *Is a contracts-less, topic-less host valid?* — Yes (user-confirmed); the
  transactional block shape (`parseTransactional`) already carries no topic.
- *Should `sharedContracts` become a reserved key via `InjectReserved`?* —
  No: reserved keys are injected after schema validation, so a conditional
  dependency `when` expression (evaluated against the resolved values) could
  not see it. It stays a schema-declared, now-optional parameter.
- *Does `xstrings` usage constrain the template-side derivation?* — The CLI
  uses sprig-compatible `xstrings` for kebab-case parity
  (`internal/system/create.go:96`); `pascalIdent` is custom but expressible
  with sprig. No new dependency is introduced by removing CLI-side use.

### Deferred to Implementation

> Implementation notes (2026-02-10): sprig pipeline `regexReplaceAll "[-._]" <name> " " | title | replace " " ""` reproduces pascalIdent exactly (verified against the old unit cases and rendered output). Field-collision detection moved into the templates via sprig `fail` with a message naming both offenders. The csproj include stayed CLI-computed (`sharedContracts = {name, include}`) because the renderer cannot express path arithmetic; the field derivations and joins moved as planned. `Create` also gained spec.files evaluation (previously deploy-engine only) — a dependency render with a false `when` leaves the parent's directory already created, matching the pre-existing failure semantics.

- *Exact sprig pipeline reproducing `pascalIdent`* ("order-events" →
  "OrderEvents"): depends on the sprig version vendored into the template
  renderer; to be written and verified in Phase 2 against the real library.
- *Field-collision detection after the move*: today `Assemble` errors when
  two topics (or two connectors) map to one C# field. Options: (a) the
  system-host template detects and fails render with a clear message, or
  (b) the CLI keeps a *rendering-agnostic* uniqueness check on the raw
  names. Decide when writing the template; (b) is the fallback if (a)
  produces an unreadable template error.
- *CLI/template compatibility window*: whether the new payload keeps the old
  derived keys (`field`, `sharedContracts.include`) for one release as
  deprecated aliases, or hard-breaks with a minimum-template-version check.
  Decide with the release owner; the plan assumes a minimum-version check in
  the CLI error message at minimum.
- *Whether `spec.dependencies` gains a `when` condition and whether
  dependencies render before the skeleton*: the system-host template needs
  both (contracts project must exist for the csproj reference to be valid;
  dependency must be conditional on topics). Both are template-library +
  engine changes scoped to Phase 2; if `when` on dependencies is rejected in
  review, the fallback is the CLI emitting an empty `topics` list and the
  template *always* declaring the dependency but rendering a no-op when
  topic-less — documented here so the reviewer sees the trade-off.

---

## Implementation Units

### Phase 1 — CLI: facts-only payload and topic-conditional requirement

- [x] U1. **Relax the shared-library guard and make the model contracts-optional**

**Goal:** `Assemble` accepts a workspace with zero shared-library scaffolds
when the assembled model has no topics; a topic-bearing system with zero
shared scaffolds no longer errors in `Assemble` (the host template's
dependency will supply it). More than one shared scaffold stays an error
always.

**Requirements:** R1, R3

**Dependencies:** None

**Files:**
- Modify: `internal/system/assemble.go`
- Test: `internal/system/assemble_test.go`

**Approach:**
- Replace the `len(shared) == 0 → error` / `len(shared) > 1 → error` switch:
  `> 1` stays an unconditional error; `== 0` is accepted and represented as
  a zero-value `SharedLibrary` (or a `*SharedLibrary` — decide in
  implementation; pointer is the cleaner "absent" signal but touches every
  consumer, zero-value keeps the diff small).
- Remove the per-topic/per-connector `Field` assignment from `Assemble`
  (moves to the template per R4) — topics/connectors carry names only. The
  two "both map to field" collision errors are removed here and re-homed per
  the deferred collision-detection decision.
- Update the `Topics.cs needs the contract types` error text: when topics
  exist and no shared scaffold exists, `Assemble` no longer errors at all —
  that message disappears.

**Patterns to follow:**
- Existing table-driven `TestAssembleErrors` in
  `internal/system/assemble_test.go` for the guard cases.

**Test scenarios:**
- Happy path: transactional-only entries, no shared scaffold → model
  assembles, `Topics` empty, shared absent, no error.
- Happy path: extractor + loader + one shared scaffold → unchanged model
  (regression guard for R2).
- Edge case: topic-bearing entries, no shared scaffold → assembles without
  error (dependency supplies contracts at render time).
- Error path: two shared-library scaffolds, transactional-only system →
  still errors with the "references exactly one" message.
- Error path: two shared-library scaffolds, topic-bearing system → same
  error.

**Verification:**
- `go test ./internal/system/` passes; the old
  `wantErr: "no shared contracts project found"` case is gone or repurposed.

---

- [x] U2. **Facts-only payload**

**Goal:** `buildPayload` emits workspace facts only: topics as
`{pubsub, name, contract}`, connectors as `{name}`, components with wiring
*names* (`topic`, `connector`, `from`, `to`) instead of resolved field
identifiers, and `sharedContracts` (when a scaffold exists) as the sibling
path and project name with no `include`.

**Requirements:** R1, R2, R4

**Dependencies:** U1

**Files:**
- Modify: `internal/system/payload.go`
- Modify: `internal/system/create.go` (summary line that prints
  `model.Shared.Path` must tolerate absence; the post-render
  `projectName`/`systemClass` read-back stays)
- Test: `internal/system/payload_test.go`

**Approach:**
- Components: emit `topic` (the topic key), `connector`, `from`, `to` raw
  names; the template resolves them to fields. Keep `kind`/`appId` verbatim.
- `sharedContracts`: `{name, path}` only when a shared scaffold was
  detected; absent entirely otherwise. Delete `contractsInclude`.
- Delete `pascalIdent` from the CLI once no caller remains (check
  `assemble.go` field derivation is gone first — U1).
- The completion line (`assembled system … contracts from %s`) prints
  "no contracts project" (or omits the clause) when shared is absent.

**Patterns to follow:**
- `payload_test.go`'s existing assertions on list emptiness ("an (empty)
  list so the template can range over it") — keep topics/connectors always
  present as lists.

**Test scenarios:**
- Happy path: full model → payload has raw names, no `field` keys anywhere,
  `sharedContracts` has no `include`.
- Edge case: transactional-only model → payload has empty `topics`,
  `sharedContracts` absent, components carry `from`/`to`.
- Edge case: topic block without connector → component carries `topic` and
  no `connector` key (mirrors today's `connectorField: ""` intent).
- Regression: payload map keys stay a closed set the template schema can
  declare (no stray keys).

**Verification:**
- `go test ./internal/system/` passes; payload contains no CLI-derived
  rendering identifiers.

---

- [x] U3. **Update the CLI-side fixture to the new payload contract**

**Goal:** the trimmed system-host manifest and declaration templates in
`create_test.go` mirror the new library template (facts-only payload,
optional `sharedContracts`, conditional `Topics.cs`), so CLI tests pin the
new contract instead of the old one.

**Requirements:** R1, R2, R3, R4

**Dependencies:** U2

**Files:**
- Modify: `internal/system/create_test.go`
- Test: `internal/system/create_test.go` (the `TestCreateAssembles*` tests)

**Approach:**
- Manifest fixture: `sharedContracts` drops out of `required`; add the
  `spec.files` rule gating `Topics.cs.tmpl` on non-empty topics; add the
  conditional `spec.dependencies` entry for the shared-library template
  (expressed in the fixture the same way the real template will, per Phase
  2's engine decision).
- Declaration fixtures: `Topics.cs.tmpl` derives fields from names via the
  same sprig pipeline Phase 2 uses; the csproj fixture gates the
  `ProjectReference` on `sharedContracts` presence and computes the include
  path in-template; the system-class fixture resolves wiring names to
  fields.
- Add an end-to-end `sys create` test over a transactional-only workspace:
  host renders, no `Topics.cs` on disk, csproj has no contracts reference,
  scaffold record written.

**Test scenarios:**
- Integration: transactional-only workspace → `Create` succeeds, rendered
  host has no `Topics.cs`, csproj has no `ProjectReference`.
- Integration: extractor+loader workspace without contracts scaffold →
  `Create` succeeds and a `Contracts/` sibling appears (dependency render).
- Regression: extractor+loader workspace with existing contracts scaffold →
  output byte-identical to today's golden expectations, sibling reused not
  recreated.

**Verification:**
- `go test ./internal/system/` passes end-to-end against the fixture;
  `go build ./...` clean.

---

### Phase 2 — Template library: own the contracts decision

- [x] U4. **Engine support for conditional, pre-skeleton dependencies (if needed)**

**Goal:** the template engine can (a) evaluate a `when` expression on a
`spec.dependencies` entry against the resolved parent values, and (b) render
dependencies before the parent skeleton, so the system-host csproj can
reference a dependency that may be created in the same run.

**Requirements:** R3

**Dependencies:** None (CLI repo; sequenced with U5)

**Files:**
- Modify: `internal/template/manifest.go` (`DependencySpec` gains `When`,
  validation mirrors `FileRule`)
- Modify: `internal/template/dependencies.go` (skip entries whose `when`
  evaluates false; reorder render vs. skeleton in
  `internal/template/create.go`)
- Test: `internal/template/dependencies_test.go`, `internal/template/manifest_test.go`

**Approach:**
- `when` semantics, truthiness, and load-time compile checks mirror
  `FileRule` exactly (manifest.go:248-261) — one condition mechanism, two
  consumers.
- Reordering: render dependencies before the skeleton only when the parent
  declares any; keep the skip-if-already-scaffolded behavior untouched.
- If review prefers not to reorder, the documented fallback is: host
  template always declares the dependency and renders it unconditionally —
  record that decision in the PR description, not silently.

**Test scenarios:**
- Happy path: dependency with `when` true → rendered; `when` false →
  skipped, no directory created, no scaffold record.
- Edge case: `when` false and target directory already scaffolded → still
  skipped (no drift warning noise) — confirm intended semantics in review.
- Error path: invalid `when` expression → manifest load error naming the
  dependency index, mirroring the `spec.files` error shape.
- Regression: dependency without `when` renders exactly as today.

**Verification:**
- `go test ./internal/template/` passes; manifest validation rejects empty
  `when` on a dependency that declares the key.

---

- [x] U5. **system-host template: optional contracts, derived fields, conditional dependency**

**Goal:** the real `system-host` template in `integrio-intropy/intropy-templates`
consumes the facts-only payload: `sharedContracts` optional, `Topics.cs`
included only when topics exist, field identifiers and the csproj include
path derived in-template, and a conditional dependency on the shared-library
template when topics exist and no contracts scaffold was detected.

**Requirements:** R1, R2, R3, R4

**Dependencies:** U2 (payload contract), U4 (if the engine changes)

**Files:**
- Modify (templates repo): `system-host/template.yaml`
- Modify (templates repo): `system-host/skeleton/Topics.cs.tmpl`,
  `Connectors.cs.tmpl`, the system-class template, the development
  definition, the host csproj template
- Test (templates repo): whatever golden-render coverage the library has;
  mirror the new cases in `internal/system/create_test.go` fixtures

**Approach:**
- Manifest: drop `sharedContracts` from `required`; declare the new payload
  keys; add the `spec.files` gate for `Topics.cs.tmpl`; add the conditional
  dependency (`when: topics non-empty and sharedContracts absent`).
- Derivation: one sprig pipeline, defined once and reused, that maps a raw
  name to the C# field identifier; the CLI's `pascalIdent` is the reference
  behavior (split on `-`, `.`, `_`; title-case segments; join).
- csproj: `<ProjectReference>` rendered only when `sharedContracts`
  present; include path computed from the host output dir and the sibling
  path the CLI passed.
- Release the library and note the minimum CLI version that emits the
  facts-only payload.

**Test scenarios:**
- Happy path: topic-bearing payload with contracts → rendered output matches
  the pre-change golden output byte-for-byte (proves R2/R4: the move is
  behavior-preserving for existing systems).
- Happy path: transactional-only payload → no `Topics.cs`, no contracts
  reference, valid host.
- Edge case: two topics whose names derive to the same field → render fails
  with a message naming both topics (per the collision-detection decision).
- Integration: topic-bearing payload without `sharedContracts` → dependency
  renders a `Contracts/` sibling and the csproj references it.

**Verification:**
- Template release published; `intropy int create` + `sys create` against
  the new tag reproduces all four scenarios from a clean workspace.

---

- [x] U6. **Version gate and release wiring**

**Goal:** a new CLI against an old template release (and vice versa) fails
with an actionable message instead of rendering a broken host.

**Requirements:** R1, R5

**Dependencies:** U3, U5

**Files:**
- Modify: `internal/system/create.go` (detect missing/extra payload keys
  from the rendered scaffold record or manifest and error with the minimum
  required template release)
- Test: `internal/system/create_test.go`

**Approach:**
- Cheap probe: after fetching the system-host manifest, verify it accepts
  the facts-only payload (e.g. schema declares the new keys / no longer
  requires `sharedContracts`); on mismatch, error: "system-host template at
  <tag> predates facts-only payloads — use template release >= vX.Y.Z or
  CLI <= vA.B.C".
- Document the pairing in both repos' release notes.

**Test scenarios:**
- Error path: CLI payload against an old-shape manifest → clear version
  error, no files written.
- Regression: matching versions render normally.

**Verification:**
- `go test ./...` green; manual smoke against both an old and the new
  template tag shows the intended error/success.

---

## System-Wide Impact

- **Interaction graph:** `sys create` is the only caller of `Assemble` and
  `buildPayload`; the dashboard (`internal/dashboard/handlers.go:195`) reads
  scaffold roles but does not consume the payload, so it is unaffected.
- **Error propagation:** the "no shared contracts" error disappears; new
  failure modes are the template-render failure for field collisions and the
  version-gate error — both must name the offending names/release.
- **State lifecycle risks:** reordering dependency rendering before the
  skeleton (U4) changes on-disk creation order; a failed skeleton render may
  now leave a freshly scaffolded `Contracts/` behind — acceptable (it is a
  valid project on its own) but note it in the PR.
- **API surface parity:** `--output-json` `Summary.SharedLibrary` becomes
  empty for transactional-only systems; that is additive-compatible
  (consumers already handle lists; a string emptying is the only drift —
  check the dashboard/web consumers during U2).
- **Unchanged invariants:** topic contract conflict detection, connector
  uniqueness, appId uniqueness, and the "skip already-scaffolded dependency"
  behavior are all preserved exactly.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| sprig cannot express `pascalIdent` exactly, changing generated field names for existing systems | U5's golden byte-for-byte test catches it; fall back to the closest sprig pipeline and treat any diff as a breaking change requiring a template major/minor bump |
| Moving field-collision errors from CLI to template produces unreadable render failures | Deferred decision with a documented fallback (CLI keeps a raw-name uniqueness pre-check); decide during U5 with a real error message in hand |
| Dependency-before-skeleton reorder (U4) leaks a partial workspace on render failure | Accepted and documented; the leftover `Contracts/` is independently valid |
| Old CLI + new template or new CLI + old template renders a broken host silently | U6 version gate; release notes in both repos name the pairing |
| Templates-repo review rejects `when` on dependencies | Documented fallback: unconditional dependency declaration, no-op render when topic-less |

---

## Documentation / Operational Notes

- README's assembly section (`README.md:303` mentions the `shared-library`
  role) needs a paragraph: hosts may be contracts-less; the host template
  scaffolds contracts on demand for topic-bearing systems.
- Release coordination: templates release first, CLI release names the
  minimum template tag.

---

## Sources & References

- Origin: conversation analysis of `internal/system/assemble.go:100` and the
  transactional-integration shape (`internal/system/blocks.go`).
- Related code: `internal/system/{assemble,payload,create,model}.go`,
  `internal/template/{dependencies,manifest,reserved,create}.go`,
  `internal/system/create_test.go` fixtures.
