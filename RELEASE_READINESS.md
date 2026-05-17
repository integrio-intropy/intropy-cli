# Release-readiness review — `intropy` CLI

**Last updated:** 2026-05-17

## Verdict

The CLI is **approaching v1 readiness**. All blockers are closed. The remaining gap is test coverage in the OCI networking layer (`internal/skill/oci/*` push/pull/resolve), which requires mock-registry HTTP scaffolding — substantial but not a release blocker. Viper config layering is intentionally deferred.

---

## What's already good

| Area | Status |
|---|---|
| Project layout (`cmd/intropy/` one-file-per-command, minimal `main.go`) | ✅ |
| `SilenceUsage: true` + `SilenceErrors: true` on root | ✅ |
| Version vars wired for `-ldflags` injection | ✅ |
| Exit-code mapping 0/1/2 with typed `usageError` sentinel | ✅ |
| Signal handling via `signal.NotifyContext` | ✅ |
| `cmd.OutOrStdout/ErrOrStderr` throughout | ✅ |
| Argument validators used everywhere | ✅ |
| Stdout/stderr discipline — diagnostics to stderr, output to stdout | ✅ |
| Build clean, tests green | ✅ |
| LICENSE (MIT) | ✅ |
| README with install, quickstart, command reference, troubleshooting | ✅ |
| CONTRIBUTING.md with Makefile workflow | ✅ |
| Makefile with build/test/quality targets | ✅ |
| `.github/workflows/test.yaml` — PR CI | ✅ |
| `.goreleaser.yaml` + `.github/workflows/release.yaml` | ✅ |
| Root persistent flags: `--verbose/-v`, `--quiet/-q`, `--no-color`, `--version` | ✅ |
| Shell completions for blueprints, installed skills, collections | ✅ |
| Unified `--output/-o {plain\|json}` across all commands | ✅ |
| `hello-world/` removed from repo root | ✅ |

---

## Coverage snapshot

| Package | Coverage | Notes |
|---------|----------|-------|
| `cmd/intropy` | ~44% | All commands have at least smoke tests |
| `internal/blueprint` | ~62% | Good — GitHub API mocked with `httptest` |
| `internal/skill` | ~59% | Adder, installer, updater, manifest, lockfile, project, collection cache covered |
| `internal/skill/oci` | ~29% | Reference parsing, Pack covered. Push, Pull, Resolve, PullIndex, PushIndex need mock-registry HTTP |
| **Total** | **~54%** | Above the 50% quality bar for `internal/skill/*` and `cmd/intropy` |

---

## Open items (non-blocking for v1)

### 1. OCI push/pull/resolve test coverage

`internal/skill/oci/push.go`, `pull.go`, `resolve.go`, `pullindex.go`, `pushindex.go` are untested. These talk to real OCI registries via `oras-go`. Unit testing requires either:

- **Option A:** `httptest` server that speaks the OCI distribution spec (substantial mock)
- **Option B:** `oras-go` memory store for unit tests
- **Option C:** Integration tests with a local registry container (slow, belongs in CI)

**Recommendation:** Add Option B unit tests + a lightweight integration test in CI before v1.1.

### 2. Viper configuration layering — **deferred to post-v1**

**Why deferred:**
- No natural config surface yet — all flags are per-invocation (ref, name, output path)
- OCI auth already flows through Docker credential chain
- Blueprint library URL is hardcoded to the official repo
- Viper adds ~15 transitive dependencies for a feature no user has requested
- Every config surface is a backward-compat contract forever

**When to revisit:**
- A user opens an issue asking for default registry / timeout / collection configuration
- The CLI grows to 15+ flags and repetition becomes painful
- A `~/.intropy/` config dir is needed for credential helpers or template caches

**Migration path (documented for future):** Add Viper to `PersistentPreRunE`, bind flags to `INTROPY_*` env vars, add `--config` flag. Cobra + Viper integration is well-documented and low-risk when the need is clear.

---

## What was fixed since the previous review

| # | Original Gap | Fix | Commit |
|---|-------------|-----|--------|
| 1 | No `.github/workflows/` | Added `test.yaml` (PR CI) and `release.yaml` (GoReleaser) | `9442a24` |
| 2 | No `.goreleaser.yaml` | Added with cross-platform builds, ldflags version injection, changelog grouping | `9442a24` |
| 3 | `isUsageError` string matching | Replaced with typed `usageError` sentinel (`errors.As`-detectable) | `1ffc4b1` |
| 4 | Stdout/stderr mixed | Moved all diagnostics to `cmd.ErrOrStderr()`; stdout reserved for program output | `1ffc4b1` |
| 5 | Inconsistent `--json` / `--output-json` | Unified to `--output/-o {plain\|json}` across all commands. `int create --output-json <path>` preserved as sidecar document | `a5247f3` |
| 6 | Missing root flags | Added `--verbose/-v`, `--quiet/-q`, `--no-color`, `--version` | `27ad606` |
| 7 | No shell completions | Added `ValidArgsFunction` for blueprints, installed skills, collections; `--collection` completion | `27ad606` |
| 8 | Test coverage gaps | `internal/skill`: 0% → 59%. `cmd/intropy`: 33% → 44%. Total: 36% → 54% | `2a424c2` |
| 9 | Dead `os.Stdout`/`os.Stderr` fallback | Removed from `internal/blueprint/create.go` | `1ffc4b1` |
| — | No `RELEASE_READINESS.md` | Created and maintained | `2ba19a2` |

---

## Suggested release checklist (for v1.0.0 tag)

- [x] LICENSE, README, CONTRIBUTING.md, Makefile in place
- [x] CI pipeline (`make check` on PRs, GoReleaser on tags)
- [x] Typed `usageError` sentinel + Cobra fallback
- [x] Stdout/stderr discipline enforced
- [x] Unified `--output/-o` flag convention
- [x] Root persistent UX flags
- [x] Shell completions for enumerable args
- [x] `internal/skill/*` and CLI commands above 50% coverage
- [ ] OCI push/pull/resolve unit tests (v1.1)
- [ ] Viper config layering (deferred until user demand)
