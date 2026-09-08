# The dashboard

Everything open across the modules you watch, and what a row actually represents.

## Dashboard

The dashboard has two tiers, because volume is real: a mature contrib project
can carry a hundred open merge requests, and multiplying that by tracked core
versions puts a *single* module past two hundred rows.

```bash
upkeep dashboard              # overview: one line per module
upkeep dashboard pathauto     # drill in: that module's individual rows
upkeep dashboard --all        # every row of every module
```

The overview answers "where should I look?":

```
MODULE      BRANCHES         MRS    PATCH ISSUES    READY    CI FAILED    UNCHECKED    CACHED

pathauto    8.x-1.x          100              22        –            2          118    17h ago
paragraphs  1.0.x,2.0.x       12              61        2            1           71    2m ago
```

`BRANCHES` names the module branches those rows sit on. It is deliberately not
a list of core versions: **one branch supports several cores at once** —
pathauto's single `8.x-1.x` declares `^10.2 || ^11 || ^12` — so a core version
says nothing about which piece of work a row is.

`MRS` and `PATCH ISSUES` count *subjects*: an issue carrying two merge requests
is one row and two merge requests, and the column says "issues" because a row's
own patch count is a number of *files*. `READY`, `CI FAILED` and `UNCHECKED`
count rows.

`UNCHECKED` is the actionable number: rows with no local verdict, plus rows
whose verdict is about a revision that is no longer current, plus rows checked
on some of the cores their branch supports and not others.

The counts are aggregated from exactly the rows the drill-down prints, never
recounted — a module whose overview says two READY-AUTO shows two when you
open it.

Naming a module narrows everything about the run, including the fetch: `upkeep
dashboard pathauto --refresh` re-fetches pathauto and nothing else.

## One row is one issue's work on one branch

Drilling in (or `--all`) shows every open contribution — merge requests *and*
patches, which are columns of the same row rather than rows of their own —
with the linked drupal.org issue and its status, upstream CI, your own check
results, and the fast-lane gate's verdict.

Every row carries a **STATUS** saying what it is in plain English and a
**NEXT** column with the command to run for it:

```
MODULE    ISSUE            VERSION  TITLE                    MR           PATCH  CI    LOCAL       STATUS             NEXT

pathauto  3262847 review   8.x-1.x  Only update child taxo…  !12          –      –     –           needs a check      upkeep check pathauto 12 --version=10
pathauto  3608383 RTBC     8.x-1.x  Remove forum integratio  !99          –      pass  pass 10,11  ready to merge     upkeep merge --fast-lane
pathauto  3311669 review   8.x-1.x  Punctuation processed…   !40 +1       2 ↑    fail  fail 10     CI failed          upkeep check pathauto 40 --version=10
pathauto  3597857 active   8.x-1.x  Config schema for form   –            4      –     stale 11    4 patches          upkeep patch:check pathauto 3597857
widget    3598272 review   2.0.x    Drupal 12 compatibility  !3 merged 2026-09-03  2   –     –     merged 2026-09-03  upkeep issue widget 3
```

- **ISSUE** — the nid and the status drupal.org has it in. An en dash is a
  merge request that claims no issue; a fifth of them do.
- **VERSION** — the module branch the work targets. The one multiplier left: an
  issue backported to two branches is two rows, because that is two pieces of
  work rather than one seen twice.
- **MR** — the representative merge request, `+n` for others on the same issue,
  and a **landing outranks everything**: `!3 merged 2026-09-03` says the issue's
  work is already in git, which an issue kept open by convention cannot tell you
  itself.
- **PATCH** — how many patch files the issue carries; `↑` means the newest of
  them postdates the merge request, so the branch may be behind the issue.
- **LOCAL** — your cached check results across every core the branch supports,
  **worst case winning and naming its core**. `pass 10,11` is green on both;
  `fail 10` names where it broke and does not list the greens beside it;
  `pass 11 · ? 10` is green where checked and honest about the core nobody has
  run. `-v` lists every core separately.

**Every row has a command.** Red CI points at `check`, because a red pipeline
is exactly when you want the branch locally to reproduce the failure; a draft
is checkable too, because unfinished is frequently abandoned and picking that
up is the job. `draft,` and `CI failed` describe the row without changing what
to do about it — the command comes from the evidence *you* hold, so an
unchecked row says check it and a checked one says look at the change. The core
in the command is the core the LOCAL cell named, so the two cannot disagree.

