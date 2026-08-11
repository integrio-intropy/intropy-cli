---
okf_version: "0.2"
---

# Intropy CLI knowledge catalog

This pilot captures knowledge that spans commands: the Intropy resource model,
state ownership, deployment invariants, end-to-end workflows, repository
contracts, and operational playbooks.

Command syntax, flags, and complete invocation details remain authoritative in
`intropy <command> --help`. The repository `README.md` remains the installation
and quickstart entry point.

## Concepts

- [Intropy resource model](concepts/intropy-resource-model.md) - How integrations, systems, components, releases, and environments relate.
- [State and ownership](concepts/state-and-ownership.md) - Which repository or service owns each fact in the Intropy workflow.
- [Releases and image digests](concepts/releases-and-image-digests.md) - Why releases record immutable image digests and promotion copies them unchanged.

## Workflows

- [Integration to production](workflows/integration-to-production.md) - The route from a scaffolded integration to a production deployment.
- [Onboard a system](workflows/onboard-a-system.md) - How a system becomes a reviewable GitOps tree.
- [Manual production gate](workflows/manual-production-gate.md) - How an approver reviews and applies the exact pending revision.

## Contracts

- [GitOps repository contract](contracts/gitops-repository-contract.md) - The repository structure and metadata required by deployment commands.

## Playbooks

- [Deployment troubleshooting](playbooks/deployment-troubleshooting.md) - How to diagnose common deployment and promotion failures without bypassing GitOps.

## Pilot frontmatter profile

Every concept uses the OKF-required `type` field and the recommended `title`,
`description`, and `tags` fields. This pilot also uses:

- `status: draft` until a maintainer verifies the content;
- `sources` for the repository documents or implementation files from which a
  concept is derived; and
- `commands` as a producer extension for command-oriented discovery.

Add `generated` only when the producer identity and generation time are known.
Add `verified` only after a human or process has checked the concept against its
listed sources. Follow the actor convention from the
[OKF v0.2 specification](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md).
