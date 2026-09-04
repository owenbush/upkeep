# Plan — the dashboard row model

*Status: built, and verified against live pathauto data — see §7.*

---

## 1. What is wrong

The dashboard has two conflations, and each one multiplies rows without adding
information.

**An issue appears more than once.** Rows are built from *subjects*: one per
merge request, plus one per patch-carrying issue. An issue with two merge
requests is two rows; an issue with an MR and a patch would be three, which is
why `RowFactory::patchRows()` carries an `isCoveredByMergeRequest()` filter
whose only job is to suppress the duplicate. That filter is the design
apologising for itself.

**Core version is treated as identity.** Every row is multiplied by the
module's tracked `core_versions`. But a module *branch* supports several cores
at once — measured on live projects:

| Module | Branch | `core_version_requirement` |
|---|---|---|
| field_visibility_conditions | 1.0.x | `^10 \|\| ^11` |
| field_visibility_conditions | 2.0.x | `^10.1 \|\| ^11 \|\| ^12` |
| pathauto | 8.x-1.x | `^10.2 \|\| ^11 \|\| ^12` |
| token | 8.x-1.x | `^10.3 \|\| ^11 \|\| ^12` |

Pathauto has **one** branch supporting three cores. With `core_versions:
['10','11']` in the registry, every pathauto issue becomes two rows describing
the same branch and the same work — and `^12` is not represented at all,
because the registry caps it. This is where the measured 244 rows came from:
roughly (100 MRs + 22 patch issues) × 2 cores. Half were duplicates by
construction.

The distinction being missed is **identity versus evidence**. What a
contribution *is*: an issue, targeting a module branch. How it was *tested*: on
some core. Core belongs to the evidence.

---

## 2. The model

> **Identity** = (module, issue, module branch).
> **Evidence** = per core, and lives in the cells.

A row is one issue's work on one branch. The multiplier that remains — an issue
with work on two branches, i.e. a backport — is a real distinction between two
pieces of work, not a test matrix.

Merge requests claiming no issue keep a row of their own. This is not an edge
case: **33 of pathauto's 162 merge requests claim no issue** (20%). Such a row
has no issue cell and takes its branch from the MR's `target_branch`.

### 2.1 Columns

```
MODULE  ISSUE           VERSION  TITLE                    MR          PATCH  CI    LOCAL        STATUS              NEXT
widget  #3598272 review 2.0.x    Automated D12 compat...  !3 merged   2 ↑    –     pass 10,11   merged 2026-09-03   upkeep issue widget 3
widget  #3467675 RTBC   1.0.x    Make URL field required  !14 +1      –      pass  fail 10      CI failed on core 10  upkeep check widget 14 --version=10
widget  –               2.0.x    Refactor the resolver    !9          –      pass  –            needs a check       upkeep check widget 9
```

- **ISSUE** — nid and drupal.org status, or `–` for an MR claiming none.
- **VERSION** — the module branch. Resolved from the MR's `target_branch`, else
  from the issue's version field via `Drupal\IssueVersion` (already built, and
  already checked against the project's real branches rather than parsed).
- **MR** — representative merge request, `+n` for others on the same issue.
  A landing outranks everything: `!3 merged 2026-09-03`.
- **PATCH** — count of patch files, `↑` when newer than the MR's last update.
  This becomes a statement about one row rather than a cross-reference between
  two, which is what `patch↑` was and why nobody could read it.
- **CI** — drupal.org's pipeline for the branch head.
- **LOCAL** — see below.
- **STATUS / NEXT** — `Dashboard\Guidance`, unchanged in spirit.

### 2.2 LOCAL, and disagreeing cores

Evidence is per core, so the cell carries the cores. **Worst case wins, with
detail available**, because the actionable fact is that something is broken:

| Cell | Meaning |
|---|---|
| `pass 10,11` | checked on both applicable cores, green on both |
| `fail 10` | red somewhere; the failing core is named, not the passing ones |
| `stale 11` | evidence exists but predates the current head |
| `pass 11 · 10 ?` | green where checked, never checked on 10 |
| `–` | nothing checked |

Under `-v`, every core is listed with its own verdict and revision.

### 2.3 Which cores apply

Today: the registry's `core_versions`, whole. That is wrong in both directions
— it can name a core the branch does not support, and it caps a branch that
supports more.

