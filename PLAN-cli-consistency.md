# CLI Consistency Analysis — intropy-cli

## Command tree inventory

```
intropy
├── completion            (cobra built-in)
├── dashboard [dir]       --addr --port -p --no-browser
├── deploy <component> [version]   ← runnable AND parent
│   ├── diff <component>    --env -e --domain --system --gitops-repo --argocd-server --output -o
│   ├── init [component...] --domain --system --env -e --topology --source-dir --version --values -f --set -s --no-input --plan --force --gitops-repo --output -o
│   ├── promote <component> --from --to --domain --system --gitops-repo --argocd-server --plan --no-wait --timeout --output -o
│   ├── status <component>  --domain --system --gitops-repo --argocd-server --output -o
│   └── sync <component>    --env -e --revision --domain --system --gitops-repo --argocd-server --no-wait --timeout --output -o
├── int
│   ├── create <template>   --output -o(dir!) --name -n --version --values -f --set -s --force --no-input --install-skills --skip-install-skills --output-json
│   ├── show [dir]          --output -o(format)
│   └── list [dir]          --output -o(format)
├── template
│   ├── list                --template-version --output -o(format)
│   └── show <template>     --template-version --output -o(format)
├── release
│   ├── create <component>  --version(required) --ref --since --domain --system --gitops-repo --allow-dirty --watch -w --output -o
│   ├── list <component>    --domain --system --gitops-repo --output -o --limit -n
│   └── view <component> <version>  --domain --system --gitops-repo --output -o
├── skills
│   ├── add [ref]           --also-install-to --name --collection --output -o
│   ├── collection
│   │   ├── add             --name --ref --output -o
│   │   ├── publish <spec-file> <ref>  --force
│   │   └── update <alias>  --ref --output -o
│   ├── list                --output -o
│   ├── publish             --path --ref --tag --force --sign
│   └── update [name]       --all --output -o
├── sys
│   └── create              --name -n(required) --output -o(dir!) --version --force --output-json
└── version
```

---

## Findings

### 1. `--output` / `-o` means two different things (HIGH) ✅ DONE

| Command | Meaning |
|---|---|
| `int create -o` | **destination directory** on disk |
| `sys create -o` | **destination directory** on disk |
| everything else (18 uses) | **output format** (plain/json) |

A user who learns `-o` = format from `skills list -o json` will be surprised when `int create -o foo` writes a directory named `foo`. This is the most dangerous inconsistency because it's silent — no error, just a different effect.

**Fix direction:** rename the directory flag in `int create` and `sys create` to `--dir` / `--out-dir` (keep `-o` reserved for format everywhere). `int create` already has `--output-json` for the machine-readable path, so the format slot is free. Backward-compat: keep `--output` as a hidden deprecated alias for one release.

---

### 2. `--output-json` vs `--output json` — two machine-readable conventions (HIGH) ✅ DONE

| Command | Machine-readable output |
|---|---|
| `int create` | `--output-json <path>` (writes to file, `-` = stdout) |
| `sys create` | `--output-json <path>` (same) |
| `int list`, `int describe`, all `skills`, all `deploy`, all `release` | `--output json` (writes to stdout) |

So `int create` has *both* a `--version` template tag flag and a separate `--output-json` file path, while its sibling `int describe` uses `--output json`. A user scripting across `int describe --output json` and `int create --output-json result.json` has to learn two patterns for the same CLI.

**Fix direction:** converge on `--output json` for stdout everywhere; keep `--output-json <path>` only where a file artifact is genuinely needed (scaffold result documents), but document it as the exception. Alternatively, add `--output json` to `int create`/`sys create` for parity and deprecate `--output-json`.

---

### 3. `--version` means three different things (MEDIUM) ✅ DONE

| Context | `--version` meaning |
|---|---|
| root (`intropy --version`) | CLI version (cobra built-in) |
| `int create`, `int describe`, `deploy init`, `sys create` | **template library release tag** |
| `release create` | **version to publish** (required) |
| `release view` | positional arg, not a flag |
| `deploy <component> [version]` | positional arg, not a flag |

The comment in `deploy_init.go` shows the authors know about this ("Named --version to match int create and int describe... cobra keeps it local to the root rather than persistent, so the name is free here"). But `release create --version 1.2.0` and `int create --version v3` mean entirely different things (a semver you are *publishing* vs a template tag you are *consuming*). And `release view` puts version positional while `release create` puts it in a flag.

**Fix direction:** rename template-tag flags to `--template-version` (or `--template-tag`) in `int create`, `int describe`, `deploy init`, `sys create`. Keep `--version` on `release create` where it genuinely is the release version. This frees `--version` from ambiguity and aligns with the already-existing Go struct field name `templateVersion` in `deploy_init.go`.

---

### 4. Version as positional vs flag (MEDIUM) ✅ DONE (`skills publish --tag` → `--version`; positional/flag alignment of release create/view left as-is by design)

| Command | Version passed as |
|---|---|
| `deploy <component> [version]` | positional (2nd arg) |
| `release view <component> <version>` | positional (2nd arg) |
| `release create <component> --version X` | flag (required) |
| `skills publish --tag X` | flag named `--tag` |
| `int create --version` | flag (template tag) |

`release create` and `release view` are the same domain (release manifests) but disagree. `skills publish --tag` introduces yet another synonym for "version".

