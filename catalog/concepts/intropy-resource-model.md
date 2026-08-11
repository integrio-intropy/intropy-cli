---
type: Intropy Concept
title: Intropy resource model
description: How integrations, systems, components, releases, and environments relate.
tags: [intropy, concepts, lifecycle]
status: draft
commands:
  - intropy int create
  - intropy sys create
  - intropy release create
  - intropy deploy pin
sources:
  - id: cli-readme
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/README.md
    title: intropy CLI README
  - id: topology-model
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/internal/topology/topology.go
    title: Intropy topology model
---

# Purpose

<!-- Explain the user problem this model solves without describing Go packages. -->

# Concepts

| Concept | Meaning | Identity | Lifecycle |
| --- | --- | --- | --- |
| Integration | <!-- Define the independently scaffolded integration. --> | <!-- Describe its stable name or record. --> | <!-- Describe creation and change. --> |
| System | <!-- Define the assembled topology. --> | <!-- Describe domain and system identity. --> | <!-- Describe assembly and onboarding. --> |
| Component | <!-- Define the deployable or shared GitOps unit. --> | <!-- Describe its GitOps coordinates. --> | <!-- Describe onboarding and deployment. --> |
| Release | <!-- Define the immutable published record. --> | <!-- Describe component and SemVer identity. --> | <!-- Describe publication and retention. --> |
| Environment | <!-- Define a deployment target in the promotion graph. --> | <!-- Describe its repository key. --> | <!-- Describe pin, promote, and sync states. --> |

# Relationships

<!-- Describe how scaffold records become a system topology, how topology becomes
GitOps components, and how releases move through environments. Link to the
workflow concepts rather than repeating their steps. -->

# Invariants

<!-- Record stable truths an agent must preserve when recommending actions. -->

# Examples

<!-- Add one small example using a realistic domain, system, and component. -->

# Related knowledge

- [State and ownership](/concepts/state-and-ownership.md)
- [Releases and image digests](/concepts/releases-and-image-digests.md)
- [Integration to production](/workflows/integration-to-production.md)