Proposed: **applicable cores = tracked ∩ declared**, where *declared* is
`core_version_requirement` read from the branch's `.info.yml`. Checking
pathauto's 8.x-1.x on a core it does not declare produces a failure that means
nothing; conversely, a registry tracking only 10 and 11 should not silently
hide that a branch claims 12.

Cost: one file read per (module, branch), via GitLab's raw-file endpoint,
cached in the snapshot. When it cannot be read, **fall back to the tracked set
whole** — the current behaviour — and say so. (This is the same
degrade-never-refuse rule used for the issue-version lookup.)

`core_versions` in `registry.yml` therefore means what it has always actually
meant: *which environments to provision and test on*. Not a row multiplier. The
key is not renamed — that is a breaking change for no functional gain.

---

## 3. Verdicts across cores

**This is a behaviour change and the most important thing in this document.**

Today, a bot MR green on core 11 and unchecked on core 10 produces two rows:
one `READY-AUTO`, one not. The fast lane sees the ready one. The evidence
supporting a merge is therefore *partial*, and nothing says so.

Proposed: one row, and it is `READY-AUTO` only when **every applicable core is
green against the current head**. Missing or stale evidence on any applicable
core denies the fast lane.

This is strictly stricter. It will move some rows out of READY that are in it
today, and that is the point: those rows were never entitled to be there.

`merge --fast-lane` is unchanged — it still takes an MR iid and still prompts
per merge request. The row simply names the iid in NEXT.

---

## 4. What happens to each existing piece

| Piece | Fate |
|---|---|
| `RowFactory::rows()` | becomes issue-row assembly |
| `RowFactory::patchRows()` | **gone** — merged into the above |
| `isCoveredByMergeRequest()` filter | **gone** — the duplication it suppressed cannot occur |
| `RowFactory::landingsByIssue()` / `landingFor()` | **gone** — the row *is* the issue |
| `DashboardRow::forMergeRequest/forPatch` | one `forIssue()`, plus `forUnlinkedMergeRequest()` |
| `DashboardRow::$core` | replaced by `$branch` + per-core evidence |
| `Patches\Contribution` | promoted: it becomes the row's payload rather than a thing bolted onto one branch of the code |
| `Results\ResultKey` | **unchanged** — already (module, subject, core, revision); evidence genuinely is per core |
| `Gate\FastLaneGate` | classifies over a row's whole evidence set, not one (MR, core) |
| `ModuleSummary` | see below |
| `merge --fast-lane` | unchanged |

### 4.1 Summary counts change meaning

`CORES` becomes `BRANCHES`. `READY` changes from "merge requests that are
ready" to "issues with a ready merge request" — these differ when an issue
carries two bot MRs. `MRS` and `PATCH ISSUES` are unaffected. Row counts drop
sharply: pathauto's ~244 becomes ~120, and every remaining row means something
distinct.

### 4.2 Snapshot migration

`ModuleSnapshot` gains per-branch `core_version_requirement`. A snapshot written
before this exists reads as "all tracked cores apply" — the current behaviour,
not a wrong claim. Same rule already used for `mergedMrData` and `forkNids`.
No cache invalidation needed; `--refresh` picks up the rest.

---

## 5. Risks

- **Blast radius.** 10 source files, 5 test files, and the code path that
  produced most of this week's bugs. That cuts both ways: it removes the
  structure that made them possible, and it is the worst place to be sloppy.
- **The verdict change is silent unless announced.** Rows will leave READY.
  Without a note in the release, that reads as a regression.
- **`core_version_requirement` parsing.** A `^10.2 || ^11 || ^12` constraint is
  not something to hand-roll. `composer/semver` does it correctly and is **not
  currently a dependency** (44 packages locked, none of them semver), so this
  step adds one — a deliberate decision, not a detail. The alternative,
  matching major versions with a regex, is wrong the first time a module writes
  `^10.2` and someone asks whether core 10.1 is covered. Whatever is chosen,
  an unparseable constraint falls back to the tracked set whole and says so —
  never to a guess.
- **Verified against fixtures is not verified.** Every bug this week survived a
  green suite. This lands with a run against real modules — pathauto, token,
  field_visibility_conditions, conditions_helper — and the before/after row
  counts recorded.

---

## 6. Sequence