`upkeep explain <term>` defines any of it; bare, it prints the whole
vocabulary. `-v` swaps the plain-English status for the gate's own reason
tokens (`REVIEW not-bot-author, ci-missing, local-missing`), which is what
anything scripted against them should read.

**On the gate's own verdicts**, visible under `-v`:

- `READY-AUTO` — a Project Update Bot compat MR with green CI and green local
  checks **on every core its branch supports**. A merge request checked on 11
  and never checked on 10 is not ready: that used to be two rows, one of them
  READY-AUTO, and the fast lane took the ready one.
- `REVIEW` — needs a human look, listed with its reasons. `not-bot-author`
  appears on every human-authored MR and is not a problem: the fast lane is
  bot-only by design.
- `BLOCKED` — set by red CI, and by nothing else. It does **not** mean merge
  conflicts; the gate never inspects mergeability.

A row with an empty MR column is a contribution with no branch behind it. It
carries no CI (drupal.org runs pipelines on branches, not on attachments — half
the reason patch work goes unreviewed) and no gate status, because there is
nothing there upkeep could merge.

`LOCAL` on such a row is its cached `patch:check` verdict, keyed to the exact
patch it was recorded against. A re-roll posted since then is a new upload, so
the row reads `stale` rather than showing you a green light for code nobody
checked. An issue with nothing open and no patch attached gets no row at all —
that is `upkeep issues`' subject, where being unclaimed is the point.

`upkeep dashboard --no-patches` restores the merge-request-only view.

- **LOCAL's cores** are the ones the row's *branch* declares, not every core in
  the registry. Each branch's `core_version_requirement` is read from its own
  info.yml on a refresh, so pathauto's 8.x-1.x is never asked about a core it
  does not claim — a failure there would say nothing about the module, and
  since the fast lane needs every applicable core green, an unchecked core the
  branch never claimed would block a merge on its own. A branch whose info.yml
  cannot be read, or whose constraint will not parse, keeps every tracked core:
  missing evidence about a branch is not evidence about a branch.

`upkeep dashboard --version=11` narrows the *evidence* to one core. It does not
remove rows: core is not what a row is about.

Remote data (GitLab MRs and drupal.org issues) is cached per module. On
subsequent runs the dashboard loads instantly from cache. To re-fetch:

```bash
upkeep dashboard --refresh            # re-fetch all modules
upkeep dashboard --refresh=widget     # re-fetch one module only
```

Local check results and gate verdicts are always resolved fresh — only the
remote API data is cached. The cache age is shown below the table.

**`--refresh` costs more than it did.** It now also scans each module's Needs
Review / RTBC issues for patches, and drupal.org returns attachments as
references that must each be fetched individually — a busy module can be
several hundred requests. Those run **8 at a time**, which brings them down to
~50ms each against ~530ms serialised; what remains is dominated by the issue
listing pages themselves, which must be read in order. Expect tens of seconds
per module on a cold cache, near-instant when drupal.org's own cache is warm.
Cached runs are unaffected. A progress bar is shown on stderr while fetching,
so stdout stays exactly the table a pipe expects. Use `--no-patches` to skip
the scan entirely.

**A degraded scan says so.** drupal.org's API fails by returning less, not by
erroring: a dropped attachment makes an issue look like it carries fewer
patches, and a truncated listing page makes a project look like it has fewer
issues. Both are reported explicitly, with the counts underneath flagged as
lower bounds:

```
 [WARNING] 3 drupal.org request(s) did not answer.
           Attachment 7128077 could not be read (HTTP 500); an issue may
           report fewer patches than it has.
           ...
           Counts below are lower bounds: re-run to pick up what was missed.
```

**Rate limits.** git.drupalcode.org publishes its budget in `RateLimit-*`
headers — 180 requests/minute unauthenticated, more with a token — and Upkeep's
GitLab calls stay sequential and few (a project lookup, an MR list, one detail
per MR), so the concurrency added here does not touch that budget. drupal.org's
api-d7 publishes no quota at all, which is why the cap is a conservative 8
rather than something tuned to a published limit. A 429 from either is honoured:
the batch waits for `Retry-After` and retries once, and anything asking for more
than 10 seconds is reported rather than slept through.
