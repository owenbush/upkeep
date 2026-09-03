# Plan — the dashboard row model

*Status: proposed. Nothing below is built. Written to be disagreed with before
1,400 tests are rewritten.*

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

Each step is independently green and shippable.

1. **`core_version_requirement` reading** — client method, `semver` matching,
   fallback. No behaviour change yet; just the data.
2. **Row identity** — `DashboardRow` keyed on (issue, branch); `forIssue()`;
   `Contribution` as payload. `patchRows()` and the covered-by filter deleted.
3. **Evidence per core** — LOCAL renders worst-case-plus-detail; `-v` lists all.
4. **Verdict across cores** — `FastLaneGate` over the whole evidence set.
   *The behaviour change; lands alone, with its reasoning in the commit.*
5. **Summary and UI** — `ModuleSummary` columns, `Ui\StateBuilder`.
6. **Live verification** — real modules, row counts before and after, and the
   four never-yet-run commands (`check`, `patch:check`, `start`, `publish`)
   exercised against a real environment.

Steps 1–3 are mechanical. Step 4 is the one to review carefully. Step 6 is the
one this week says not to skip.
