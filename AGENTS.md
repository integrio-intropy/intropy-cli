# Writing style — comments, help text, and CLI output

This file is the house style for everything a reader sees: Go doc comments,
`--help` output, and the strings a command prints or returns as errors. It
applies to every Go file in this repository. Keep it short enough that it can
be re-read before every PR.

---

## Code comments

**Explain why, not what changed.** A comment answers the question the code
cannot: the invariant, the trade-off, the reason a non-obvious choice is the
safe one. It never narrates history.

- Never reference a PR, issue, fix, or the past tense of the code itself.
  "Used to", "now", "previously", "after the refactor", "fixes #123" are all
  forbidden. Git history already knows; the reader of the code does not care.
- A doc comment on every exported symbol. One or two sentences is usually
  enough. `golint` enforces this; don't make a reviewer do it.
- Target ≤ 10 lines per doc block. If you need more, the extra content
  belongs in `docs/` or a README, not above the function.
- "Deliberately not X" comments are encouraged when they stop a plausible
  wrong refactor. `internal/gitops/remote.go` is the exemplar: it says the
  function is *not* used to derive the cache directory, and why.
- No TODO, FIXME, HACK, or XXX in committed code. File an issue instead.

---

## Help text (cobra)

`--help` is reference material, not a blog post. A reader in a terminal wants
to know what the command does, what it costs, and which flags change the
behavior. Philosophy and motivation belong in `docs/`.

- **`Short:`** one line, imperative verb, no trailing period.
  Good: `Pin a component's image digest into an environment`
  Bad:  `This command pins the image digest.`
- **`Long:`** target ≤ 150 words, hard ceiling ~200. Cover: what it does,
  what it reads, what it writes, what can go wrong that the user can prevent.
  Cut: why the command exists, why GitOps is good, architecture essays.
- **`Use:`** positional args in `<>`, optional in `[]`, variadic as `name...`.
  Good: `pin <component> [version]`
  Bad:  `pin COMPONENT [VERSION]`, `pin <component> [<version>]`

If a `Long:` draft exceeds 200 words, the excess is almost always the second
paragraph onwards. Move it to a file under `docs/` and keep the help text
to usage.

---

## Flag descriptions

Use the shared constants in `cmd/intropy/flagtext.go`. Never hand-write a new
description for a flag another command already has:

- `--output`, `--env`, `--gitops-repo`, `--argocd-server`, `--domain`,
  `--system`, `--template-version` all have constants.
- If you add a flag that several commands will share, add a constant first.
- Flag descriptions are fragments, not sentences: no trailing period, no
  "This flag…". Start with the noun. Good: `target environment (required)`.

---

## Runtime output

A CLI's output is read twice: once by a human scanning, once by a script
parsing. The two never share a stream.

- **stdout is for results.** Tables, JSON, the thing the command was run to
  produce.
- **stderr is for everything else.** Progress, warnings, completion messages,
  errors. A pipeline reading stdout must never see a status line.

### Voice

- Progress and completion: **lowercase, present tense, no trailing period**.
  Good: `syncing api on argocd.example.com at a1b2c3d`
  Good: `published ghcr.io/example/skills/pr-review:1.2.0`
  Bad:  `Published ghcr.io/…`  `Sync complete.`
- Empty states follow the same rule: `no skills installed`, not
  `No skills installed.`
- A follow-up hint naming a command goes on its own line, in backticks:
  `use 'intropy skills add <ref>' to add one`

### Errors

- Front-load what failed. The remediation, if there is one, goes on a second
  line. Do not editorialize — the user is already reading an error.
  Good: `pending change is a1b2c3d, not the f4e5d6c you reviewed`
        `run 'intropy deploy diff api --env staging' before syncing`
  Bad:  `…has advanced since you reviewed it — you really should read the new diff…`
- Wrap errors with context as they travel up: `fmt.Errorf("pull %s: %w", …)`.
  The outermost prefix names the command or operation, not the internals.

### Vocabulary

- **image digest**, not "bits", "artifacts", or "the images".
- **environment** in prose, `--env` only when naming the flag.
- `->` for a transition (`1.0 -> 1.1`), ` @ ` for a pin (`api @ 1.2.0`).
  Never mix the two in one message.
- A commit SHA is shown short (7 chars) unless the full value is the point.

### Command verbs

One verb per meaning, used on every noun (`int`, `template`, `skills`, ...).
The full set, grouped by what the verb does to the world:

**Read-only** — exit 0 even when the answer is "they disagree", never write
to git, never trigger a sync:

- **`list`** enumerates a collection: integrations on disk, templates in the
  library, installed skills, published releases. It never prints one item's
  details.
- **`show`** prints the details of exactly one thing: a template's manifest,
  an integration's scaffold record. It never enumerates.
- **`status`** reports one component across every environment, one row
  each, and whether the rows agree. It never prints one environment's
  details — that is `show`.
- **`diff`** prints the rendered change a `sync` would apply, as the
  resources themselves. Both sides are committed revisions; it never diffs
  an uncommitted worktree.

**Create or change local project state** — may write files under the
project, never push:

- **`create`** makes something new from a template or from scaffolded
  parts: an integration, a system host, a release manifest to publish.
- **`add`** attaches something that already exists elsewhere to this
  project: a skill from a registry, a collection registration. Unlike
  `create`, the content is pulled, not generated.
- **`update`** reconciles something installed against the ref its source
  now pins: a skill against its collection, the cached collection index
  against the registry. It never changes which ref is pinned — that is
  the publisher's job.
- **`init`** scaffolds the manifests a system needs. Against the GitOps
  repository it writes the tree and pushes a branch for review, never the
  default branch. With `--local` it renders the local development cluster's
  overlay instead and streams the built manifests to stdout, for piping to
  kubectl; its only write then is the workspace state file
  `.intropy/deploy-values.yaml`. One pipeline owns both destinations —
  topology, the connector question and the render are shared; only where
  the manifests land differs.

**Move or publish** — change what runs somewhere or what others can pull:

- **`publish`** pushes an immutable artifact to an OCI registry: a skill,
  a collection, a release manifest. What is published cannot be edited in
  place; a new version is a new publish.
- **`pin`** writes one component's image digest into one environment's
  overlay.
- **`promote`** copies the digests one environment runs into the next
  environment in the promotion graph.
- **`sync`** applies an environment's pending change through ArgoCD. It
  never decides what the change is — that is `pin` and `promote`.

Do not import kubectl's `get`/`describe` split — `int describe` was retired
because it described templates, not integrations. New verbs need a reason
that the set above cannot express, and the reason goes in this file with
the verb, not in a commit message.

---

## What this file is not

It does not replace `CONTRIBUTING.md` (workflow, tests, commit convention) or
`README.md` (what the CLI is). When those disagree with this file, this file
wins on writing style and loses on everything else.
