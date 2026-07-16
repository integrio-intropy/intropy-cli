# Contributing to intropy CLI

Thank you for your interest in contributing! This document covers the workflow,
standards, and conventions we follow.

## Table of Contents

- [Getting Started](#getting-started)
- [Development Workflow](#development-workflow)
- [Commit Message Convention](#commit-message-convention)
- [Pull Request Process](#pull-request-process)
- [Code Standards](#code-standards)
- [Testing](#testing)
- [Project Conventions](#project-conventions)
- [Reporting Issues](#reporting-issues)

---

## Getting Started

### Prerequisites

- [Go 1.26+](https://go.dev/dl/)
- macOS or Linux (Windows developers: use [WSL 2](https://learn.microsoft.com/en-us/windows/wsl/install))
- A working Go module cache (`go env GOPATH`)

### Clone and build

```bash
git clone https://github.com/integrio-intropy/intropy-cli.git
cd intropy-cli
make build
```

Or manually:

```bash
go build -o bin/intropy ./cmd/intropy
```

Verify the build:

```bash
./bin/intropy version
# or
make run ARGS="version"
```

## Development Workflow

We use a **feature-branch workflow** with pull requests to `main`.

```bash
# Sync your fork
gh repo sync --branch main

# Or manually
git fetch upstream
git rebase upstream/main

# Create a feature branch
git checkout -b feat/short-description
```

### Branch naming

| Prefix | Use for |
|--------|---------|
| `feat/` | New commands, flags, or capabilities |
| `fix/` | Bug fixes |
| `docs/` | README, help text, or code comment changes |
| `refactor/` | Internal restructuring without behavior change |
| `test/` | New or updated tests |
| `chore/` | Dependency updates, tooling, maintenance |

**Examples:** `feat/int-create-dry-run`, `fix/template-archive-paths`, `docs/readme-install`

## Commit Message Convention

We follow [Conventional Commits](https://www.conventionalcommits.org/) for
automated changelog generation.

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

**Types:** `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

**Scopes (common):** `int`, `skills`, `oci`, `template`, `cli`, `deps`

**Examples:**

```
feat(int): add --dry-run flag to create

fix(template): handle nested archive paths on extraction

docs(readme): update install instructions for macOS
```

## Pull Request Process

1. **Open a draft PR early** for large or complex changes.
2. **Fill out the PR description** — what changed and why.
3. **Ensure CI passes** — `go test ./...`, `go vet ./...`, and `gofmt` must be green.
4. **Request review** from maintainers.
5. **Address feedback** promptly. Amend commits and force-push to the same branch.

### PR Checklist

- [ ] `make check` passes (fmt, vet, tests)
- [ ] `make ci` passes if you have `golangci-lint` installed (full pipeline)
- [ ] Code is formatted with `gofmt` (or `gofumpt`)
- [ ] `go mod tidy` has been run if imports changed
- [ ] New or changed behavior has tests
- [ ] Command-line changes update help text and README if applicable
- [ ] Commit messages follow the convention above
- [ ] Only relevant files are committed (no `git add .`)
- [ ] No debug code, `fmt.Printf` artifacts, or temporary files

## Code Standards

### Go style

- Follow [Effective Go](https://go.dev/doc/effective_go) and the
  [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).
- Run `make fmt` (or `gofumpt`) before committing.
- Run `make vet` to catch common mistakes.
- Run `make tidy` after adding or removing imports.

### CLI patterns

This project uses Cobra + Viper. When adding or modifying commands:

- Set `SilenceUsage: true` and `SilenceErrors: true` on the command.
- Return errors from `RunE` — never call `os.Exit()` inside commands.
- Write diagnostic output to `cmd.ErrOrStderr()`, program output to `cmd.OutOrStdout()`.
- Bind flags to Viper with `viper.BindPFlag` for env/config support.
- Use `cobra.ExactArgs`, `cobra.MinimumNArgs`, etc. for argument validation.
- Provide `RegisterFlagCompletionFunc` for flag value completion.

### Error handling

- Return wrapped errors with context: `fmt.Errorf("resolving template: %w", err)`
- Use exit code `2` for usage errors (invalid flags/args), `1` for runtime errors.

## Testing

### Run the test suite

```bash
make test
```

Or manually:

```bash
go test ./...
```

### Run with race detection

```bash
make test-race
```

Or manually:

```bash
go test -race ./...
```

### Key testing patterns

- Test commands by executing them programmatically and capturing output.
- Use `cmd.SetOut(buf)` and `cmd.SetErr(errBuf)` so tests can inspect output.
- Mock OCI registry calls and HTTP transports (e.g. the GitHub template client) — do not hit the network or real registries in tests.
- Each `_test.go` file lives alongside the code it tests.

### Adding tests

- New features **must** include tests.
- Bug fixes **should** include a regression test.
- Table-driven tests are preferred for multiple cases.

## Project Conventions

### Project layout

```
cmd/intropy/         Cobra commands — one file per command + tests
internal/template/  Template download, validation, describe, render
internal/skill/      skills.json/lockfile, install/update/add, collection cache
internal/skill/oci/  OCI client wrappers, pack/push/pull, references
```

### Version stamping

Version, commit, and build date are injected at compile time. When testing locally:

```bash
go build -ldflags "\
  -X main.version=dev \
  -X main.commit=$(git rev-parse --short HEAD) \
  -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o bin/intropy ./cmd/intropy
```

### OCI and skills compliance

The `skills` subsystem implements the
[Agent Skills OCI Artifacts Spec](https://github.com/ThomasVitale/agents-skills-oci-artifacts-spec).
When modifying OCI packaging, pulling, or metadata, ensure conformance with the
spec. Changes that affect wire format or artifact structure should be discussed
in an issue first.

## Reporting Issues

Before opening an issue:

1. Search existing issues (open and closed) for duplicates.
2. Use a clear, descriptive title.

A good bug report includes:
- Steps to reproduce
- Expected vs. actual behavior
- Output of `intropy version`
- Environment: OS, Go version (`go version`)
- If applicable: the exact command and flags used

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE).

## Security

If you discover a security vulnerability, **do not open a public issue**.
Email the maintainers directly.

---

Questions? Open a discussion or reach out to the maintainers.
