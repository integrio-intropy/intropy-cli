---
type: Intropy Contract
title: GitOps repository contract
description: The repository structure and metadata required by deployment commands.
tags: [intropy, contract, gitops, kustomize, argocd]
status: draft
commands:
  - intropy deploy init
  - intropy deploy pin
  - intropy deploy promote
  - intropy deploy diff
  - intropy deploy sync
  - intropy deploy status
sources:
  - id: gitops-config
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/internal/gitops/config.go
    title: GitOps repository configuration model
  - id: gitops-layout
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/internal/gitops/layout.go
    title: GitOps repository layout
  - id: cli-readme
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/README.md
    title: intropy CLI README
---

# Scope

<!-- Define which repository assumptions are stable contracts for users and
which remain implementation details. -->

# Root configuration

<!-- Describe deploy.yaml semantically: registry, ArgoCD coordinates, environment
policies, promotion edges, and platform selection. Link to a schema when one
exists rather than reproducing every field. -->

# Component coordinates

```text
domains/<domain>/<system>/<component>/
├── component.yaml
├── base/
└── overlays/<environment>/
```

<!-- Explain how names are derived and disambiguated. -->

# Component metadata

<!-- Describe the role of component.yaml, including source paths, image names,
component kind, and enabled environments. -->

# Overlay metadata

<!-- Explain digest pins and provenance annotations as machine-readable facts.
Name compatibility expectations for changes to these keys. -->

# Commit metadata

<!-- Explain deployment commit trailers, their consumers, and why renaming a key
is a compatibility change. -->

# Validation rules

<!-- List repository conditions commands verify before writing or syncing. -->

# Related knowledge

- [State and ownership](/concepts/state-and-ownership.md)
- [Onboard a system](/workflows/onboard-a-system.md)
- [Manual production gate](/workflows/manual-production-gate.md)
