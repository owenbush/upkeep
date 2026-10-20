# Upkeep documentation

Upkeep is a maintenance orchestrator CLI for contributed Drupal modules. It
tracks every module you maintain across the core versions you support, shows
every open contribution with its CI and local-check state, runs each one
through a fully isolated check flow, and offers a human-approved fast-lane
merge for the ones that pass every gate.

New here? [Install](guide/install.md), then [set up a
cockpit](guide/cockpit.md), then [build base artifacts](base-artifacts.md) for
a core version. After that, `upkeep issues <module>` works on any Drupal
module — registered or not.

## Getting started

- [Install](guide/install.md) — requirements, installing from a clone, shell completion
- [GitLab token](guide/gitlab-token.md) — what needs a credential and what does not
- [Set up a cockpit](guide/cockpit.md) — the registry, and what a cockpit holds
- [Base artifacts](base-artifacts.md) — the per-core building blocks, pre-release majors, rebuilding
- [Publishing: issue forks and SSH](guide/publishing.md) — how work reaches drupal.org

## Daily flow

- [Which command do I want?](guide/which-command.md) — thirty commands by what you are trying to do, what each changes, and what can lose work
- [The dashboard](guide/dashboard.md) — everything open, and what a row represents
- [Work on an issue](guide/issue-loop.md) — `issues` → `start` → `publish`
- [Work on somebody else's MR](guide/taking-over-an-mr.md) — `checkout` → `check --working-copy` → `publish`
- [Patch contributions](guide/patches.md) — the work a merge-request view cannot see
- [Running checks](guide/checks.md) — the isolated flow, and how it matches drupal.org CI
- [Review, merge and release notes](guide/review-and-merge.md)
- [Working in an environment](guide/environments.md) — a shell, a path, a running site
- [Fixtures](guide/fixtures.md) — check against real database state
- [Agents](guide/agents.md) — letting an AI agent drive upkeep, and where it stops

## Reference

- [Command reference](commands.md) — every command, generated from the CLI itself
- [Exit codes](reference/exit-codes.md) — the 0/1/2 contract
- [Disk housekeeping](reference/disk.md) — where the space goes, and reclaiming it
- [Upgrading a cockpit](reference/upgrading.md)
- [Structured output](reference/json.md) — `--json`, the envelope, and the exit-code contract
- [Releasing upkeep](reference/releasing.md) — tagging, the secrets it needs, signing and notarization
