---
name: upkeep
description: >-
  Maintain contributed Drupal modules with the upkeep CLI: triage drupal.org and
  GitLab issue queues, check merge requests and patches in isolated ddev
  environments against the same merge ref and the same checks drupal.org CI
  uses, work an issue, and prepare it for a merge request. Use when working on a
  Drupal contrib module, a drupal.org issue, a patch, or a merge request on
  git.drupalcode.org, and whenever asked to triage, check, review or fix
  contributed Drupal module code. Reads structured output with --json and stops
  before anything that writes to drupal.org.
metadata:
  author: owenbush
---

# upkeep

A CLI that drives ddev and ddev-drupal-contrib to check and work on contributed
Drupal modules. One environment per module per core version, built by copying a
per-core base artifact set rather than resolving composer each time.

Run `upkeep --help` for the command list and `upkeep <command> --help` for any
one of them. This file is the judgement that is not in the help text.

## Always start here

```bash
upkeep dashboard --json
```

Every row carries a `status` and a **`next`** field holding the literal command
to run. Follow `next` rather than composing your own: it already accounts for
the core version, the subject and what state the row is in.

Name a module for its rows: `upkeep dashboard pathauto --json`.

## Use --json, not the tables

Nine commands take `--json`: `dashboard`, `issues`, `patches`, `check`,
`patch:check`, `status`, `review`, `publish`, `patch:promote`.

Do not parse the tables. They truncate titles to fit a column, print `–` for an
empty cell, and set column widths from the widest row. The JSON carries the
untruncated values and is a contract; the tables are free to change shape.

Every document is `{"upkeep", "schema", "command", "warnings", "data"}` on
stdout, and nothing else is ever on stdout in that mode.

**Read `warnings`.** It is not decoration. These readers degrade by returning
*less data*, so a non-empty `warnings` means every count and list below it is a
**lower bound**. Say so when you report findings.

## Exit codes decide what you do next

| Code | Meaning | What to do |
| --- | --- | --- |
| `0` | upkeep did what was asked | carry on |
| `1` | the work it supervised failed | read the JSON; this is about the code |
| `2` | upkeep could not do the job | this is about your invocation or the setup |

**Never retry a `2` unchanged.** It means a bad argument, an untracked core, a
missing environment or a missing credential — running it again produces the
same refusal. Read the message: refusals in this tool name the command to run.

A failed `check` is exit **1** and still emits its payload. You do not need to
parse anything to know it failed.

## Stop before anything that writes to drupal.org

Three commands act in public under the maintainer's name:

- `publish` — pushes a branch and opens a merge request
- `needs-work` — posts a comment a contributor will read and act on
- `merge` — merges a merge request

**Do not run these unless the person asked for that specific action in this
session.** Prepare the work, then report the exact command for them to run.

`publish` and `needs-work` refuse without a terminal unless `--yes` is passed,
and `merge` refuses outright. Treat those refusals as correct rather than as
something to work around: passing `--yes` to get past one is the agent
equivalent of forging a signature.

`needs-work --dry-run` prints the comment and posts nothing. That is the right
way to prepare one for review.

## Which branches survive

`check`, `review`, `patch:apply` and `patch:check` **force-update the
disposable branch they own** — `mr-<iid>` or `patch-<nid>` — on every run. A
commit you make there is a commit waiting to be destroyed.

Branches upkeep never resets, and so the only ones to commit on:

```bash
upkeep start <module> <nid>       # a new work branch for an issue
upkeep checkout <module> <iid>    # somebody's merge request, to take it further
upkeep patch:promote <module> <nid>  # somebody's patch, onto a work branch
```

`upkeep env:path <module>` prints the environment directory; the module's own
git checkout is at `<that path>/module`, and that is where a commit goes.

## Checks cost minutes, not seconds

`check` provisions an environment if there is not one, applies the subject, and
runs PHPUnit, PHPStan, PHPCS, an install check and a smoke test. Do not put it
in a loop and do not re-run it to see the same answer twice — read the JSON you
already have.

Check what you have written without any merge request or token:

```bash
upkeep check <module> --working-copy --json
```

## Two things you will otherwise report wrongly

**A green drupal.org pipeline does not mean CI found nothing.** The
gitlab_templates CI marks the phpcs and phpstan jobs `allow_failure: true` by
default, so a pipeline reports success while those jobs failed inside it. Never
tell somebody "CI is green" from the pipeline status alone.

**CI tests the merge ref, not the branch.** drupal.org analyses the
contributor's branch merged into the *current* tip of the target. Measured on
pathauto, 23 of 25 open merge requests have a merge tree that differs from
their own branch. So "the branch passes" is not "CI will pass", and upkeep
deliberately checks the merge ref for that reason.

If `upkeep check` and the project's own pipeline disagree, read
`upkeep check`'s standards note before concluding either is wrong: the coder
and PHPCS versions follow the core version being tested, and the two can want
opposite things.

## Reading needs no credential

`dashboard`, `issues`, `patches`, `check` and `review` work with no GitLab
token at all, because git.drupalcode.org serves a public project's merge
requests, refs, forks and raw files anonymously. A token is for writing. If a
read fails for lack of one, something else is wrong.

## Fixtures

Some modules cannot be exercised on an empty site. `--fixture=<name>` loads a
committed database fixture before the work, on `check`, `patch:check` and
`review`.

**It overwrites the environment's database.** On `check` and `patch:check` that
is the point. On `review` be aware somebody may have been using that site.

Nothing loads a fixture unless asked: there is no default and no inference.

## Any module, registered or not

The registry is a watchlist for the dashboard, not a gate. Every command above
works on a module nobody has registered — so you do not need to add anything to
configuration before working on an issue.

## When you are stuck

- `upkeep explain <term>` defines any word the output prints.
- The "Which command do I want?" guide at https://upkeep.owenbush.dev covers
  the command surface by situation.
- `upkeep status` shows what is on disk; `upkeep prune` reclaims it and is
  dry-run until `--yes`.
