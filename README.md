# Upkeep

Upkeep is a maintenance orchestrator CLI for contributed Drupal modules. From
one "cockpit" directory it tracks every module you maintain across the Drupal
core versions you support, shows every open contribution with its CI and
local-check state on a single dashboard, runs each MR through a fully isolated
check flow (PHPUnit, PHPStan, PHPCS, install, smoke) in a disposable
per-module-per-core environment, and — for compat MRs that pass every gate —
offers a human-approved fast-lane merge.

Environments are provisioned with [ddev](https://ddev.com/) plus the
[ddev-drupal-contrib](https://github.com/ddev/ddev-drupal-contrib) add-on
(pinned at version 1.1.5) behind a thin adapter, and database fixtures come
from the companion [ddev-upkeep](https://github.com/owenbush/ddev-upkeep)
add-on. You never interact with either directly unless you want to.

**[Full documentation →](docs/)**

## Quick start

```bash
# 1. Install from a clone (not yet on Packagist)
git clone https://github.com/owenbush/upkeep.git && cd upkeep && composer install

# 2. Scaffold a cockpit
./bin/upkeep init ~/my-cockpit && cd ~/my-cockpit

# 3. Build the base artifacts for a core version you maintain for
upkeep base-artifacts:build --version=11

# 4. Look at a module — any module, registered or not
upkeep issues pathauto
upkeep dashboard pathauto
```

Reading git.drupalcode.org needs no credential. A
[token](docs/guide/gitlab-token.md) is for writing: merging, commenting and
publishing.

## Documentation

**Getting started**

| | |
| --- | --- |
| [Install](docs/guide/install.md) | Requirements, installing from a clone, shell completion |
| [GitLab token](docs/guide/gitlab-token.md) | What needs a credential and what does not |
| [Set up a cockpit](docs/guide/cockpit.md) | The registry, and what lives in a cockpit directory |
| [Base artifacts](docs/base-artifacts.md) | The per-core building blocks, pre-release core majors, rebuilding |
| [Publishing: issue forks and SSH](docs/guide/publishing.md) | How work reaches drupal.org |

**Daily flow**

| | |
| --- | --- |
| [The dashboard](docs/guide/dashboard.md) | Everything open, and what a row represents |
| [Work on an issue](docs/guide/issue-loop.md) | `issues` → `start` → `publish` |
| [Patch contributions](docs/guide/patches.md) | The work a merge-request view cannot see |
| [Running checks](docs/guide/checks.md) | The isolated flow, and how it matches drupal.org CI |
| [Review, merge and release notes](docs/guide/review-and-merge.md) | Looking at it, merging it, writing it up |
| [Working in an environment](docs/guide/environments.md) | A shell, a path, a running site |
| [Fixtures](docs/guide/fixtures.md) | Check against real database state |
| [Browser UI](docs/guide/ui.md) | The same core behind a local web page |

**Reference**

| | |
| --- | --- |
| [Command reference](docs/commands.md) | Every command, generated from the CLI itself |
| [Exit codes](docs/reference/exit-codes.md) | The 0/1/2 contract |
| [Disk housekeeping](docs/reference/disk.md) | Where the space goes, and reclaiming it |
| [Upgrading a cockpit](docs/reference/upgrading.md) | What a version change expects |

**Design**

| | |
| --- | --- |
| [Design document](docs/contrib-maintainer-design.md) | Why upkeep is shaped the way it is |
| [The dashboard row model](docs/dashboard-row-model.md) | Why a row is `(module, issue, branch)` |
| [Any module, not just yours](docs/any-module.md) | The registry as a watchlist rather than a gate |

## Policy stance

Merges are one human approval per merge request, by Drupal Association policy.
There is deliberately no batch mode and no unattended merge path, and the
browser UI deliberately has no merge button — a button that POSTs an action
name is not the per-MR prompt that earns the approval. See
[Review, merge and release notes](docs/guide/review-and-merge.md).

## License

MIT — see [LICENSE](LICENSE).