1. ~~**`core_version_requirement` reading**~~ — **done** (`0aa5444`).
2. ~~**Evidence across cores**~~ — **done** (`6faa06b`).
3. ~~**Row identity**~~ — **done** (`9334156`), and it took step 4 with it.
4. ~~**Verdict across cores**~~ — **done, inside step 3 rather than after it.**
   Planned to land alone, and it could not: the moment a row stops being one
   (subject, core) pair it has no single-core verdict to carry, so
   `FastLaneGate::classify()` had to take the core *list* and the evidence
   across it in the same commit. Keeping them apart would have meant picking an
   arbitrary core to gate on for one commit — a worse state than the one being
   fixed.
5. ~~**Summary and UI**~~ — done for `ModuleSummary` (CORES → BRANCHES) and
   `Ui\StateBuilder`. The browser *page* still renders the old field names and
   is untouched, at the user's request.
6. ~~**§2.3, applicable cores**~~ — **done.** `ModuleSnapshot::$coreConstraints`
   holds branch => raw `core_version_requirement`, read from that branch's
   info.yml on a refresh, and `RowFactory::applicable()` narrows the tracked
   cores to it per row. Landed together with the deletion it paid for: the
   per-merge-request issue lookup, which cost **155 drupal.org requests on
   pathauto alone** plus an attachment lookup per file on each, for a cell that
   is now the row's own identity. A refresh is strictly cheaper than before —
   one info.yml per branch (pathauto: two) against 155 issue fetches.

### Why step 3 had no smaller green slice

Measured before starting it: **20 files** referenced the members it changes
(`$core`, `$local`, `forPatch`, `forMergeRequest`), across ~1,800 lines of
source plus eight test files. `DashboardRow`, `RowFactory`, `Guidance`,
`ModuleSummary`, `RowAssembler`, `DashboardCommand` and `Ui\StateBuilder` all
break together the moment the constructors change, and every dashboard test
asserts on cell layout that moves at the same time.

Attempts to slice it further were considered and rejected:

- *Remove the core multiplier first, keep subject rows* — same blast radius,
  because it is the constructor signature that breaks everything, not the row
  count.
- *Merge patch rows into MR rows first, keep `$core`* — leaves two row
  identities in play at once, which is the state the whole change exists to
  end.

---

## 7. Live verification

Run on 2026-09-04 against pathauto's real GitLab and drupal.org data — 107 open
merge requests, 62 merged, 196 issue forks, 93 open issues with their 301
attachments dereferenced — fed through the real `ModuleSnapshot`, `RowFactory`
and `ModuleSummary`. Fixtures were what hid every bug this week; this is the
check that was missing.

| | |
|---|---|
| Rows, old model | ~244 — (107 MRs + 22 patch issues) x 2 tracked cores |
| **Rows, new model** | **115** — 67 issue rows + 48 unlinked merge-request rows |
| Rows per branch | 114 on `8.x-1.x`, 1 on `7.x-1.x` |
| Merge requests counted | 107 (matches the input) |
| Patch issues counted | 42 |

What the sample table confirmed, which no fixture had:

- **The grouping is real.** `!73 +1` is one issue carrying two merge requests
  on one row, where the old model made two.
- **Patch and merge request coexist.** `!133` with 8 patch files, `!125` with
  4 — each was previously an MR row plus a patch row the covered-by filter
  suppressed.
- **Pairing loses nothing.** Of the 48 unlinked rows, 2 claim no issue at all
  and 46 claim an issue that is genuinely *not open* (fixed or closed).
  **Zero** referenced an issue that was in the open queue, i.e. no pairing
  misses. Those 46 still print their nid, which is why `forUnlinkedMergeRequest`
  carries one.
- **drupal.org status earns its column.** `RTBC`, `needs work`, `postponed`
  and `review` all appear, and they ask different things of a maintainer.

### The one thing that is wrong, and knowably so

pathauto's `7.x-1.x` branch has no `core_version_requirement` — Drupal 7 used
`core = 7.x` in a `.info` file, which predates the key entirely. So the
constraint lookup finds nothing, the fallback applies, and that row gathers
evidence on cores 10 and 11 for a branch that obviously supports neither.

Left as it is on purpose. The fallback rule is *missing evidence about a branch
is not evidence about a branch*, and the alternative — inferring core support
from a branch name — is exactly the parsing `Drupal\IssueVersion` exists to
refuse. One row on one module, and it is visibly a D7 branch.
