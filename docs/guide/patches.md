# Patch contributions

The contributions a merge-request dashboard cannot see, and the whole patch surface around them.

## Patch contributions

The dashboard is MR-centric, so contributions that arrive as a patch file
never appear on it. Plenty of the Drupal community still works that way, and
plenty of issues carry both — a patch posted in comment 4, a merge request
opened in comment 9, a re-roll posted in comment 14 that never made it onto
the branch. To see them:

```bash
upkeep patches                      # every registered module
upkeep patches --module=widget      # one module
upkeep patches --without-mr         # only what no branch carries
```

lists drupal.org issues in Needs Review or RTBC alongside the state of any
merge request on the same issue:

```
MODULE    ISSUE       STATUS    PATCHES    LATEST PATCH             MR         TITLE

widget    #3489012    review    2          3489012-12-schema.patch  –          Add config schema
widget    #3467675    RTBC      1          3467675-4-required.patch !7         Make URL field required
widget    #3597808    review    3          3597808-9-d11.patch      !1 empty   Drupal 11 compatibility
```

The MR column is the point. `–` means no merge request claims the issue, `!7`
means one does and carries changes, and **`!1 empty` means one exists and
carries nothing** — an open branch with no commits the target does not already
have. The Project Update Bot leaves exactly that on a great many contrib
projects, and an empty MR covers no work: the patch beside it is the only
contribution there is, and it is yours to review or close the MR over.

An issue is withheld only when a merge request genuinely carries the work —
it claims authorship of the issue (the `Issue #NNN` title convention, an
issue-fork branch name, or a drupal.org issue link) *and* has a non-empty
diff. A passing mention like the bot's `Relates to #NNN` is not authorship,
and does not withhold anything. Pass `--without-mr` to narrow the report to
issues no branch carries at all; empty-MR rows stay, because those are
precisely the ones that look covered and are not.

This is the one command with a documented degraded mode: with no GitLab token
it warns once, scans without cross-referencing merge requests, and still exits
0 — a wider result set rather than no result at all. Where a `dashboard` cache
exists it is used, and it already knows which MRs are empty; otherwise the
scan asks GitLab for the detail of each MR attached to a scanned issue, since
GitLab's merge-request *list* omits the diff refs that settle the question.

## Check a patch

A patch contribution costs the same as a merge request:

```bash
upkeep patch:check widget 3597808 --version=11
```

takes the drupal.org issue node id straight out of the `patches` table above,
downloads the patch, applies it onto a branch off the module's base, runs the
full check suite, and reports:

```
Resolving issue #3597808 via drupal.org ...
Patch: 3597808-9-d11.patch (comment 9, 4.2 KB, 2026-06-11)
Issue #3597808 "Automated Drupal 12 compatibility fixes" (review)
Target: Drupal core 11, module widget

[Patch] Applying patch "3597808-9-d11.patch" onto 1.0.x as patch-3597808 ...

 Check    Status   Exit  Duration
 phpunit  passed   0     31.2s
 phpcs    FAILED   1      2.1s

 [ERROR] 1 check(s) failed for 3597808-9-d11.patch (issue #3597808).
 Report it at https://www.drupal.org/node/3597808 — the drupal.org API is
 read-only, so the status change is a browser action.
```

Same exit-code contract as `check`: **0** all green, **1** a check failed,
**2** upkeep could not produce a verdict — which includes a patch that no
longer applies.

**Applying escalates before giving up.** Three attempts, each loosening
something different, none loosening what has to *match* — the changed lines
are compared exactly throughout:

1. **straight** — the patch as cut.
2. **three-way** — resolves hunks plain context matching rejects, whenever the
   blobs the patch was generated against are in the repository.
3. **reduced context** — one line of surrounding context instead of three.

The third rung is not a nicety. drupal.org generates patches against an export
whose files may carry a trailing blank line the repository does not, so a hunk
header promises seven context lines for a six-line file and git refuses all
nine files over one of them. Project Update Bot patches hit this routinely.
When a patch applies at rung 2 or 3 you are told, because a hunk placed on one
line of context is a weaker guarantee than one placed on three:

```
Applied via reduced context — the patch did not match the working copy
exactly; review the result with that in mind.
```

**When all three fail, the report says what is stale rather than only that
something is:**

```
Patch "3597808-4-old.patch" (issue #3597808) does not apply to "1.0.x",
even with a three-way merge and reduced context.

It changes 9 file(s); 8 apply, 1 do not:
  tests/modules/widget_test/widget_test.info.yml

git looked for this and did not find it:
  name: 'Widget Test'
  type: module
  core_version_requirement: ^10 || ^11

That file has moved on since the patch was cut. Re-roll against "1.0.x", or
check the issue for a newer patch.
```

"One of these nine files is stale" is actionable in a way "the patch failed"
is not.

**Choosing the patch.** An issue routinely carries several — an original, two
re-rolls, an interdiff. With one patch attached there is nothing to choose.
With several, you are asked:

