---
type: Intropy Concept
title: State and ownership
description: Which repository or service owns each fact in the Intropy workflow.
tags: [intropy, concepts, gitops, ownership]
status: draft
commands:
  - intropy int show
  - intropy deploy status
  - intropy release show
sources:
  - id: cli-readme
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/README.md
    title: intropy CLI README
  - id: project-layout
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/CONTRIBUTING.md
    title: intropy CLI project layout
  - id: gitops-remote
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/internal/gitops/remote.go
    title: GitOps remote resolution
---

# Purpose

<!-- Explain why agents and operators must know which system is authoritative
before reading or changing state. -->

# Ownership map

| Fact | Authoritative location | Written by | Read by |
| --- | --- | --- | --- |
| Integration scaffold inputs | <!-- Source or scaffold record. --> | <!-- Actor or command. --> | <!-- Consumers. --> |
| System topology | <!-- Host topology record. --> | <!-- Actor or command. --> | <!-- Consumers. --> |
| Release metadata | <!-- OCI release manifest. --> | <!-- Actor or command. --> | <!-- Consumers. --> |
| Desired deployment | <!-- GitOps overlay. --> | <!-- Actor or command. --> | <!-- Consumers. --> |
| Applied revision and health | <!-- ArgoCD. --> | <!-- Controller. --> | <!-- Consumers. --> |
| Installed agent skills | <!-- Project manifest and lock/cache. --> | <!-- Actor or command. --> | <!-- Consumers. --> |

# Precedence rules

<!-- Capture cross-command configuration and discovery precedence only where it
changes which source wins. Leave individual flag descriptions in CLI help. -->

# Boundaries

<!-- Explain what the CLI deliberately does not infer, own, or mutate. -->

# Related knowledge

- [Intropy resource model](/concepts/intropy-resource-model.md)
- [GitOps repository contract](/contracts/gitops-repository-contract.md)
- [Deployment troubleshooting](/playbooks/deployment-troubleshooting.md)
