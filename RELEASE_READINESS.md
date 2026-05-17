# Release-readiness review — `intropy` CLI

**Last updated:** 2026-05-17

## Verdict

The core command surface is well-structured and Cobra-idiomatic, but **not yet ready for a public v1 release**. The biggest remaining gaps are test coverage in the skills stack, a release pipeline, and a few CLI-discipline inconsistencies that will bite scripted/Backstage callers.

**What changed since the last review:** LICENSE, README, CONTRIBUTING.md, and Makefile are now in place. Quickstart and Troubleshooting sections added to README. `hello-world/` cleaned up.

---

## What's already good

| Area | Status |
|---|---|
| Project layout (`cmd/intropy/` one-file-per-command, minimal `main.go`) | ✅ |
| `SilenceUsage: true` + `SilenceErrors: true` on root (`cmd/intropy/root.go:13`) | ✅ |
| Version vars wired for `-ldflags` injection (`cmd/intropy/version.go:11`) | ✅ |
| Exit-code mapping 0/1/2 (`cmd/intropy/main.go:17`) | ✅ |
| Signal handling via `signal.NotifyContext` on long-running cmds | ✅ |
| `cmd.OutOrStdout/ErrOrStderr` throughout — testable I/O | ✅ |
| Argument validators (`ExactArgs`, `MaximumNArgs`, `NoArgs`) used everywhere | ✅ |
| `MarkFlagRequired("output")` on `int create` | ✅ |
| Build clean, tests green | ✅ |
| LICENSE (MIT) | ✅ |
| README with install, command reference, and Backstage call-out | ✅ |
| CONTRIBUTING.md with Makefile workflow | ✅ |
| Makefile with build/test/quality targets | ✅ |
| `hello-world/` removed from repo root | ✅ |

---

## Gaps to close before tagging v1

### 1. No release pipeline

**Blocker for a public tag.**

- **No `.github/workflows/`** — at minimum `go test ./...`, `go vet`, `gofmt -l` on PRs.
- **No `.goreleaser.yaml`** — needed to produce per-OS binaries and inject version via ldflags.
- **`intropy version` still prints `dev unknown unknown`** unless built with `make build` or manual ldflags. A release workflow must verify the binary reports the tag.

**Fix:** Add `.github/workflows/test.yaml` (PR CI) and `.goreleaser.yaml` (release binaries). The `Makefile` already has the correct ldflags — wire it into CI.

### 2. Inconsistent stdout/stderr discipline

Stdout is for *machine-readable program output*; diagnostics go to stderr.

| File | Line | Issue |
|------|------|-------|
| `skills_collection_add.go:89-90` | `cmd.Printf` "Registered collection …" to **stdout** — diagnostic, should be stderr |
| `skills_collection_update.go:94-95` | `cmd.Printf` "Refreshed collection …" to **stdout** — same |
| `skills_update.go:64,89` | "No skills installed." / "Nothing to update." → stdout. Per-skill "Updated X" / "X already at Y" → **stderr** — mixed |
| `skills_list.go:20,32-33` | "No skills installed." → stdout. Defensible (empty list is the list) but inconsistent with `skills_update` |
| `internal/blueprint/create.go:60-63` | Falls back to `os.Stdout`/`os.Stderr` if caller doesn't pass them. Dead code today (`int_create.go:46-47` always passes both) — remove the fallback so the package can never bypass test capture |

**Fix:** Move all "X succeeded" diagnostics to `cmd.ErrOrStderr()`; reserve `cmd.OutOrStdout()` for command output (JSON, tables, lists). Standardize empty-state messages.

### 3. Machine-readable output is inconsistent

This matters because the stated north star is Backstage scaffolder consumption.

- `int describe` uses `--json` (`int_describe.go:50`).
- `int create` uses `--output-json <path>` (different flag, writes to a file path) (`int_create.go:61`).
- `skills list`, `skills add`, `skills update`, `skills collection add`, `skills collection update` — **no JSON output at all**. Scripts must parse human-readable strings.

**Fix:** Pick one convention (`--output {table|json|plain}` with stdout-only is the common idiom) and apply across the board, especially on every "mutation succeeded" command — Backstage will want to read the resulting digest/version from a known JSON shape.

### 4. `isUsageError` classification is fragile

`cmd/intropy/main.go:27-42` matches Cobra's English error message prefixes. The test in `skills_add_test.go:23` even leans on the magic phrase `"requires "` in your hand-rolled error in `skills_add.go:41`. A Cobra release that rewords its errors silently breaks exit-code 2 mapping.

**Fix:** Define a sentinel/typed `usageError` and have any `RunE` that wants exit 2 return it. Detect Cobra's flag-parse errors via `errors.Is(err, pflag.ErrHelp)` etc. where available, or `SetFlagErrorFunc` to wrap them.

### 5. Missing baseline UX flags

For a release-quality CLI, add to the root persistent flags (and a `PersistentPreRunE` to apply them):

