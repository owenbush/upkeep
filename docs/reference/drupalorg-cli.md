# drupalorg-cli: where upkeep uses it, and where it does not

Upkeep runs [drupalorg-cli](https://github.com/mglaman/drupalorg-cli) for ten
issue workflow actions and nothing else. Everything upkeep *reads* from
drupal.org and git.drupalcode.org still goes through its own clients.

This page explains that split: what we use the CLI for, what we tried to use it
for and didn't, the evidence behind each decision, and what would make us
revisit it. Every figure below was measured against drupalorg-cli **0.10.3** on
2026-11-03 and 2026-11-04.

## Why we looked at it

Upkeep had its own clients for drupal.org's api-d7 (`internal/drupal`) and for
git.drupalcode.org's GitLab API (`internal/gitlab`), and drupalorg-cli covers a
lot of the same ground. Maintaining a second implementation of something the
community already maintains is a cost, so the starting rule was simple:

> **Use the CLI for everything it supports at parity; keep our own code for
> everything it does not.**

"At parity" was defined strictly. A capability moves to the CLI only if the CLI
returns every field upkeep's callers actually read, with the same meaning. We
checked that against live drupal.org rather than fixtures. The reason for the
strict bar: upkeep removes the native code for anything that moves, so there is
no fallback, and a missing field doesn't error. It shows up as missing data,
like an issue that seems to have no patches or a merge request that pairs with
nothing.

## Where upkeep uses the CLI

For actions upkeep had no implementation of at all. The CLI already does them
well, so it was the right call to wrap it rather than write them again.

| upkeep command | runs | posts publicly? |
|---|---|---|
| `issue:fork <module> <issue>` | `issue:fork` | yes |
| `issue:get-access <module> <issue>` | `issue:get-access` | yes |
| `issue:assign <module> <issue> [users...]` | `issue:assign` | yes |
| `issue:unassign <module> <issue> [users...]` | `issue:unassign` | yes |
| `issue:reassign <module> <issue> <users...>` | `issue:reassign` | yes |
| `issue:label <module> <issue> <labels...>` | `issue:label` | yes |
| `issue:unlabel <module> <issue> <labels...>` | `issue:unlabel` | yes |
| `issue:relabel <module> <issue> <labels...>` | `issue:relabel` | yes |
| `issue:setup-remote <module> <issue>` | `issue:setup-remote` | no (local git) |
| `issue:fork-checkout <module> <issue> [branch]` | `issue:checkout` | no (local git) |

You need PHP and drupalorg-cli 0.10.3 or newer only for these ten. Everything
else in upkeep works without it. See [Install](../guide/install.md).

### How these differ from running the CLI yourself

Upkeep adds a few things on top of the CLI. Each one is deliberate:

- **Module-scoped arguments.** Every command takes `<module>` first, like the
  rest of upkeep, and passes the CLI a project-qualified reference
  (`project/<module>#<nid>`), never a bare number. On a project whose issues
  moved to GitLab, new issue numbers continue from drupal.org's node IDs, so a
  bare number can name an unrelated node on drupal.org. On `helper`, 3619173 is
  "Mercury Editor" from another project.
- **The write gate.** The eight posting commands act in public under your name,
  so they refuse to run without a terminal unless you pass `--yes`, the same
  rule as `publish` and `needs-work`. Running the CLI directly has no such
  check.
- **One credential source.** Upkeep passes the GitLab token it already uses
  (`UPKEEP_GITLAB_TOKEN` or `~/.config/upkeep/drupal-pat`) to the CLI as
  `DRUPALORG_GITLAB_TOKEN`. It goes only in the child process's environment,
  never on the command line, and is redacted from everything the CLI prints.
  This is the one place upkeep hands a credential to another program, and it
  is documented as the only one.
- **A different name for checkout.** The CLI's `issue:checkout` is
  `issue:fork-checkout` in upkeep, because upkeep's own `checkout` (take over
  a merge request) already exists and does something different.
- **Text, not JSON.** Upkeep passes the CLI's own output through to you and
  parses none of it.

## Where upkeep does not use the CLI, and why

### Merge request reads: tried, measured, kept native

