# Command reference

<!-- GENERATED FILE. Run `composer docs:commands` to regenerate; do not edit by hand. -->

Every command upkeep ships, generated from the CLI itself so it cannot drift from what
the binary actually does. `composer docs:check` fails the build when this file and the
commands disagree.

Conventions worth knowing before the list:

- **`--version` is always the target Drupal core major**, never an application version.
  The application-level `-V` is deliberately removed.
- **Exit codes are a contract**: `0` the command did what was asked, `1` the work it
  supervised failed, `2` upkeep could not do the job.
- **Reading needs no credential.** A token is required only to merge, comment or publish.

| Command | What it does |
| --- | --- |
| [`api:probe`](#upkeep-apiprobe) | Probe the git.drupalcode.org GitLab API for a module: open MRs and head pipeline status. |
| [`base-artifacts:build`](#upkeep-base-artifactsbuild) | Build the canonical per-core-version base artifacts: resolved base tree + clean-install DB dump. |
| [`base-artifacts:status`](#upkeep-base-artifactsstatus) | List which core versions have base artifacts, their build dates and sizes. |
| [`check`](#upkeep-check) | Run one merge request through the full isolated check flow and cache the per-check results. |
| [`completion`](#upkeep-completion) | Dump the shell completion script |
| [`dashboard`](#upkeep-dashboard) | Per-module overview of everything open; name a module to see its rows and what to run next. |
| [`dev`](#upkeep-dev) | Prepare an environment for active development: provision if needed, optionally check out a branch, and print the path. |
| [`env:path`](#upkeep-envpath) | Print the absolute path of a module's environment directory. |
| [`exec`](#upkeep-exec) | Run a command in a module's environment directory. |
| [`explain`](#upkeep-explain) | Explain a term from upkeep's output (run bare to list them all). |
| [`init`](#upkeep-init) | Scaffold a new cockpit: module registry, base-artifacts/, fixtures/, and projects/ directories. |
| [`issue`](#upkeep-issue) | Show and open the drupal.org issue linked to a merge request. |
| [`issues`](#upkeep-issues) | List a module's open drupal.org issues and what has been contributed to each. |
| [`merge`](#upkeep-merge) | Fast-lane merge: prompt per READY-AUTO merge request and merge only on an explicit per-MR approval. |
| [`modules`](#upkeep-modules) | List the modules registered in the cockpit module registry. |
| [`modules:add`](#upkeep-modulesadd) | Register maintained modules from your git.drupalcode.org project memberships (interactive opt-in). |
| [`needs-work`](#upkeep-needs-work) | Post local check results as a comment on the merge request. |
| [`notes`](#upkeep-notes) | Draft paste-ready Markdown release notes: merged MRs since the module's last tag. |
| [`patch:apply`](#upkeep-patchapply) | Download a patch from a drupal.org issue and apply it in the module environment. |
| [`patch:check`](#upkeep-patchcheck) | Run one drupal.org patch through the full isolated check flow. |
| [`patch:promote`](#upkeep-patchpromote) | Apply a drupal.org patch onto an issue work branch, credited to its author, ready to publish. |
| [`patches`](#upkeep-patches) | Show drupal.org issues carrying patch files, and how they relate to merge requests. |
| [`prune`](#upkeep-prune) | Reclaim disposable state (environment trees, engine projects, materialized snapshots). Dry-run by default; never touches base artifacts, keep-marked items, or committed fixture dumps. |
| [`publish`](#upkeep-publish) | Push an issue's work branch and open a merge request for it. |
| [`review`](#upkeep-review) | Apply a merge request to a running site and print its browsable URL. |
| [`start`](#upkeep-start) | Start (or resume) work on a drupal.org issue: provision an environment and open a branch. |
| [`status`](#upkeep-status) | Report cockpit state; --disk itemizes real measured disk usage per module, core version, and category. |

## Global options

Accepted by every command, so they are listed once rather than repeated below.

| Option | What it does |
| --- | --- |
| `--ansi` | Force (or disable --no-ansi) ANSI output |
| `--help\|-h` | Display help for the given command. When no command is given display help for the list command |
| `--no-ansi` | Negate the "--ansi" option |
| `--no-interaction\|-n` | Do not ask any interactive question |
| `--quiet\|-q` | Only errors are displayed. All other output is suppressed |
| `--silent` | Do not output any message |
| `--verbose\|-v\|-vv\|-vvv` | Increase the verbosity of messages: 1 for normal output, 2 for more verbose output and 3 for debug |

## `upkeep api:probe`

Probe the git.drupalcode.org GitLab API for a module: open MRs and head pipeline status.

```
upkeep api:probe <module>
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name (e.g. conditions_helper) or full project path (e.g. project/conditions_helper) |

## `upkeep base-artifacts:build`

Build the canonical per-core-version base artifacts: resolved base tree + clean-install DB dump.

```
upkeep base-artifacts:build [--version VERSION] [--force] [--stability STABILITY] [--scratch-dir SCRATCH-DIR] [--cockpit COCKPIT]
```

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Drupal core major version to build artifacts for (e.g. 11) |
| `--force` | Deliberately rebuild over an existing artifact set |
| `--stability=STABILITY` | Lowest release stability to accept (dev, alpha, beta, RC, stable). Needed while a core major is still in alpha or beta, which is when compatibility work happens |
| `--scratch-dir=SCRATCH-DIR` | Directory for the throwaway site-install project (must be a path your Docker provider mounts, e.g. under your home directory) Default: `~/.upkeep/scratch`. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep base-artifacts:status`

List which core versions have base artifacts, their build dates and sizes.

```
upkeep base-artifacts:status [--cockpit COCKPIT]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep check`

Run one merge request through the full isolated check flow and cache the per-check results.

```
upkeep check [--version VERSION] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--working-copy] [--fixture FIXTURE] [--] <module> [<mr>]
```

Runs one merge request through the full isolated flow: provision the (module x core) environment, apply the MR, run every check, and cache the result where the dashboard and the fast-lane gate read it.

```
upkeep check pathauto 12
upkeep check pathauto 12 --version=11
upkeep check pathauto 12 --fixture=sample-content
```

Or check what you are working on right now, whatever branch that is — after upkeep start, or upkeep patch:promote, or your own edits:

```
upkeep check pathauto --working-copy
```

That mode needs no merge request and no GitLab token, and caches nothing: a working copy has no revision the dashboard could match a verdict against.

Exits 0 all green, 1 a check failed, 2 it could not run at all. Needs base artifacts for that core: upkeep base-artifacts:build --version=11.

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `mr` | no | Merge request IID on the module's drupalcode project |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |
| `--working-copy` | Check the module working copy as it stands instead of a merge request (caches nothing) |
| `--fixture=FIXTURE` | Load this named fixture into the database before running checks (aborts before any check when the fixture is unknown) |

## `upkeep completion`

Dump the shell completion script

```
upkeep completion [--debug] [--] [<shell>]
```

The completion command dumps the shell completion script required to use shell autocompletion (currently, bash, fish, zsh completion are supported).

Static installation -------------------

Dump the script to a global completion file and restart your shell:

```
upkeep completion bash | sudo tee /etc/bash_completion.d/upkeep
```

Or dump the script to a local file and source it:

```
upkeep completion bash > completion.sh
```

```
# source the file whenever you use the project
source completion.sh
```

```
# or add this line at the end of your "~/.bashrc" file:
source /path/to/completion.sh
```

Dynamic installation --------------------

Add this to the end of your shell configuration file (e.g. "~/.bashrc"):

```
eval "$(upkeep completion bash)"
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `shell` | no | The shell type (e.g. "bash"), the value of the "$SHELL" env var will be used if this is not given |

**Options**

| Option | What it does |
| --- | --- |
| `--debug` | Tail the completion debug log |

## `upkeep dashboard`

Per-module overview of everything open; name a module to see its rows and what to run next.

```
upkeep dashboard [--cockpit COCKPIT] [--version VERSION] [--refresh [REFRESH]] [--no-patches] [--all] [--] [<module>]
```

Where to start. With no arguments it summarises every registered module — how many merge requests and patch issues are open, how many are ready, and how many have never been checked.

Name a module to see its individual rows. Each row carries a NEXT column with the command to run for it.

```
upkeep dashboard                    every module, one line each
upkeep dashboard pathauto           that module's rows, with what to do about each
upkeep dashboard pathauto --refresh re-fetch first (goes to the network)
upkeep dashboard --all              every row of every module
upkeep dashboard pathauto -v        the gate's own reason tokens
```

Cached by default, so repeat runs are instant. upkeep explain &lt;term&gt; defines any column or status.

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | no | Drill into one module: its individual merge requests and patch issues. Omit for the overview. |

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--version=VERSION` | Only show rows targeting this core major version (e.g. 11) |
| `--refresh=REFRESH` | Re-fetch remote data: --refresh for all modules, --refresh=&lt;module&gt; for one |
| `--no-patches` | Omit patch rows: only merge requests, as the dashboard showed before patch contributions were included |
| `--all` | Every row of every module, rather than the per-module overview |

## `upkeep dev`

Prepare an environment for active development: provision if needed, optionally check out a branch, and print the path.

```
upkeep dev [--version VERSION] [--branch BRANCH] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--] <module>
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--branch=BRANCH` | Branch to check out in the module working copy |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |

## `upkeep env:path`

Print the absolute path of a module's environment directory.

```
upkeep env:path [--version VERSION] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--] <module>
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |

## `upkeep exec`

Run a command in a module's environment directory.

```
upkeep exec [--version VERSION] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--] <module> <cmd>...
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `cmd` | yes | Command to run (use -- before the command to separate it from upkeep options) |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |

## `upkeep explain`

Explain a term from upkeep's output (run bare to list them all).

```
upkeep explain [<term>]
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `term` | no | The word to explain, e.g. "LOCAL", "stale", "unclaimed". Matches partially. |

## `upkeep init`

Scaffold a new cockpit: module registry, base-artifacts/, fixtures/, and projects/ directories.

```
upkeep init [<dir>]
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `dir` | no | Directory to create the cockpit in |

## `upkeep issue`

Show and open the drupal.org issue linked to a merge request.

```
upkeep issue [--no-open] [--cockpit COCKPIT] [--] <module> <mr>
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `mr` | yes | Merge request IID on the module's drupalcode project |

**Options**

| Option | What it does |
| --- | --- |
| `--no-open` | Do not open the drupal.org issue in the browser |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep issues`

List a module's open drupal.org issues and what has been contributed to each.

```
upkeep issues [--cockpit COCKPIT] [--status STATUS] [--unclaimed] [--] <module>
```

Every open issue on a module, whether or not anyone has contributed to it — the difference from dashboard and patches, which both start from a contribution.

```
upkeep issues pathauto              every open issue
upkeep issues pathauto --unclaimed  only what nobody has started
upkeep issues pathauto --status=active
```

The merge-request column is filled from the dashboard cache, so run upkeep dashboard --refresh=&lt;module&gt; if it is empty. To begin work on one: upkeep start &lt;module&gt; &lt;issue&gt;.

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not |

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--status=STATUS` | Only this status: active, review, needs-work, rtbc, postponed (default: every open status) |
| `--unclaimed` | Only issues nobody has contributed to yet — the work that has not started |

## `upkeep merge`

Fast-lane merge: prompt per READY-AUTO merge request and merge only on an explicit per-MR approval.

```
upkeep merge [--fast-lane] [--cockpit COCKPIT]
```

Walks the current READY-AUTO rows one at a time, asking for an explicit approval per merge request.

```
upkeep merge --fast-lane
```

Only bot compatibility MRs with green CI and green local checks are ever offered. There is no batch mode and no unattended flag, by Drupal Association policy — see the README's policy stance.

**Options**

| Option | What it does |
| --- | --- |
| `--fast-lane` | Required: run the fast-lane loop (the only mode; named explicitly because it performs merges) |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep modules`

List the modules registered in the cockpit module registry.

```
upkeep modules [--cockpit COCKPIT]
```

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep modules:add`

Register maintained modules from your git.drupalcode.org project memberships (interactive opt-in).

```
upkeep modules:add [--core-versions CORE-VERSIONS] [--cockpit COCKPIT] [--] [<modules>...]
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `modules` | no | Module machine names to register without prompting (must be among your memberships) |

**Options**

| Option | What it does |
| --- | --- |
| `--core-versions=CORE-VERSIONS` | Comma-separated core majors the new entries track (e.g. "10,11") Default: `11`. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep needs-work`

Post local check results as a comment on the merge request.

```
upkeep needs-work [--version VERSION] [--cockpit COCKPIT] [--no-open] [--dry-run] [--] <module> <mr>
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `mr` | yes | Merge request IID on the module's drupalcode project |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--no-open` | Do not open the drupal.org issue in the browser |
| `--dry-run` | Print the comment without posting it |

## `upkeep notes`

Draft paste-ready Markdown release notes: merged MRs since the module's last tag.

```
upkeep notes [--cockpit COCKPIT] [--] <module>
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name (resolved via the cockpit registry when available) or full project path (e.g. project/conditions_helper) |

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |

## `upkeep patch:apply`

Download a patch from a drupal.org issue and apply it in the module environment.

```
upkeep patch:apply [--version VERSION] [--file FILE] [--url URL] [--latest] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--no-update] [--] <module> <issue>
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `issue` | yes | drupal.org issue node id carrying the patch (see `upkeep patches`) |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--file=FILE` | Apply this exact attachment by filename instead of choosing (e.g. 3597808-9-d11.patch) |
| `--url=URL` | Fetch the patch from this URL instead of the issue's attachments (a fork, a re-roll posted elsewhere) |
| `--latest` | Take the newest patch without asking, even when the issue carries several |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |
| `--no-update` | Do not fetch the base branch first; check against the working copy's base as it stands |

## `upkeep patch:check`

Run one drupal.org patch through the full isolated check flow.

```
upkeep patch:check [--version VERSION] [--file FILE] [--url URL] [--latest] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--no-update] [--fixture FIXTURE] [--] <module> <issue>
```

The patch-side counterpart of check: downloads a patch from a drupal.org issue, applies it onto a branch off the base, and runs the full suite.

```
upkeep patch:check pathauto 3597857
upkeep patch:check pathauto 3597857 --latest   take the newest patch without asking
upkeep patch:check pathauto 3597857 --file=NAME
```

With several patches on the issue and no terminal to ask at, the newest is taken, and said so.

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `issue` | yes | drupal.org issue node id carrying the patch (see `upkeep patches`) |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--file=FILE` | Apply this exact attachment by filename instead of choosing (e.g. 3597808-9-d11.patch) |
| `--url=URL` | Fetch the patch from this URL instead of the issue's attachments (a fork, a re-roll posted elsewhere) |
| `--latest` | Take the newest patch without asking, even when the issue carries several |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |
| `--no-update` | Do not fetch the base branch first; check against the working copy's base as it stands |
| `--fixture=FIXTURE` | Load this named fixture into the database before running checks (aborts before any check when the fixture is unknown) |

## `upkeep patch:promote`

Apply a drupal.org patch onto an issue work branch, credited to its author, ready to publish.

```
upkeep patch:promote [--version VERSION] [--file FILE] [--url URL] [--latest] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--no-update] [--branch BRANCH] [--partial] [--] <module> <issue>
```

Converts a patch contribution into a branch you can open a merge request from.

```
upkeep patch:promote pathauto 3597857
upkeep patch:promote pathauto 3597857 --latest
upkeep patch:promote pathauto 3597857 --file=NAME
upkeep patch:promote pathauto 3597857 --partial
```

The commit credits whoever posted the patch, by name, in the message — promoting moves somebody else's work into history, and the commit is the durable record of whose it is. Nothing is pushed: run upkeep publish afterwards, which is the step that puts it on drupal.org.

Afterwards, upkeep check &lt;module&gt; --working-copy runs the suite against the branch it made, and upkeep dev &lt;module&gt; prints the site URL. A patch that only applied with reduced context is a weaker guarantee than a merge request implies, so checking before you publish is worth the minutes.

--partial is for a patch that will not apply at all. Every hunk that still fits lands on the branch and the rest is left as &lt;file&gt;.rej beside the file it could not change, which is where a re-roll starts. Nothing is committed — the commit carries the patch author's name, and half their patch is not what they wrote — so resolve the rejects, delete the .rej files, commit, and publish. It exits 1, because the patch did not apply.

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `issue` | yes | drupal.org issue node id carrying the patch (see `upkeep patches`) |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--file=FILE` | Apply this exact attachment by filename instead of choosing (e.g. 3597808-9-d11.patch) |
| `--url=URL` | Fetch the patch from this URL instead of the issue's attachments (a fork, a re-roll posted elsewhere) |
| `--latest` | Take the newest patch without asking, even when the issue carries several |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |
| `--no-update` | Do not fetch the base branch first; check against the working copy's base as it stands |
| `--branch=BRANCH` | Work branch to promote onto (default: the drupal.org &lt;nid&gt;-&lt;slug&gt; convention) |
| `--partial` | When the patch will not apply, keep the hunks that still fit and leave the rest as .rej files to resolve by hand — the start of a re-roll rather than a refusal |

## `upkeep patches`

Show drupal.org issues carrying patch files, and how they relate to merge requests.

```
upkeep patches [--cockpit COCKPIT] [--module MODULE] [--without-mr]
```

Issues carrying patch files, and how each relates to a merge request — the contributions the MR-centric dashboard cannot see.

```
upkeep patches                  every registered module
upkeep patches --module=pathauto
upkeep patches --without-mr     only what no branch carries
```

To check one: upkeep patch:check &lt;module&gt; &lt;issue&gt;. An MR shown as "empty" carries no commits, so any patch beside it is the only work there is — see upkeep explain "empty MR".

**Options**

| Option | What it does |
| --- | --- |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--module=MODULE` | Only scan this module (default: all registered modules) |
| `--without-mr` | Only issues no merge request carries — omit those where a patch sits alongside a real MR |

## `upkeep prune`

Reclaim disposable state (environment trees, engine projects, materialized snapshots). Dry-run by default; never touches base artifacts, keep-marked items, or committed fixture dumps.

```
upkeep prune [--trees] [--snapshots] [--projects] [--all] [--older-than OLDER-THAN] [--keep-latest KEEP-LATEST] [-y|--yes] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT]
```

Dry-run by default: without --yes the command only lists deletion candidates with their reclaimable sizes. Protected regardless of any flag combination:

```
* base artifacts (<cockpit>/base-artifacts/) — canonical, expensive to rebuild
* committed fixture dumps (module tests/fixtures/*.sql.gz, <cockpit>/fixtures/*.sql.gz)
* keep-marked items: touch <project>/.keep to keep a whole environment, or
<artifact>.keep (e.g. materialized/<name>.sql.keep) to keep one snapshot.
```

Environments are always disposed through the engine adapter (containers and named volumes released with the tree); pruned state regenerates on demand.

**Options**

| Option | What it does |
| --- | --- |
| `--trees` | Prune disposable environment trees (disposed via the adapter teardown, which also releases the engine project) |
| `--snapshots` | Prune materialized fixture snapshots (their committed .sql.gz dumps can rebuild them at any time) |
| `--projects` | Prune whole engine projects: trees plus their docker named volumes, via the adapter teardown |
| `--all` | Prune everything disposable: trees, volumes, and snapshots |
| `--older-than=OLDER-THAN` | Only prune items unused for at least this long (e.g. 30d, 12h); items of unknown age are then excluded |
| `--keep-latest=KEEP-LATEST` | Snapshots: keep this many newest snapshots per project regardless of age Default: `0`. |
| `--yes\|-y` | Actually delete. Without this flag the command is a dry run and deletes NOTHING |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |

## `upkeep publish`

Push an issue's work branch and open a merge request for it.

```
upkeep publish [--version VERSION] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--branch BRANCH] [--title TITLE] [--target TARGET] [--draft] [--] <module> <issue>
```

Pushes the work branch for an issue and opens its merge request — after which it is an ordinary MR that dashboard, check and merge already handle.

```
upkeep publish pathauto 3223746
upkeep publish pathauto 3223746 --draft
```

Opens merge requests; never merges one. Re-running after more commits updates the existing MR rather than opening a second.

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `issue` | yes | drupal.org issue node id whose work branch should be published |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |
| `--branch=BRANCH` | Publish this branch instead of deriving it |
| `--title=TITLE` | Merge request title (default: from the issue) |
| `--target=TARGET` | Branch to merge into (default: the base the work branch was started from) |
| `--draft` | Open it as a draft |

## `upkeep review`

Apply a merge request to a running site and print its browsable URL.

```
upkeep review [--version VERSION] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--] <module> <mr>
```

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `mr` | yes | Merge request IID on the module's drupalcode project |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |

## `upkeep start`

Start (or resume) work on a drupal.org issue: provision an environment and open a branch.

```
upkeep start [--version VERSION] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT] [--no-update] [--branch BRANCH] [--base BASE] [--] <module> <issue>
```

Begins work on an issue nobody has contributed to yet: provisions the environment and opens a branch named to drupal.org's issue-fork convention, so the merge request that follows is linked to the issue.

```
cd $(upkeep start pathauto 3223746)
upkeep start pathauto 3223746 --version=11
```

Resumes rather than restarts: an existing branch is checked out as it stands, and nothing here ever resets or discards. When the work is ready: upkeep publish &lt;module&gt; &lt;issue&gt;.

**Arguments**

| Argument | Required | What it is |
| --- | --- | --- |
| `module` | yes | Module machine name — any Drupal module, registered or not (see `upkeep modules`) |
| `issue` | yes | drupal.org issue node id to work on (see `upkeep issues &lt;module&gt;`) |

**Options**

| Option | What it does |
| --- | --- |
| `--version=VERSION` | Target core major version; must be tracked by the module's registry entry. Defaults to the first core version listed there. |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |
| `--no-update` | Do not fetch the base branch first; check against the working copy's base as it stands |
| `--branch=BRANCH` | Name the branch yourself instead of deriving it from the issue title |
| `--base=BASE` | Branch to start from (default: whatever the module working copy is currently on) |

## `upkeep status`

Report cockpit state; --disk itemizes real measured disk usage per module, core version, and category.

```
upkeep status [--disk] [--cockpit COCKPIT] [--projects-root PROJECTS-ROOT]
```

**Options**

| Option | What it does |
| --- | --- |
| `--disk` | Itemize disk usage (project trees, materialized snapshots, docker volumes, base artifacts, fixture dumps) with totals |
| `--cockpit=COCKPIT` | Path to the cockpit directory (defaults to $UPKEEP_COCKPIT, then the current directory) |
| `--projects-root=PROJECTS-ROOT` | Directory holding the engine environments (defaults to $UPKEEP_PROJECTS_ROOT, then &lt;cockpit&gt;/projects/ if it exists, then ~/.upkeep/projects) |