- `--verbose` / `-v` and `--quiet` / `-q` (log level)
- `--no-color` (color libs auto-detect TTY, but a manual override is expected)
- `--version` on the root — set `rootCmd.Version = version` so `intropy --version` works in addition to the `version` subcommand

### 6. No configuration layering

The project uses Cobra but **no Viper** — no env-var/config-file overlay. Registry credentials, timeouts, default collection name, etc. all live in code. For a Backstage-scaffolder caller, env-var overrides (e.g. `INTROPY_REGISTRY_USER`, `INTROPY_BLUEPRINT_VERSION`) are the natural plumbing path.

**Fix:** Add Viper wiring with `--config` + `INTROPY_*` env prefix. Even a minimal setup (config file path, env prefix, and a few key overrides) would close this.

### 7. No shell completions / argument completion

Cobra ships `completion` by default, but there are **zero** `ValidArgsFunction` / `RegisterFlagCompletionFunc` registrations. Easy wins:

- `<blueprint>` arg on `int create`/`int describe` → completion from the GitHub blueprint index
- `<name>` arg on `skills update` → list installed skills from lockfile
- `<alias>` arg on `skills collection update` → list registered collections
- `--collection` flag value → same source

Then document `intropy completion zsh|bash|fish > …` in the README.

### 8. Test coverage gaps — **biggest quality bar**

| Package | Coverage | Gap |
|---------|----------|-----|
| `cmd/intropy` | 33.0% | Only `skills_add`, `skills_list`, `skills_publish`, `collection_publish` have tests. `skills_update`, `skills_collection`, `skills_collection_add`, `skills_collection_update` have **none** |
| `internal/blueprint` | 64.9% | Good |
| `internal/skill` | **0.0%** | No tests at all — adder, installer, updater, registry, manifest, lockfile, project, extractor, collectioncache |
| `internal/skill/oci` | 4.7% | Only `reference_test.go` covers reference parsing. Push, resolve, index publish untested |
| **Total** | **36.2%** | Far below a believable v1 bar |

**Fix:**
- Add CLI-level tests for `skills update`, `skills collection add`, `skills collection update`.
- Add unit tests for `internal/skill/*` — manifest/lockfile read-write, project discovery, updater logic, collection cache.
- Add unit tests for `internal/skill/oci/*` — mock registry HTTP for push/pull/resolve.
- Confirm test parallelism is off, or refactor flag-backing globals (`skillsAddOpts` reset in `skills_add_test.go:62`) to per-command structs constructed in `init()` to allow `t.Parallel()`.

### 9. Minor inconsistencies

| Issue | Fix |
|-------|-----|
| `int describe` uses `--json` (boolean, stdout); `int create` uses `--output-json <path>` (file path). Pick one convention | Unify to `--output json` or `--json` consistently |
| `skills add --also-install-to` (`skills_add.go:118`) uses `StringSliceVar` (splits on commas); `int create --values/-f` uses `StringArrayVarP` (doesn't split). Divergence trips users | Use `StringArray` for paths consistently |
| `skills collection` help is a one-liner only | Add a `Long` description |
| `--values -` (stdin) is mentioned in `int_create.go:57` flag description but not in `Long` | Document precedence with `--set` and what "one doc from stdin" means in the command long help |

---

## Suggested release checklist

### Blockers (must have for public tag)

1. [ ] Add `.github/workflows/test.yaml` — `make check` on PRs
2. [ ] Add `.goreleaser.yaml` + release workflow — verify `intropy version` prints the tag
3. [ ] Replace `isUsageError` string matching with typed sentinel errors

### Quality bars for believable v1

4. [ ] Move all "X succeeded" diagnostics to stderr; reserve stdout for command output
5. [ ] Unify the JSON-output story across mutating commands (`--output json` or `--json`)
6. [ ] Add root persistent `--verbose`/`--quiet`/`--no-color` and `rootCmd.Version`
7. [ ] Wire Viper with `--config` + `INTROPY_*` env prefix
8. [ ] Add completion functions for known enumerable args (blueprints, installed skills, registered collections)
9. [ ] Bring `internal/skill/*` and the four uncovered commands above 50% test coverage
10. [ ] Remove `internal/blueprint/create.go:60-63` `os.Stdout`/`os.Stderr` fallback

---

## What was fixed since the previous review

| Original Gap | Fix | Commit |
|---|---|---|
| No LICENSE | Added MIT License | Prior to this review |
| No README | Added full README with install, commands, examples | Prior to this review |
| No CONTRIBUTING.md | Added with Makefile workflow, Cobra/Viper patterns, PR checklist | `ee01fe4` |
| No Makefile | Added with build, test, quality, and dev targets | `ee01fe4` |
| No Quickstart in README | Added 3-command end-to-end quickstart | `0ecea72` |
| No Contributing link in README | Added section linking to CONTRIBUTING.md | `ee01fe4` |
| No Troubleshooting in README | Added table with 4 common issues | `0ecea72` |
| `hello-world/` in repo root | Removed | `ee01fe4` |
| No License/DCO in CONTRIBUTING | Added MIT license line | `ee01fe4` |