**Fix direction:** pick one. Since `release create` needs several other flags anyway, keeping `--version` there is fine, but consider `release create <component> <version>` positional to match `release view`. Rename `skills publish --tag` to `--version` for cross-CLI consistency (the OCI tag *is* the skill version — the code even says "The tag becomes the skill version in the OCI config").

---

### 5. `--name` / `-n` shorthand collision with `--limit -n` (LOW) ✅ DONE (`--limit` is long-only now)

| Command | `-n` shorthand |
|---|---|
| `int create -n` | `--name` (integration name) |
| `sys create -n` | `--name` (system name) |
| `release list -n` | `--limit` (max releases) |

`-n` for `--name` and `-n` for `--limit` are different commands so there's no runtime conflict, but muscle memory across the CLI is misleading. `kubectl` uses `-n` for namespace; `docker` doesn't use `-n` for limit.

**Fix direction:** drop the `-n` shorthand from `release list --limit` (keep `--limit` long-only, or use `-l` if free).

---

### 6. `--env -e` means different things in deploy vs deploy init (LOW) ✅ DONE (`deploy init` now takes `--environments`, plural, no shorthand)

| Command | `--env -e` |
|---|---|
| `deploy`, `deploy diff`, `deploy sync` | **single** target environment (required) |
| `deploy init` | **list** of environments to scaffold overlays for (optional, repeatable) |

Same flag name, same shorthand, different cardinality and optionality. A user typing `deploy init -e staging` expecting "target staging" will instead get "scaffold an overlay for staging among others".

**Fix direction:** rename in `deploy init` to `--envs` or keep `--env` but drop the `-e` shorthand there to force a pause. Better: rename to `--environments` (plural signals list).

---

### 7. `deploy` is both a runnable command and a parent (DESIGN NOTE) ✅ DONE (deploy is now a pure parent; the runnable form is `deploy pin`)

`intropy deploy <component>` works, and `intropy deploy diff|init|promote|status|sync` also works. Cobra resolves subcommand names before positional args, and the Long description even documents this: "The subcommand names win over the component argument, so a component actually called diff, init, promote, status or sync is not reachable this way."

This is a deliberate design choice and documented, but it means a component named `sync` can never be deployed. Compare with `release`, which is purely a parent (`release create/list/view`) — no runnable `release` itself.

**Fix direction:** acceptable if documented (it is), but consider making `deploy` non-runnable and requiring an explicit verb (`deploy pin`?) for symmetry with `release` and `skills`. Low priority — the current design is at least self-aware.

---

### 8. `--from` / `--to` vs `--env` pattern (LOW)

`deploy promote` uses `--from`/`--to` for environments, while every other deploy subcommand uses `--env`. This is actually *good* (promote has two environments, others have one), but the shorthand inconsistency remains: `--env` has `-e` everywhere, while `--from`/`--to` have no shorthands. That's fine — just noting it's intentional.

No fix needed.

---

### 9. `skills collection add` requires flags for what siblings take positionally (LOW)

| Command | Primary identifier |
|---|---|
| `skills collection add` | `--name` + `--ref` (both flags, both required) |
| `skills collection update <alias>` | positional |
| `skills collection publish <spec> <ref>` | positional |

So within the same `collection` group, add takes flags, update takes positional alias, publish takes positional spec+ref. The `add` case is understandable (two pieces of data), but a user might expect `skills collection add <name> <ref>` to work, mirroring `publish <spec> <ref>`.

**Fix direction:** accept `--name`/`--ref` as flags (explicit is fine for registration), but consider also accepting two positional args `skills collection add <name> <ref>` for parity with `publish`. Or leave as-is — this is minor.

---

### 10. `--values -f` type inconsistency: StringArray vs StringSlice (LOW) ✅ DONE (both are StringArray now)

| Command | Flag type |
|---|---|
| `int create` | `StringArrayVarP` (no comma splitting) |
| `deploy init` | `StringSliceVarP` (comma splitting enabled) |

pflag's `StringSlice` splits on commas (`-f a,b` = two files); `StringArray` does not. Same flag name, same shorthand, different splitting behavior. A user who writes `--values base.yaml,extra.yaml` under `deploy init` gets two files; under `int create` they get one file literally named `base.yaml,extra.yaml`.

**Fix direction:** pick one — `StringArray` is the safer default (file paths can contain commas). Change `deploy init` to `StringArrayVarP`.

---

## Summary of recommended changes (priority order)

| # | Change | Commands affected | Breaking? |
|---|---|---|---|
| 1 | Rename directory flag `--output`→`--out-dir` | `int create`, `sys create` | Yes (deprecate old) |
| 2 | Converge machine-readable on `--output json` | `int create`, `sys create` | Additive |
| 3 | Rename template tag `--version`→`--template-version` | `int create`, `int describe`, `deploy init`, `sys create` | Yes (deprecate old) |
| 4 | Align `skills publish --tag`→`--version` | `skills publish` | Yes (deprecate old) |
| 5 | Drop `-n` from `release list --limit` | `release list` | Minor |
| 6 | Rename `deploy init --env`→`--environments` | `deploy init` | Yes (deprecate old) |
| 7 | Unify `--values` to StringArray | `deploy init` | Edge-case behavior |
| 8 | (Optional) positional args for `collection add` | `skills collection add` | Additive |

Items 1–3 are the high-value fixes: they remove genuine ambiguity where the same flag name silently does different things. Items 4–7 are polish. Item 8 is optional.
