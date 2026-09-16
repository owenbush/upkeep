# Command reference

<!-- GENERATED FILE. Run `go run ./cmd/docgen` to regenerate; do not edit by hand. -->

Every command upkeep ships, generated from the CLI itself so it cannot drift from what
the binary actually does. `go run ./cmd/docgen --check` fails the build when this file
and the commands disagree.

Conventions worth knowing before the list:

- **`--version` is always the target Drupal core major**, never an application version.
  The application-level `-V` is deliberately removed; `upkeep version` prints it.
- **Exit codes are a contract**: `0` the command did what was asked, `1` the work it
  supervised failed, `2` upkeep could not do the job.
- **Reading needs no credential.** A token is required only to merge, comment or publish.

| Command | What it does |
| --- | --- |
| [`api:probe`](#upkeep-apiprobe) | Probe the git.drupalcode.org API for a module: open MRs and head pipeline status |
| [`base-artifacts:build`](#upkeep-base-artifactsbuild) | Build the canonical per-core base artifacts: resolved base tree and clean-install DB dump |
| [`base-artifacts:status`](#upkeep-base-artifactsstatus) | List which core versions have base artifacts, with build dates and sizes |
| [`check`](#upkeep-check) | Run one merge request through the full isolated check flow |
| [`dashboard`](#upkeep-dashboard) | Per-module overview of everything open; name a module for its rows |
| [`dev`](#upkeep-dev) | Prepare an environment for active development |
| [`env:path`](#upkeep-envpath) | Print the absolute path of a module's environment directory |
| [`exec`](#upkeep-exec) | Run a command in a module's environment directory |
| [`explain`](#upkeep-explain) | Explain a term from upkeep's output |
| [`init`](#upkeep-init) | Scaffold a new cockpit |
| [`issue`](#upkeep-issue) | Show and open the drupal.org issue linked to a merge request |
| [`issues`](#upkeep-issues) | List a module's open drupal.org issues and what has been contributed to each |
| [`merge`](#upkeep-merge) | Fast-lane merge: prompt per READY-AUTO merge request |
| [`modules`](#upkeep-modules) | List the modules registered in the cockpit module registry |
| [`modules:add`](#upkeep-modulesadd) | Register maintained modules from your git.drupalcode.org project memberships |
| [`needs-work`](#upkeep-needs-work) | Post local check results as a comment on the merge request |
| [`notes`](#upkeep-notes) | Draft paste-ready Markdown release notes: merged MRs since the module's last tag |
| [`patch:apply`](#upkeep-patchapply) | Download a patch from a drupal.org issue and apply it in the module environment |
| [`patch:check`](#upkeep-patchcheck) | Run one drupal.org patch through the full isolated check flow |
| [`patch:promote`](#upkeep-patchpromote) | Apply a drupal.org patch onto an issue work branch, credited to its author |
| [`patches`](#upkeep-patches) | Show drupal.org issues carrying patch files, and how they relate to merge requests |
| [`prune`](#upkeep-prune) | Reclaim disposable state; dry-run unless --yes |
| [`publish`](#upkeep-publish) | Push an issue's work branch and open a merge request for it |
| [`review`](#upkeep-review) | Apply a merge request to a running site and print its browsable URL |
| [`start`](#upkeep-start) | Start (or resume) work on a drupal.org issue |
| [`status`](#upkeep-status) | Report cockpit state; --disk itemizes real measured disk usage |
| [`version`](#upkeep-version) | Print the upkeep version |

## `upkeep api:probe`

What GitLab actually says about a module — for when a row looks wrong and
the question is whether upkeep misread the API or the API said something
surprising.

  upkeep api:probe pathauto
  upkeep api:probe project/conditions_helper

Takes the name or the full path, consults no registry, and only ever reads.

```
upkeep api:probe <module>
```

## `upkeep base-artifacts:build`

Builds the artifact set every environment for a core is seeded from.

  upkeep base-artifacts:build --version=11
  upkeep base-artifacts:build --version=12 --stability=alpha
  upkeep base-artifacts:build --version=11 --force

A rebuild is staged beside the live set and swapped in at the end, so a
build that fails costs the attempt and leaves the existing set untouched.

```
upkeep base-artifacts:build [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--force` | Deliberately rebuild over an existing artifact set |
| `--scratch-dir=SCRATCH-DIR` | Directory for the throwaway site-install project (must be a path your container runtime mounts, e.g. under your home directory) Default: `~/.upkeep/scratch`. |
| `--stability=STABILITY` | Lowest release stability to accept (dev, alpha, beta, RC, stable). Needed while a core major is still in alpha or beta, which is when compatibility work happens |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Drupal core major version to build artifacts for (e.g. 11) |

## `upkeep base-artifacts:status`

List which core versions have base artifacts, with build dates and sizes

```
upkeep base-artifacts:status [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep check`

Runs one merge request through the full isolated flow: provision the
(module x core) environment, apply the MR, run every check, and cache the
result where the dashboard and the fast-lane gate read it.

  upkeep check pathauto 12
  upkeep check pathauto 12 --version=11
  upkeep check pathauto 12 --fixture=sample-content

Or check what you are working on right now, whatever branch that is — after
upkeep start, or upkeep patch:promote, or your own edits:

  upkeep check pathauto --working-copy

That mode needs no merge request and no GitLab token, and caches nothing: a
working copy has no revision the dashboard could match a verdict against.

Exits 0 all green, 1 a check failed, 2 it could not run at all. Needs base
artifacts for that core: upkeep base-artifacts:build --version=11.

```
upkeep check <module> [mr] [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--fixture=FIXTURE` | Load this named fixture into the database before running checks |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |
| `--working-copy` | Check the module working copy as it stands instead of a merge request (caches nothing) |

## `upkeep dashboard`

Where to start. With no arguments it summarises every watched module — how
many merge requests and patch issues are open, how many are ready, and how many
have never been checked.

Name a module to see its individual rows. Each row carries a NEXT column with
the command to run for it.

  upkeep dashboard                    every module, one line each
  upkeep dashboard pathauto           that module's rows, with what to do about each
  upkeep dashboard pathauto --refresh re-fetch first (goes to the network)
  upkeep dashboard --all              every row of every module
  upkeep dashboard pathauto -v        the gate's own reason tokens

Cached by default, so repeat runs are instant.
upkeep explain <term> defines any column or status.

```
upkeep dashboard [module] [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--all` | Every row of every module, rather than the per-module overview |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--no-patches` | Omit patch rows: only merge requests, as the dashboard showed before patch contributions were included |
| `--refresh=REFRESH` | Re-fetch remote data: --refresh for all modules, --refresh=<module> for one |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Only show rows targeting this core major version (e.g. 11) |

## `upkeep dev`

Prepare an environment for active development: provision it if needed, optionally check out a branch, and print where it is.

```
upkeep dev <module> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--branch=BRANCH` | Branch to check out in the module working copy |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep env:path`

Print the absolute path of a module's environment directory.

stdout carries the path and nothing else, so `cd $(upkeep env:path pathauto)` works.

```
upkeep env:path <module> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep exec`

Run a command in a module's environment directory.

Exit codes follow the CLI-wide contract rather than the child's raw code: 0 the command succeeded, 1 it exited non-zero, 2 upkeep could not run it — an unresolvable module, an untracked core, no provisioned environment, or a child that never started.

stdout belongs to the wrapped command; upkeep's own words go to stderr.

```
upkeep exec <module> -- <command>... [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep explain`

Explain a term from upkeep's output. Run it bare to list every term.

Matches on the term and on its meaning, so a half-remembered word still finds it.

```
upkeep explain [term]
```

## `upkeep init`

Scaffold a new cockpit: module registry, base-artifacts/, fixtures/, and projects/ directories.

```
upkeep init [dir]
```

## `upkeep issue`

Show and open the drupal.org issue linked to a merge request

```
upkeep issue <module> <mr> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--no-open` | Do not open the drupal.org issue in the browser |

## `upkeep issues`

Every open issue on a module, whether or not anyone has contributed to it —
the difference from dashboard and patches, which both start from a
contribution.

  upkeep issues pathauto              every open issue
  upkeep issues pathauto --unclaimed  only what nobody has started
  upkeep issues pathauto --status=active

The merge-request column is filled from the dashboard cache, so run
upkeep dashboard --refresh=<module> if it is empty.
To begin work on one: upkeep start <module> <issue>.

```
upkeep issues <module> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--status=STATUS` | Only this status: active, review, needs-work, rtbc, postponed (default: every open status) |
| `--unclaimed` | Only issues nobody has contributed to yet — the work that has not started |

## `upkeep merge`

Walks the current READY-AUTO rows one at a time, asking for an explicit
approval per merge request.

  upkeep merge --fast-lane

Only bot compatibility MRs with green CI and green local checks are ever
offered. There is no batch mode and no unattended flag, by Drupal Association
policy — see the README's policy stance.

```
upkeep merge --fast-lane [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--fast-lane` | Required: run the fast-lane loop (the only mode; named explicitly because it performs merges) |

## `upkeep modules`

List the modules registered in the cockpit module registry

```
upkeep modules [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep modules:add`

Lists the projects you are a member of on git.drupalcode.org and registers
the ones you choose, so the survey commands cover them.

  upkeep modules:add                    pick from your memberships
  upkeep modules:add token pathauto     register these without prompting
  upkeep modules:add --core-versions=10,11

Reads GitLab and writes registry.yml, and nothing else.

```
upkeep modules:add [module...] [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--core-versions=CORE-VERSIONS` | Comma-separated core majors the new entries track (e.g. "10,11") Default: `11`. |

## `upkeep needs-work`

Posts the cached check results for a merge request as a comment, then opens
its drupal.org issue so the status can be set to Needs work.

  upkeep check pathauto 42        record the results
  upkeep needs-work pathauto 42   publish them

Posts what was recorded; it never runs the checks itself. --dry-run prints
the comment without posting it.

```
upkeep needs-work <module> <mr> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--dry-run` | Print the comment without posting it |
| `--no-open` | Do not open the drupal.org issue in the browser |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep notes`

Drafts release notes for a module — everything merged since its last tag,
as Markdown on stdout ready to paste into a release.

  upkeep notes pathauto
  upkeep notes project/conditions_helper > notes.md

Drafts; it never tags and never cuts a release. The module is resolved
through the cockpit registry when there is one, and taken as a project path
when there is not.

```
upkeep notes <module> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep patch:apply`

Download a patch from a drupal.org issue and apply it in the module environment.

`patch:check` is the same resolution and apply followed by the full suite.

stdout carries the environment path and nothing else, so it composes:
  cd $(upkeep patch:apply widget 3597808)

```
upkeep patch:apply <module> <issue> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--file=FILE` | Apply this exact attachment by filename instead of choosing (e.g. 3597808-9-d11.patch) |
| `--latest` | Take the newest patch without asking, even when the issue carries several |
| `--no-update` | Do not fetch the base branch first; check against the working copy's base as it stands |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--url=URL` | Fetch the patch from this URL instead of the issue's attachments (a fork, a re-roll posted elsewhere) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep patch:check`

The patch-side counterpart of check: downloads a patch from a drupal.org
issue, applies it onto a branch off the base, and runs the full suite.

  upkeep patch:check pathauto 3597857
  upkeep patch:check pathauto 3597857 --latest   take the newest patch without asking
  upkeep patch:check pathauto 3597857 --file=NAME

With several patches on the issue and no terminal to ask at, the newest is
taken, and said so.

Exits 0 all green, 1 a check failed, 2 it could not run at all — which
includes a patch that does not apply, because no verdict on the contribution
was produced.

```
upkeep patch:check <module> <issue> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--file=FILE` | Apply this exact attachment by filename instead of choosing (e.g. 3597808-9-d11.patch) |
| `--fixture=FIXTURE` | Load this named fixture into the database before running checks |
| `--latest` | Take the newest patch without asking, even when the issue carries several |
| `--no-update` | Do not fetch the base branch first; check against the working copy's base as it stands |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--url=URL` | Fetch the patch from this URL instead of the issue's attachments (a fork, a re-roll posted elsewhere) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep patch:promote`

Converts a patch contribution into a branch you can open a merge request from.

  upkeep patch:promote pathauto 3597857
  upkeep patch:promote pathauto 3597857 --latest
  upkeep patch:promote pathauto 3597857 --file=NAME
  upkeep patch:promote pathauto 3597857 --partial

The commit credits whoever posted the patch, by name, in the message —
promoting moves somebody else's work into history, and the commit is the
durable record of whose it is. Nothing is pushed: run upkeep publish
afterwards, which is the step that puts it on drupal.org.

Afterwards, upkeep check <module> --working-copy runs the suite against the
branch it made, and upkeep dev <module> prints the site URL. A patch that only
applied with reduced context is a weaker guarantee than a merge request
implies, so checking before you publish is worth the minutes.

--partial is for a patch that will not apply at all. Every hunk that still fits
lands on the branch and the rest is left as <file>.rej beside the file it could
not change, which is where a re-roll starts. Nothing is committed — the commit
carries the patch author's name, and half their patch is not what they wrote —
so resolve the rejects, delete the .rej files, commit, and publish. It exits 1,
because the patch did not apply.

```
upkeep patch:promote <module> <issue> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--branch=BRANCH` | Work branch to promote onto (default: the drupal.org <nid>-<slug> convention) |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--file=FILE` | Apply this exact attachment by filename instead of choosing (e.g. 3597808-9-d11.patch) |
| `--latest` | Take the newest patch without asking, even when the issue carries several |
| `--no-update` | Do not fetch the base branch first; check against the working copy's base as it stands |
| `--partial` | When the patch will not apply, keep the hunks that still fit and leave the rest as .rej files to resolve by hand — the start of a re-roll rather than a refusal |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--url=URL` | Fetch the patch from this URL instead of the issue's attachments (a fork, a re-roll posted elsewhere) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep patches`

Issues carrying patch files, and how each relates to a merge request — the
contributions the MR-centric dashboard cannot see.

  upkeep patches                  every registered module
  upkeep patches --module=pathauto
  upkeep patches --without-mr     only what no branch carries

To check one: upkeep patch:check <module> <issue>. An MR shown as "empty"
carries no commits, so any patch beside it is the only work there is —
see upkeep explain "empty MR".

```
upkeep patches [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--module=MODULE` | Only scan this module (default: all registered modules) |
| `--without-mr` | Only issues no merge request carries — omit those where a patch sits alongside a real MR |

## `upkeep prune`

Reclaim disposable state (environment trees, engine projects, materialized
snapshots). Dry-run by default: without --yes the command only lists deletion
candidates with their reclaimable sizes.

Protected regardless of any flag combination:

  * base artifacts (<cockpit>/base-artifacts/) — canonical, expensive to rebuild
  * committed fixture dumps (module tests/fixtures/*.sql.gz, <cockpit>/fixtures/*.sql.gz)
  * keep-marked items: touch <project>/.keep to keep a whole environment, or
    <artifact>.keep (e.g. materialized/<name>.sql.keep) to keep one snapshot.

Environments are always disposed through the engine adapter (containers and
named volumes released with the tree); pruned state regenerates on demand.

```
upkeep prune --trees|--snapshots|--projects|--all [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--all` | Prune everything disposable: trees, volumes, and snapshots |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--keep-latest=KEEP-LATEST` | Snapshots: keep this many newest snapshots per project regardless of age Default: `0`. |
| `--older-than=OLDER-THAN` | Only prune items unused for at least this long (e.g. 30d, 12h); items of unknown age are then excluded |
| `--projects` | Prune whole engine projects: trees plus their named volumes, via the adapter teardown |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--snapshots` | Prune materialized fixture snapshots (their committed .sql.gz dumps can rebuild them at any time) |
| `--trees` | Prune disposable environment trees (disposed via the adapter teardown, which also releases the engine project) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--yes` / `-y` | Actually delete. Without this flag the command is a dry run and deletes NOTHING |

## `upkeep publish`

Pushes the work branch for an issue and opens its merge request — after which
it is an ordinary MR that dashboard, check and merge already handle.

  upkeep publish pathauto 3223746
  upkeep publish pathauto 3223746 --draft

Opens merge requests; never merges one. Re-running after more commits updates
the existing MR rather than opening a second.

```
upkeep publish <module> <issue> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--branch=BRANCH` | Publish this branch instead of deriving it |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--draft` | Open it as a draft |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--target=TARGET` | Branch to merge into (default: the base the work branch was started from) |
| `--title=TITLE` | Merge request title (default: from the issue) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep review`

Apply a merge request to a running site and print its browsable URL.

Runs no checks — this is for looking at the change in a browser. Use `upkeep check` for a verdict.

```
upkeep review <module> <mr> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep start`

Begins work on an issue nobody has contributed to yet: provisions the
environment and opens a branch named to drupal.org's issue-fork convention, so
the merge request that follows is linked to the issue.

  cd $(upkeep start pathauto 3223746)
  upkeep start pathauto 3223746 --version=11

Resumes rather than restarts: an existing branch is checked out as it stands,
and nothing here ever resets or discards.
When the work is ready: upkeep publish <module> <issue>.

```
upkeep start <module> <issue> [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--base=BASE` | Branch to start from (default: whatever the module working copy is currently on) |
| `--branch=BRANCH` | Name the branch yourself instead of deriving it from the issue title |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--no-update` | Do not fetch the base branch first; check against the working copy's base as it stands |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |
| `--verbose` / `-v` | Show every line of the engine's own output rather than the latest |
| `--version=VERSION` | Target core major version; must be one the module tracks. Defaults to the first core version listed for it. |

## `upkeep status`

Report cockpit state.

--disk itemizes real measured disk usage per module, core version and category: project trees, materialized snapshots, engine volumes, base artifacts and fixture dumps, with totals.

```
upkeep status [flags]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--disk` | Itemize disk usage (project trees, materialized snapshots, engine volumes, base artifacts, fixture dumps) with totals |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then <cockpit>/projects/ if it exists, then ~/.upkeep/projects) |

## `upkeep version`

Print the upkeep version

```
upkeep version
```