```
 Which patch should be applied?
  [0] 3597808-12-reroll.patch (comment 12, 5.1 KB, 2026-06-14) [newest]
  [1] 3597808-9-d11.patch (comment 9, 4.2 KB, 2026-06-11)
  [2] 3597808-4-first.patch (comment 4, 3.8 KB, 2026-05-02)
```

Three ways to skip the question:

```bash
upkeep patch:check widget 3597808 --latest                    # newest, no prompt
upkeep patch:check widget 3597808 --file=3597808-9-d11.patch  # that exact one
upkeep patch:check widget 3597808 --url=https://example.test/reroll.patch
```

`--url` covers the re-roll posted somewhere other than the issue — a fork, a
pipeline artefact. The issue id is still required, because the branch, the
commit message and the report are keyed on it. A name `--file` cannot match is
refused and the available patches listed; it never quietly falls back to a
different one. Non-interactively (a pipe, cron, `--no-interaction`) the newest
is taken and *said*, so a scripted run never reports a verdict on a patch
nobody named.

`--fixture=NAME` works exactly as it does on `check`.

## Apply a patch without checking it

```bash
cd $(upkeep patch:apply widget 3597808 --version=11)
```

downloads and applies the patch, then stops — for when you want to click
through the site or run something the suite does not cover. It takes the same
selection options, prints the environment path on stdout and nothing else, and
leaves the working copy on `patch-<nid>`.

**Where the verdict goes.** Results are cached exactly as `check`'s are, and
show up in the LOCAL column of that issue's dashboard row. They are keyed by
the patch's source URL, so a verdict about a patch that has since been
re-rolled reads as `stale` rather than as a current pass.

They are stored under a separate namespace (`results/<module>/patch-<nid>/…`)
from merge-request results (`results/<module>/<iid>/…`). That separation is
deliberate and structural: the fast-lane gate reads merge-request entries only,
so a patch verdict can never become grounds for merging a branch. A patch is
not something upkeep can merge — if it is good, the way to move it into the
pipeline is `patch:promote`, below.

Known limitation: the key is the patch's URL, not its bytes. drupal.org mints a
distinct URL per upload, so a re-roll is always detected; a `--url` pointing at
something edited in place (a gist) is not.

## Has this issue already been done?

Some issues are kept open on purpose. Project Update Bot compatibility issues
are the standard case: the convention is to leave them open so the bot can post
again as core moves, which means an open one may have had its work merged
months ago. Two such issues look identical until you ask what merged.

`upkeep patches` now asks. The MR column reads:

```
!1 merged 2026-06-12                    the work landed, nothing since
!1 merged 2026-06-12, newer work since  it landed, then the bot posted again
!2 empty                                a bot draft covering nothing
```

Verified against live projects: `conditions_helper` #3596502 is *active* with
its MR merged on 2026-06-12, while `field_visibility_conditions` #3598272 is
*needs review* with an open draft. Opposite situations, indistinguishable
before this.

The dashboard says it too, and there it matters most: promote a patch, fix it,
merge it, and the bot's draft is left as the only *open* merge request on the
issue — so the row used to read `draft, needs a check` and point at checking a
branch that had been superseded. It now reads `merged 2026-09-03`.

It never says "resolved" and never changes an issue's status — api-d7 is
read-only, and whether a landed-and-quiet issue should be closed is a judgement
about the convention, not about the evidence. It reports what merged and
whether anything has happened since; the close stays yours.

**Why bot merge requests used to pair with nothing.** They are titled
`Automated Project Update Bot fixes`, their branch is `project-update-bot-only`,
and their description says only "Relates to #NNN" — which upkeep rejects on
purpose, so a bot cannot suppress an issue's patches merely by mentioning it.
Correct, and it left every bot MR attached to no issue at all. The fix is the
issue fork: an MR from `issue/<module>-<nid>` belongs to that issue, because
drupal.org made that repository *for* it. That is a fact about how the fork
exists rather than a string somebody typed, so it outranks every other signal.

## Which branch a patch is applied to

A drupal.org issue is filed against a **version**, and its patches are cut from
that branch. upkeep reads the issue's version, checks it against the branches
the project actually has, and applies the patch there:

```
Base branch: 2.0.x (from the issue version "2.0.0")
```

Without this, a patch from a 2.0.0 issue was applied to whatever the clone had
checked out — usually the default branch — and reported `does not apply to
1.0.x`. True, and useless: it was never meant for 1.0.x.

The version field is never *parsed* into a branch, only proposed and checked.
Real issues carry `2.0.0`, `8.x-1.x-dev`, `4.6.x-dev`, `5.1`, `6.14` and — in
one sample, 56 times — the literal string `x.y.z`. So upkeep generates
candidates (`2.0.0` → `2.0.x` → `2.x`), takes the first that names a real
branch, and falls back to the working copy's base if none does, saying so:

```
Issue version "x.y.z" matches no branch on project/widget (1.0.x, 2.0.x);
using the working copy's base.
```

