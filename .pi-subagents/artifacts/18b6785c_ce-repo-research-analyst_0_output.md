## Repository Research Summary

### Technology & Infrastructure
- **Go CLI**: Go `1.26.2`, Cobra `v1.10.2`; entrypoint `cmd/intropy/main.go`, root command `cmd/intropy/root.go`.
- **Architecture**: single-module repository (`go.mod`), command code in `cmd/intropy/`, domain logic in `internal/`.
- **Kubernetes**: existing `intropy manifests create` generates Kustomize base/overlay trees for individual scaffolded integrations (`cmd/intropy/manifests_create.go`, `internal/deploy/create.go`).
- **No k3s/kubectl/Helm execution layer found**: no k3s-specific files or Go-side command execution implementation was found.
- **Packaging**: GoReleaser produces macOS/Linux binaries plus multi-arch distroless GHCR images (`.goreleaser.yaml`, `Dockerfile`). Release image is the CLI only; it does not include `kubectl`, `kustomize`, Docker, or k3s tooling.

### Architecture & Structure
- Cobra command groups register through package `init()`:
  - `cmd/intropy/sys.go` owns `intropy sys`.
  - `cmd/intropy/sys_create.go` owns `intropy sys create`.
  - Recommended new command shape: `cmd/intropy/sys_deploy.go`, registered beneath existing `sysCmd`.
- Command handlers are intentionally thin: construct options, install signal-aware context, delegate to internal package, and route stdout/stderr through Cobra writers.
- `internal/system/` is the primary seam for system-host semantics:
  - `create.go` scans scaffold records, renders the remote `system-host` template, and emits host metadata via `CreateResult`.
  - `model.go` represents components, topics, connectors, and shared contracts.
  - `assemble.go` validates topology and creates the model.
  - `codegen.go` generates `Topics.cs`, `Connectors.cs`, and the C# `ISystemDefinition`.
- System hosts currently default connectors to local file transports, not cluster infrastructure:
  - `internal/system/codegen.go`: `Transport.File("./test/<connector>")`.
  - Generated extractors default to a one-minute schedule (`"* * * * *"`), a relevant mapping candidate for k3s `CronJob` generation.

### Existing Deploy/Configuration Assets
- `intropy manifests create` is **per integration**, not per assembled system:
  - Finds `.intropy/scaffold.json` by walking upward.
  - Fetches the pinned integration template from GitHub.
  - Renders `manifests/skeleton/` into `<project>/deploy` by default.
  - Supports `--set`, repeatable `--values`, `--no-input`, `--force`, `--version`, and `--output-json`.
  - Files: `cmd/intropy/manifests_create.go`, `internal/deploy/create.go`.
- The generated shape is expected to be Kustomize:
  - README documents `deploy/base` and `deploy/overlays/{dev,prod}` and recommends `kustomize build`.
  - Deploy fixtures demonstrate Deployment, Service, Dapr annotations, and Kustomize image overrides: `internal/deploy/create_test.go`.
- Scaffold metadata is the durable system/integration contract:
  - `.intropy/scaffold.json` records template source/version and values.
  - System creation has a stable JSON output document containing components, topics, connectors, and shared-library path (`internal/system/create.go`).
- No persistent CLI config mechanism is implemented in the reviewed command code. **Moderate documentation discrepancy**: `CONTRIBUTING.md` says “Cobra + Viper” and directs flag binding to Viper, but `go.mod` has no Viper dependency and reviewed commands bind flags directly to Cobra.

### Recommended Implementation Seams
1. **CLI boundary — `cmd/intropy/sys_deploy.go`**
   - Add `sys deploy` under `sysCmd`.
   - Follow `sys create` conventions: `RunE`, `signal.NotifyContext`, no direct `os.Exit`, `cmd.OutOrStdout()`/`cmd.ErrOrStderr()`, typed usage errors where appropriate.
   - Suggested flags: host/workspace path, `--kubeconfig`, `--context`, namespace, overlay/environment, image override(s), `--dry-run`, `--wait`, and machine-readable `--output-json`.

2. **Domain boundary — new `internal/system/deploy.go` or `internal/systemdeployment/`**
   - Keep Cobra isolated from deployment orchestration.
   - Reuse `template.ListScaffolds`/`system.Assemble` for topology validation where deployment needs component/connector knowledge.
   - Define dependency-injected interfaces for Kubernetes application and image build/push operations so tests never require a real cluster.

3. **Manifest composition**
   - Reuse `internal/deploy.Create` only for the individual integration manifest-generation stage; it is not currently a multi-component system deploy API.
   - A system deployer should first ensure each integration has rendered manifests, then compose them into a system-level Kustomize overlay and apply it.
   - The host’s local `Transport.File` connector defaults are unsuitable for k3s. System deployment needs an explicit connector-to-cluster-transport policy (Dapr components/bindings, broker, PVC, or an explicit unsupported error).

4. **Cluster application**
   - Prefer a Go Kubernetes client or a narrow injected command runner rather than shelling from Cobra.
   - If executing `kubectl`, account for its absence from released distroless images. The current containerized CLI cannot run sibling local `kubectl` binaries.

5. **Image lifecycle**
   - Individual manifests require `imageRepository` and image tags (`internal/deploy/create_test.go`); `sys deploy` must define how local source directories become images and how k3s can access them.
   - For local k3s, document/implement one explicit strategy: import into containerd, push to a local registry, or require prebuilt remotely accessible images.

### Tests & Documentation Conventions
- Tests live beside code; table-driven tests and mocked HTTP/OCI are required conventions (`CONTRIBUTING.md`).
- Relevant existing test seams:
  - CLI flag/argument behavior: `cmd/intropy/sys_create_test.go`.
  - System assembly and generated system-host output: `internal/system/create_test.go`, `internal/system/assemble_test.go`.
  - Manifest rendering and Kustomize expectations: `internal/deploy/create_test.go`.
- Required validation per contribution guidance: `make check`; CI runs SPA build, `go build -v ./...`, `go test -race -v ./...`, `go vet ./...`, and `gofmt` checks (`Makefile`, `.github/workflows/test.yaml`).
- README command docs must be updated for CLI behavior changes (`CONTRIBUTING.md`).
- No GitHub issue or PR templates were found under `.github/`; only `workflows/test.yaml` and `workflows/release.yaml` are present.

### Review Findings
- **blocker — deployment semantics unresolved**: `internal/system/codegen.go` hardcodes file-based local connectors. A k3s deployment cannot safely infer a cluster transport from these paths.
- **high — no image distribution contract**: generated manifests use container images, but repository code has no image build/push/import workflow. k3s nodes will not automatically access local images.
- **high — deploy scope mismatch**: `internal/deploy/create.go` handles one scaffolded integration; it cannot directly deploy/compose a system host and all component projects.
- **medium — packaged CLI lacks cluster tools**: `Dockerfile` is distroless and `.goreleaser.yaml` ships a static CLI. A subprocess-based `kubectl` implementation will not work inside the published image without a packaging redesign.
- **medium — documentation contradiction**: `CONTRIBUTING.md` references Viper patterns absent from `go.mod` and reviewed commands; do not introduce Viper solely to follow stale prose without a project decision.