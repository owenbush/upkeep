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

- [The dashboard](guide/dashboard.md) — everything open, and what a row represents
- [Work on an issue](guide/issue-loop.md) — `issues` → `start` → `publish`
- [Patch contributions](guide/patches.md) — the work a merge-request view cannot see
- [Running checks](guide/checks.md) — the isolated flow, and how it matches drupal.org CI
- [Review, merge and release notes](guide/review-and-merge.md)
- [Working in an environment](guide/environments.md) — a shell, a path, a running site
- [Fixtures](guide/fixtures.md) — check against real database state
- [Browser UI](guide/ui.md) — the same core behind a local web page

## Reference

- [Command reference](commands.md) — every command, generated from the CLI itself
- [Exit codes](reference/exit-codes.md) — the 0/1/2 contract
- [Disk housekeeping](reference/disk.md) — where the space goes, and reclaiming it
- [Upgrading a cockpit](reference/upgrading.md)

## Design

Why upkeep is shaped the way it is, rather than how to use it.

- [Design document](contrib-maintainer-design.md) — the problem, the adapter boundary, cold starts, the fast lane and the DA policy behind it
- [The dashboard row model](dashboard-row-model.md) — why a row is `(module, issue, branch)` and core is evidence, not identity
- [Any module, not just yours](any-module.md) — the registry as a watchlist rather than a gate
- [Base artifacts](base-artifacts.md) — lifecycle, staged rebuilds, pre-release core majors