Everything about this degrades quietly. No version, no GitLab token, an
unreadable branch list — all of them fall back rather than refuse, because
resolving the branch exists to be right more often, not to add a way to be
stopped.

## When a patch will not apply

Applying escalates — straight, then `--3way`, then reduced context — and only
when all three fail is the patch reported as unappliable. The report names
which files are stale and shows the context git could not find, because "this
needs a re-roll" is the review outcome worth reporting.

Two different failures produce it, and they want opposite things:

- **The branch moved.** The patch is genuinely stale and wants re-rolling
  against the branch.
- **The patch was cut against a release tarball.** drupal.org's packaging
  script appends `version`, `project` and `datestamp` to every `.info.yml` when
  it builds a release, so a patch generated from an unpacked release carries
  those lines as *context* — and no commit ever had them. It cannot apply to a
  checkout however fresh the branch is, and re-rolling "against 1.0.x" sends
  you looking for changes nobody made. upkeep says so when it sees the
  packaging lines in the failing context; the fix is to regenerate the patch
  from a git checkout.

Either way, `--partial` turns the refusal into a starting point. It is on
**`patch:promote` only**, whichever command produced the failure:

```bash
upkeep patch:promote pathauto 3603341 --partial
```

Every hunk that still fits is applied onto the issue work branch and the rest
is left as `<file>.rej` beside the file it could not change. You resolve those,
delete the `.rej` files, and carry on:

```bash
upkeep check pathauto --working-copy
upkeep publish pathauto 3603341
```

**Nothing is committed.** A promoted patch's commit carries the patch author's
name (see below), and half their patch plus a pile of rejects is not what they
wrote — putting their name on it would be the misattribution the whole
attribution rule exists to prevent. Committing is yours, once it is your work.

It exits 1, not 0: the patch did not apply, and a script treating a partial
promotion as success would publish half of somebody's contribution.

If *nothing* fits, `--partial` refuses exactly as it would without the flag —
an empty working copy beside a pile of rejects is not a head start on anything.

### Why only `patch:promote`

Resolving rejects is work, and it has to go somewhere that survives.

| Command | Lands on | Survives the next apply? |
| --- | --- | --- |
| `patch:check` | `patch-<nid>` | No — reset from the base every time |
| `patch:apply` | `patch-<nid>` | No — reset from the base every time |
| `patch:promote` | the issue work branch | Yes — `start` never resets one |

That reset is deliberate where it is: it makes a re-roll get tested on its own
rather than stacked on whatever was applied last. But it means rejects resolved
on `patch-<nid>` would be destroyed by the next `patch:check` on that issue,
without warning, because destroying that branch is the correct behaviour there.
The issue work branch is the one upkeep promises never to reset — it can hold
the only copy of something — so it is the only safe place to do the work.

`patch:check --partial` would be worse than unavailable. It would run the suite
against a half-applied patch and cache the result *as a verdict on the patch* —
evidence the fast-lane gate reads.

## Turn a patch into a merge request

A patch and a merge request carry the same work, but only one of them gets CI,
review threads, or a fast lane. `patch:promote` closes that gap:

```bash
upkeep patch:promote widget 3597808 --version=11
upkeep publish widget 3597808
```

The first command applies the patch onto the issue's work branch
(`<nid>-<slug>`, drupal.org's own convention) and commits it. The second pushes
and opens the MR — the same `publish` the issue loop uses, so the result is an
ordinary merge request that `dashboard`, `check` and `merge` already handle.

It takes the same selection options as `patch:check` (`--file`, `--url`,
`--latest`), plus `--branch` to override the branch name.

**The commit credits the patch's author, by name.** Promoting moves somebody
else's work into history under whoever pushes it, and the commit message is the
only durable record of whose work it was. So upkeep resolves the account that
posted the file and writes drupal.org's own commit convention:

```
Issue #3597808 by hebatelhayah: Fix the widget on PHP 8.4

Applied from the patch "3597808-9-fix.patch", posted to the issue by
hebatelhayah, and promoted to a merge request with `upkeep patch:promote`. The
change is hebatelhayah's work; whoever opens the merge request is carrying it
over, not authoring it.

Patch-author: hebatelhayah <https://www.drupal.org/u/hebatelhayah>
Patch-file: 3597808-9-fix.patch
Patch-source: https://www.drupal.org/files/issues/3597808-9-fix.patch
Issue: https://www.drupal.org/node/3597808
```

Set `UPKEEP_PROMOTER` to add a `Promoted-by:` trailer naming yourself.

There is deliberately **no `--author` and no `Co-authored-by:`**. Both want an
email address; drupal.org publishes a username and a profile URL and no address
at all, and synthesising one would put a guess about somebody's identity into
permanent history. If the file records no readable account, upkeep says so
loudly and still promotes — the commit states the work is not the promoter's
and points at the issue. Credit on drupal.org is allocated through the
issue-credit system anyway, which is a browser action and stays yours to do.