The CLI's `mr:list` and `mr:status` overlap with four of upkeep's GitLab reads:
`OpenMergeRequests`, `MergedMergeRequests`, `MergeRequest` and `HeadPipeline`.
We built CLI-backed versions of all four. We ran them side by side with the
native client on content_sync, pathauto, token and jumplinks, and compared
every field upkeep's callers read. **All four failed.**

| Read | Fields the CLI could not supply | Speed vs native |
|---|---|---|
| Open merge requests | author ID, description, source project | 2.6× slower |
| Merged merge requests | as above, plus merged date | 3.2× slower |
| A single merge request | as above, plus head SHA, merge status, diff refs, pipeline project | 9.3× slower (two PHP processes) |
| Head pipeline | pipeline project; also reports `skipped` as `canceled` | 2.8× slower |

Those fields aren't incidental. Each one does a job in upkeep:

- The **author ID** lets upkeep recognise bot merge requests.
- The **description** and **source project** pair a merge request with the
  issue it fixes. The source project is the *only* thing that pairs a Project
  Update Bot MR.
- The **merged date** answers "has this already landed?" for issues kept open
  by convention.
- The **head SHA** and **diff refs** key cached check results and detect empty
  merge requests.
- The **pipeline project** is where an issue fork's CI jobs actually live.

We also found that `mr:list` writes `\<` into its JSON when a title contains
`<`, which isn't valid JSON. On pathauto a single such title (MR !104) made
every CLI read of the project fail.

With no fallback allowed, moving any of these would have been a regression, so
all four stay native. We then deleted the CLI-backed versions rather than keep
unused code around. The comparison harness itself is in git history at commit
`7db67a3` (`cmd/parity`), so the measurement can be repeated against a newer
CLI.

### `mr:logs`: native, because the CLI's version can't find fork pipelines

`upkeep mr:logs <module> <mr>` prints the failed jobs and their log tails for a
merge request's pipeline. We first wrapped the CLI's `mr:logs`, but it looks up
pipeline jobs in the merge request's *target* project. On drupal.org a merge
request's pipeline runs in the *issue fork*. For content_sync !22, the jobs are
in the fork (project 250962, which answers 200) and the CLI asks the main
project (55211, which answers 404). Nearly every drupal.org merge request comes
from an issue fork, so the CLI's version fails in the usual case. Upkeep reads
the jobs from wherever the pipeline actually ran.

### Issues and issue listings: never candidates

`drupal.Issue` and `ProjectIssues` drive the dashboard, the patch surface and
`issues`. The CLI doesn't cover what they need:

- `issue:show` returns no attachments, so the patch files would disappear.
- `issue:search` requires a title keyword and returns only ID, title and
  status.
- Neither sees issues created on GitLab after a project's queue moved there.

## How we keep this honest

- **A live contract check.** `go run ./cmd/livecheck --cli-contract` runs
  `drupalorg help` for each of the ten wrapped commands and fails if a CLI
  release renames one or changes its arguments. It only ever runs `help`. The
  command list comes from the same table that builds upkeep's commands, so the
  two can't disagree.
- **A minimum version.** Upkeep refuses a CLI older than 0.10.3, the release
  this was verified against, with a message that says how to upgrade.
- **The bar for moving more.** A read moves to the CLI only after a live
  comparison of the fields its callers read comes back clean. If that changes,
  the native code goes.

## What would change this

None of this is a criticism of drupalorg-cli. It serves its own users well, and
upkeep needs some unusual data: bot pairing, fork-aware pipelines, evidence
keyed on merge-ref SHAs. The gaps we found are drafted as upstream issues:

1. `issue:show --format=json` has no attachments.
2. `issue:search` requires a query and returns only nid, title and status.
3. GitLab-migrated queues: issues created after the move are missing.
4. `mr:list <issue nid>` returns every MR in the project.
5. `mr:logs` reads jobs from the target project rather than the fork.
6. `mr:list` emits invalid JSON for titles containing `<`.
7. `mr:list` lacks author ID, description, source project, merged date, SHAs,
   merge status and diff refs, and can't page beyond 100.
8. `mr:status` collapses pipeline statuses and has no pipeline project.

If those are fixed upstream, re-run the comparison and move whatever passes.
That's the same rule we started with.
