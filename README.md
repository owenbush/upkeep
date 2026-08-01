# Upkeep

Upkeep is a maintenance orchestrator CLI for contributed Drupal modules. From
one "cockpit" directory it tracks every module you maintain across the Drupal
core versions you support, shows all open merge requests with their CI and
local-check state on a single dashboard, runs each MR through a fully isolated
check flow (PHPUnit, PHPStan, PHPCS, install, smoke) in a disposable
per-module-per-core environment, and — for compat MRs that pass every gate —
offers a human-approved fast-lane merge.

Environments are provisioned with [ddev](https://ddev.com/) plus the
[ddev-drupal-contrib](https://github.com/ddev/ddev-drupal-contrib) add-on
(pinned at version 1.1.5) behind a thin adapter, and database fixtures come
from the companion [ddev-upkeep](https://github.com/owenbush/ddev-upkeep)
add-on. You never interact with either directly unless you want to.

## Requirements

- PHP >= 8.2 and Composer
- ddev >= 1.24.10 with a working Docker provider
- A [git.drupalcode.org](https://git.drupalcode.org) personal access token
  (next section)

> **Note for Colima / Docker Desktop users:** environments are bind-mounted
> into the Docker VM, and macOS Docker providers only share your home
> directory by default. The projects root (where environments live) must be
> under `$HOME` — the default `~/.upkeep/projects` already is, so this only
> matters if you point `--projects-root`/`UPKEEP_PROJECTS_ROOT` elsewhere.

## Install

Until the first release is published to Packagist, install from a clone:

```bash
composer install
```

then put `bin/upkeep` on your `PATH` (or call it by path). Once published,
the intended install is `composer global require owenbush/upkeep`.

Sanity check:

```bash
upkeep list --raw
```

## GitLab token (PAT)

Upkeep talks to the Drupal.org GitLab instance (git.drupalcode.org) for MR
listings, pipeline status, and fast-lane merges.

**Getting a token:** sign in at
[git.drupalcode.org](https://git.drupalcode.org) (use your Drupal.org
account), then go to **User Settings → Personal access tokens** and create
one. If GitLab only offers you the fine-grained flow, the permissions Upkeep
needs are: **Repository: read**, **Merge requests: read and write**, **CI/CD:
read**. On the classic scope picker, the `api` scope covers all of it.

**Configuring it** — Upkeep resolves the token in this order:

1. the `UPKEEP_GITLAB_TOKEN` environment variable, if non-empty;
2. the file `~/.config/upkeep/drupal-pat` (or
   `$XDG_CONFIG_HOME/upkeep/drupal-pat`), content trimmed.

For the file route:

```bash
mkdir -p ~/.config/upkeep
touch ~/.config/upkeep/drupal-pat
chmod 600 ~/.config/upkeep/drupal-pat
```

then paste the token into that file (a single line). Upkeep never prints the
token.

Verify connectivity (also useful any time you want a quick look at a
module's open MRs and head pipeline):

```bash
upkeep api:probe conditions_helper
```

## Cockpit setup

The cockpit is a plain directory holding your module registry, the per-core
base artifacts, your shared fixture library, and cached check results. Create
one:

```bash
upkeep init ~/upkeep-cockpit
```

That scaffolds `registry.yml` plus empty `base-artifacts/` and `fixtures/`
directories. Every command finds the cockpit via `--cockpit`, else the
`UPKEEP_COCKPIT` environment variable, else the current directory — exporting
the variable once is the comfortable setup:

```bash
export UPKEEP_COCKPIT=~/upkeep-cockpit
```

Environments live under the projects root, resolved as `--projects-root`,
else `UPKEEP_PROJECTS_ROOT`, else `~/.upkeep/projects` (see the Colima note
above if you move it).

### Register your modules

Edit `registry.yml`. Each entry is a module machine name with its
git.drupalcode.org project path and the list of core versions you maintain it
for:

```yaml
modules:
  conditions_helper:
    project: project/conditions_helper
    core_versions: ["10", "11"]
  field_visibility_conditions:
    project: project/field_visibility_conditions
    core_versions: ["11"]
```

Or skip the hand-editing: `modules:add` lists every `project/` namespace
project your token is a member of on git.drupalcode.org — for a maintainer,
that's your modules — and registers the ones you opt into:

```bash
upkeep modules:add                          # interactive: pick from your memberships
upkeep modules:add token_or field_helper    # non-interactive: register by name
upkeep modules:add --core-versions=10,11    # cores the new entries track (default: 11)
```

Already-registered modules are never offered twice, existing entries are never
overwritten, and the command is read-only against GitLab. Note: writing
regenerates `registry.yml`, so hand-written comments in it do not survive.

Check what is registered:

```bash
upkeep modules
```

### Build base artifacts

Base artifacts are the canonical per-core-version building blocks every
environment starts from: a fully resolved `drupal/recommended-project` tree
plus a clean-install database dump. Build one per core version you maintain
for (each build spins up a throwaway ddev project, installs Drupal, dumps the
database, and tears itself down — a few minutes each):

```bash
upkeep base-artifacts:build --core=11
```

Re-running for an existing core version requires `--force` (a deliberate
rebuild). See what you have:

```bash
upkeep base-artifacts:status
```

## Daily flow

### Dashboard

```bash
upkeep dashboard
```

shows every open MR across all registered modules and tracked core versions,
with the linked drupal.org issue status, upstream CI state, local check
results (from your cached `check` runs), and the fast-lane gate status:

- `READY-AUTO` — a Project Update Bot compat MR with green CI and green local
  checks; eligible for the fast-lane merge prompt.
- `REVIEW` — needs a human look (draft, missing/failed CI, failed local
  checks, or simply not a bot compat MR); listed with its reasons.
- `BLOCKED` — cannot proceed (e.g. merge conflicts).

Filter to one core version with `upkeep dashboard --version=11`.

Remote data (GitLab MRs and drupal.org issues) is cached per module. On
subsequent runs the dashboard loads instantly from cache. To re-fetch:

```bash
upkeep dashboard --refresh            # re-fetch all modules
upkeep dashboard --refresh=widget     # re-fetch one module only
```

Local check results and gate verdicts are always resolved fresh — only the
remote API data is cached. The cache age is shown below the table.

### Check an MR

```bash
upkeep check field_visibility_conditions 2 --version=11
```

runs that MR through the full isolated flow: ensure the (module × core)
environment exists (built from the base artifact on first use), apply the MR
via a Composer path repository, run every check, print the per-check report,
and cache the results where the dashboard reads them.

Flags: `--version=N` selects the target core major (must be tracked by the
module's registry entry; defaults to the first one listed). `--fixture=NAME`
loads a named database fixture before checking (see Fixtures below). Note
that `--version` is *always* the core selector — `upkeep --version` does not
print an application version, by design.

Exit codes are a contract you can script against:

| Code | Meaning |
| ---- | ------- |
| 0 | every check passed |
| 1 | at least one check failed (the run produced a verdict) |
| 2 | infrastructure error — the run never produced a verdict |

### Review an MR in the browser

```bash
upkeep review field_visibility_conditions 2 --version=11
```

applies the MR to the running site in that environment, makes sure the module
is installed, and prints the site URL plus a one-time login URL.

### Fast-lane merge

```bash
upkeep merge --fast-lane
```

walks the current `READY-AUTO` rows **one at a time** and asks for an
explicit per-MR approval; only an approved row is merged, via a single API
call. Rows that are not `READY-AUTO` are shown in a non-actionable summary
and are never offered. Immediately before each merge the MR is re-fetched:
head-SHA drift, a CI regression, a state change, or a new draft marker
demotes the row (with reasons) instead of merging, and the merge call itself
carries the expected head SHA so GitLab rejects races the re-check cannot
see.

**Policy stance.** The Drupal Association permits automation to *prepare*
work but expects merges to be individual human actions. Upkeep enforces that
structurally: one prompt, one approval, one API call per MR. There is no
batch mode, no flag that merges without prompting, and no unattended mode in
v1 — an unattended mode would need explicit DA approval first. `--fast-lane`
is required precisely because the command performs merges; the flag names the
workflow, it never skips approval.

**Degraded path.** If the GitLab instance refuses API merges (the merge
endpoint closed to PATs), an approved row is not lost: Upkeep prints the
exact browser merge URL for that MR and records it as handled manually. You
perform the same single human action, just in the browser.

### Interact with an environment

Once a `check` or `review` has provisioned an environment, you can run
commands in it directly without knowing the project path:

```bash
upkeep exec entity_type_access_conditions -- ddev drush cr
upkeep exec entity_type_access_conditions -- ddev ssh
upkeep exec entity_type_access_conditions --version=10 -- ddev logs
```

Everything after `--` is run with the environment directory as the working
directory. The exit code is passed through, so you can script against it.

To get the bare path (for `cd` or other tools):

```bash
cd $(upkeep env:path entity_type_access_conditions)
upkeep env:path entity_type_access_conditions --version=10
```

Both commands default to the first core version tracked in the registry when
`--version` is omitted.

### Issue status

Most merge requests link to a drupal.org issue (via the `Issue #NNN:` title
convention or the branch name). To view the linked issue's details and open
it in the browser — where you can change the status to RTBC, Needs work,
etc.:

```bash
upkeep issue entity_type_access_conditions 2
```

This extracts the issue number from the MR, fetches the issue from
drupal.org (title, status, priority, version), and opens the drupal.org
issue page in your browser. Use `--no-open` to just print the details
without launching the browser.

The dashboard also shows issue status for each MR in the ISSUE column (e.g.
`#3467675 (review)`), pulled live from the drupal.org API.

> **Note:** the drupal.org API is read-only, so status changes go through
> the browser. The `issue` command gets you there in one step.

### Release notes

```bash
upkeep notes conditions_helper
```

drafts paste-ready Markdown release notes: every MR merged since the module's
last tag, grouped and linked. Takes a registered machine name or a full
project path (e.g. `project/conditions_helper`).

## Fixtures

Checks run against a clean install by default. When a check (or your manual
review) needs real content — configured entities, test users, sample nodes —
load a named fixture with `check --fixture=NAME`. Fixtures are gzipped SQL
dumps managed by the companion **ddev-upkeep** add-on, which Upkeep installs
into every environment automatically; module-local fixtures live in the
module's `tests/fixtures/`, shared ones in the cockpit's `fixtures/`
directory. See the
[ddev-upkeep README](https://github.com/owenbush/ddev-upkeep) for the full
fixture model and the `tests/fixtures/` convention.

> **Until ddev-upkeep is published:** `ddev add-on get owenbush/ddev-upkeep`
> 404s while the repo is private, so point Upkeep at your add-on source
> explicitly — `export UPKEEP_ADDON_SOURCE=/path/to/ddev-upkeep` (a local
> checkout path, or any source `ddev add-on get` accepts). This is temporary
> and disappears at publication.

## Disk housekeeping

```bash
upkeep status
```

summarizes the cockpit, environments, and total tracked disk usage;
`upkeep status --disk` itemizes it per module, core version, and category
(project trees, docker volumes, materialized snapshots, base artifacts,
fixture dumps) with totals.

```bash
upkeep prune --all
```

is a **dry run** — it lists deletion candidates (environment trees, docker
volumes, materialized fixture snapshots) with reclaimable sizes and deletes
nothing until you add `--yes`. Narrow it with `--trees`, `--snapshots`, or
`--projects`, age-gate with `--older-than=30d`, and keep the newest N
snapshots per project with `--keep-latest=N`. Everything pruned regenerates
on demand.

Protected regardless of flags: base artifacts, committed fixture dumps
(`.sql.gz`), and keep-marked items — `touch <project>/.keep` keeps a whole
environment, and an `<artifact>.keep` sibling (e.g.
`materialized/<name>.sql.keep`) keeps a single snapshot. Environments are
always disposed through the engine adapter, so containers and named volumes
are released together with the tree.

## Command reference

| Command | Description |
| ------- | ----------- |
| `upkeep init [<dir>]` | Scaffold a new cockpit: `registry.yml`, `base-artifacts/`, `fixtures/` |
| `upkeep modules` | List the modules registered in the cockpit registry |
| `upkeep modules:add` | Register maintained modules from your git.drupalcode.org memberships (interactive opt-in) |
| `upkeep api:probe <module>` | Probe the GitLab API for a module: open MRs and head pipeline status |
| `upkeep base-artifacts:build --core=N [--force] [--scratch-dir=DIR]` | Build the canonical per-core base artifacts (resolved tree + clean-install dump) |
| `upkeep base-artifacts:status` | List built core versions with dates and sizes |
| `upkeep dashboard [--version=N] [--refresh[=MODULE]]` | All open MRs with CI, local check, and fast-lane status (cached; `--refresh` re-fetches) |
| `upkeep check <module> <mr> [--version=N] [--fixture=NAME]` | Full isolated check flow for one MR; exit 0/1/2 contract |
| `upkeep review <module> <mr> [--version=N]` | Apply an MR to a running site and print its browsable URL |
| `upkeep exec <module> [--version=N] -- <command...>` | Run a command in the module's environment directory |
| `upkeep env:path <module> [--version=N]` | Print the absolute path of a module's environment directory |
| `upkeep issue <module> <mr> [--no-open]` | Show the linked drupal.org issue and open it in the browser |
| `upkeep merge --fast-lane` | Per-MR human-approved merges of READY-AUTO rows only |
| `upkeep notes <module>` | Paste-ready Markdown release notes since the last tag |
| `upkeep status [--disk]` | Cockpit state; `--disk` itemizes measured disk usage |
| `upkeep prune [--trees\|--snapshots\|--projects\|--all] [--older-than=T] [--keep-latest=N] [--yes]` | Reclaim disposable state; dry-run without `--yes` |

Global per-command options: `--cockpit`, and (where environments are
involved) `--projects-root`. Run `upkeep help <command>` for the full text of
any command.

## Design

The architecture — the adapter boundary that keeps engine specifics out of
the orchestrator, the base-artifact cold-start strategy, the fixture model,
and the DA-policy analysis behind the fast lane — is written up in
[docs/contrib-maintainer-design.md](docs/contrib-maintainer-design.md).

## License

MIT.
