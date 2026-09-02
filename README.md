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
> directory by default. The projects root (where environments live) must
> therefore be under `$HOME`, and Upkeep **refuses** a
> `--projects-root`/`UPKEEP_PROJECTS_ROOT` (or a
> `base-artifacts:build --scratch-dir`) that resolves outside it, exiting 2
> with an explanation. That is cheaper than an opaque mount failure minutes
> into a provision. The check is done on the resolved path, so a symlink or a
> `..` pointing out of `$HOME` is refused too, and there is no override — a
> path outside your home directory cannot work. The defaults
> (`<cockpit>/projects`, `~/.upkeep/projects`, `~/.upkeep/scratch`) are all
> inside `$HOME` already, so this only matters if you move them.

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

That scaffolds `registry.yml` plus empty `base-artifacts/`, `fixtures/` and
`projects/` directories. Every command finds the cockpit via `--cockpit`, else
the `UPKEEP_COCKPIT` environment variable, else the current directory —
exporting the variable once is the comfortable setup:

```bash
export UPKEEP_COCKPIT=~/upkeep-cockpit
```

Environments live under the projects root, resolved in this order:

1. `--projects-root`;
2. the `UPKEEP_PROJECTS_ROOT` environment variable;
3. `<cockpit>/projects/`, when that directory exists — which is why `init`
   creates it: a cockpit is self-contained by default;
4. `~/.upkeep/projects`.

Whichever wins must resolve to a path under `$HOME` (see the Colima note
above).

Two more directories appear inside the cockpit as you use it: `results/`
(cached check results) and `cache/` (cached GitLab and drupal.org data). Both
hold token-scoped remote data and raw check output, so Upkeep creates them
`0700` with `0600` files. If you keep your cockpit in version control, exclude
both — they are caches, they regenerate, and they are not meant to be shared.

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
upkeep base-artifacts:build --version=11
```

Re-running for an existing core version requires `--force` (a deliberate
rebuild). See what you have:

```bash
upkeep base-artifacts:status
```

## Daily flow

### Dashboard

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
MODULE      CORES    MRS    PATCH ISSUES    READY    CI FAILED    UNCHECKED    CACHED

pathauto    10,11    100              22        –            2          244    17h ago
paragraphs  11        12              61        2            1           73    2m ago
```

`MRS` and `PATCH ISSUES` count *subjects* — one merge request tracked across
two cores is one merge request, and the column says "issues" because a row's
own patch count is a number of *files*. `READY`, `CI FAILED` and `UNCHECKED`
count per (subject × core), because the same branch can be green on 10 and red
on 11, which is the whole reason both are tracked.

`UNCHECKED` is the actionable number: rows with no local verdict, plus rows
whose verdict is about a revision that is no longer current.

The counts are aggregated from exactly the rows the drill-down prints, never
recounted — a module whose overview says two READY-AUTO shows two when you
open it.

Naming a module narrows everything about the run, including the fetch: `upkeep
dashboard pathauto --refresh` re-fetches pathauto and nothing else.

Drilling in (or `--all`) shows every open contribution across tracked core
versions — merge requests *and* patch-only issues — with the linked drupal.org
issue, upstream CI state, local check results (from your cached `check` and
`patch:check` runs), and the fast-lane gate status:

Every row carries a **STATUS** saying what it is in plain English and a
**NEXT** column with the command to run for it:

```
MODULE    MR     ISSUE     CORE  TITLE                    CI    LOCAL  STATUS          NEXT

pathauto  !12    3262847   11    Only update child taxo…  –     –      needs a check   upkeep check pathauto 12 --version=11
pathauto  !99    3608383   11    Remove forum integration pass  pass   ready to merge  upkeep merge --fast-lane
pathauto  !40    3311669   11    Punctuation processed…   fail  –      CI failed       (the contributor's move)
pathauto  patch  3597857   11    Config schema for form   –     stale  4 patches       upkeep patch:check pathauto 3597857
```

A command in NEXT is yours to run. A note in parentheses means there is
nothing for you to do — it is waiting on somebody else.

`upkeep explain <term>` defines any of it; bare, it prints the whole
vocabulary. `-v` swaps the plain-English status for the gate's own reason
tokens (`REVIEW not-bot-author, ci-missing, local-missing`), which is what
anything scripted against them should read.

**On the gate's own verdicts**, visible under `-v`:

- `READY-AUTO` — a Project Update Bot compat MR with green CI and green local
  checks; eligible for the fast-lane merge prompt.
- `REVIEW` — needs a human look, listed with its reasons. `not-bot-author`
  appears on every human-authored MR and is not a problem: the fast lane is
  bot-only by design.
- `BLOCKED` — set by red CI, and by nothing else. It does **not** mean merge
  conflicts; the gate never inspects mergeability.

Rows whose MR column reads `patch` are contributions with no branch behind
them. They carry no CI (drupal.org runs pipelines on branches, not on
attachments — half the reason patch work goes unreviewed) and no gate status,
because there is nothing there upkeep could merge. Their STATUS says what
arrived instead:

```
MODULE   MR     ISSUE      CORE  TITLE                      CI  LOCAL  STATUS

widget   !7     3467675    11    Make URL field required    ok  pass   READY-AUTO
widget   patch  3597808    11    Drupal 12 compatibility    –   pass   PATCH 1 patch
widget   patch  3597857    11    Config schema for form     –   stale  PATCH 4 patches, !1 empty
```

`LOCAL` on a patch row is its cached `patch:check` verdict, keyed to the exact
patch it was recorded against. A re-roll posted since then is a new upload, so
the row reads `stale` rather than showing you a green light for code nobody
checked. An issue whose work a *real* branch already carries gets no patch row
— it already has its MR row above, and the classification is the same one
`upkeep patches` uses, so the two views cannot disagree about what is covered.

`upkeep dashboard --no-patches` restores the merge-request-only view.

Filter to one core version with `upkeep dashboard --version=11`.

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

See [Exit codes](#exit-codes) for what `check` returns.

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
endpoint closed to PATs — HTTP 403), an approved row is not lost: Upkeep
prints the exact browser merge URL for that MR and records it as handled
manually. You perform the same single human action, just in the browser. A
rejected *credential* is HTTP 401 and is reported differently, because "use
the browser" would be the wrong advice for a bad token.

**Exit codes.** `merge` exits 0 when everything you approved went through (or
you approved nothing), 1 when any merge failed, and 2 when GitLab rejected the
credential or no token was configured.

### Interact with an environment

Once a `check` or `review` has provisioned an environment, you can run
commands in it directly without knowing the project path:

```bash
upkeep exec entity_type_access_conditions -- ddev drush cr
upkeep exec entity_type_access_conditions -- ddev ssh
upkeep exec entity_type_access_conditions --version=10 -- ddev logs
```

Everything after `--` is run with the environment directory as the working
directory.

**The child's exit code is not passed through.** `exec` answers on the same
0/1/2 contract as every other command: 0 if the command succeeded, **1 for any
non-zero exit** — 1, 2, 7 and 127 all collapse to 1 — and 2 if Upkeep could not
run it at all (unregistered module, untracked core version, no provisioned
environment, or a child process that never started). Collapsing is deliberate:
it means a child exiting 2 can never be mistaken for an Upkeep infrastructure
failure. If you need the child's own code, have the child report it — for
example `upkeep exec mod -- sh -c 'mycmd; echo "rc=$?"'`.

**The child does not inherit `UPKEEP_GITLAB_TOKEN`.** The credential is
removed from every child environment, because Symfony's process layer
otherwise copies the whole parent environment into every subprocess — and this
tool logs, renders and caches that subprocess's output. If a command you run
through `exec` needs a GitLab token, give it one from its own source rather
than relying on inheritance.

Upkeep's own diagnostics from `exec` go to **stderr**; stdout belongs entirely
to the wrapped command, so piping it stays clean.

To get the bare path (for `cd` or other tools):

```bash
cd $(upkeep env:path entity_type_access_conditions)
upkeep env:path entity_type_access_conditions --version=10
```

`env:path` prints the path and nothing else on stdout — its errors also go to
stderr, so `cd $(upkeep env:path …)` can never capture an error message into
the path.

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

The `<mr>` argument must be a merge request IID — a positive integer.
Anything else (`abc`, `0`, `-3`) is refused with exit 2 rather than being
turned into a request for `!0` and a confusing 404. The same rule applies
everywhere an MR number is taken: `check`, `review`, `issue`, `needs-work`.

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

### Work on an issue

The other three commands start from a *contribution* — a merge request, or a
patch somebody already posted. These start from the issue itself, which is
where a maintainer's own work begins.

```bash
upkeep issues pathauto                  # every open issue, and what is on it
upkeep issues pathauto --unclaimed      # only what nobody has started
upkeep issues pathauto --status=active
```

```
ISSUE       STATUS      PRIORITY  CONTRIBUTION  TITLE

#3608478    RTBC        Major     !183          Memcache Transaction-Aware Issue After…
#3616056    review      Normal    1 patch       Deprecated function: Using null as an…
#3223746    review      Normal    unclaimed     JSON API response has empty path when…
#3611658    active      Normal    unclaimed     Generator pattern cache grows unbounded…

93 open issues · 42 awaiting you · 29 unclaimed
```

`unclaimed` is the point of the view: an issue nobody has contributed to is the
most actionable row on the list. The old scan covered Needs Review and RTBC
only — 42 of pathauto's 93 open issues — so everything Active, Needs work or
Postponed was invisible, which is precisely where work begins.

Then:

```bash
cd $(upkeep start pathauto 3223746 --version=11)
# ... write the fix, commit it ...
upkeep publish pathauto 3223746
```

`start` provisions the environment and opens a branch named to drupal.org's
issue-fork convention (`<nid>-<slug>`), so drupal.org, GitLab and upkeep all
recognise it as belonging to that issue. Re-running **resumes** — an existing
branch is checked out as it stands, and nothing here ever resets, forces or
discards. That is the difference from the `mr-` and `patch-` branches, which
*are* reset on every apply so a contribution is tested alone.

`publish` pushes the branch and opens the merge request. What it pushes
re-enters the pipeline that already existed: an ordinary MR that `dashboard`,
`check` and `merge` have always handled. Re-running after more commits reports
the MR that exists rather than duplicating it.

**It opens merge requests; it never merges them.** Those are opposite acts, and
merging stays behind `merge --fast-lane`'s per-MR prompt.

**It cannot create issues or change their status.** drupal.org's API is
read-only (a POST answers 403), so that will always be a browser action —
`upkeep issue` gets you there in one step.

### Patch contributions

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

### Check a patch

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

### Apply a patch without checking it

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
pipeline is to push it as a branch and open an MR.

Known limitation: the key is the patch's URL, not its bytes. drupal.org mints a
distinct URL per upload, so a re-roll is always detected; a `--url` pointing at
something edited in place (a gist) is not.

### Browser UI

```bash
upkeep ui
```

serves the cockpit as a page on `127.0.0.1` and opens it. Filterable, modules
expandable in place — the progressive disclosure a terminal cannot do, which is
what makes a 244-row module readable.

Two views, mirroring the two questions:

- **Waiting for you** — the contribution rows. A **Check** button runs `check`
  or `patch:check` in the background and streams the output into a drawer.
- **Issue queue** — every open issue, contribution as a column, unclaimed work
  highlighted. **Start** opens a work branch; **Publish** pushes it and opens
  the merge request.

A finished job refreshes the rows it affected.

`--port=N` picks the port, `--no-open` suppresses the browser. It runs in the
foreground until Ctrl-C — there is no daemon, no pid file, and no port left
listening afterwards.

**If the page says the link is from a previous run:** the token is minted fresh
each time `upkeep ui` starts, so a tab from an earlier run carries a dead one.
No command re-prints the current link, but you rarely need one:

1. find the `127.0.0.1` address ending `?token=…` in your browser history — the
   newest is the live one; or
2. use the URL the running `upkeep ui` printed; or
3. stop it with Ctrl-C and run `upkeep ui` again.

Following a fresh launch URL is always enough — the token in it takes
precedence over whatever cookie the browser is still holding, and refreshes it.

Starting a second `upkeep ui` while the first still holds the port is refused
rather than half-started, because the port would keep answering with the *old*
token and any URL printed would already be dead.

**How it is kept safe, and why it bothers.** It runs on your machine, but the
port is reachable by anything else running there — including **any web page you
visit**, which can POST to `127.0.0.1` without being able to read the reply.
That matters because the process holds your GitLab PAT, which can push branches
and open merge requests in your name. So:

- the link carries a token minted per run and never written to disk, and every
  path is behind it, assets included;
- the page also sets it as an `HttpOnly`, `SameSite=Strict` cookie, which
  authenticates its own scripts and API calls and keeps that cookie off
  cross-site requests entirely;
- every refusal is an identical 404, except a plain visit to the page, which
  explains itself.

The token **stays in the address bar**. Removing it looked tidier and stranded
people: a restart mints a new one, and a tab whose URL had been cleaned had
nothing left to present and no way to find the current link except the terminal
it came from. Left there, the link is recoverable from your history. The trade
is that a screenshot of the window shows the token for the life of that run.
The browser never supplies a command line: it names one of three actions
(`check`, `patch-check`, `refresh`) whose parameters are validated into shapes
they already had to have. Binding to loopback keeps the port off the network,
but not away from other software on the machine — the token is the actual
barrier.

**Publish is the only thing that leaves your machine, and it asks first.** A
merge request is public the moment it exists, so the button opens a
confirmation rather than firing. `Start` needs no such guard: it is local, and
resumes an existing branch rather than resetting it, so pressing it twice loses
nothing.

**It cannot merge.** The Drupal Association stance is one human approval per
merge, and `merge --fast-lane`'s per-MR prompt is what earns that; a button
that posts an action name is not the same thing, and a table of checkboxes
beside a "merge selected" control is exactly the batch mode this tool refuses
to have. Publishing is not that call — it proposes work for review, which is
what the policy protects rather than restricts — but *merging* from the browser
still needs its own design first.

**It is a renderer, not a second tool.** What it shows comes from the same
`RowFactory` the CLI table and the fast-lane gate consume, and what it *does*
is run the `upkeep` binary as a subprocess — so exit codes, adapter behaviour
and secret redaction are inherited rather than reimplemented, and the page
cannot drift from the terminal.

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

### When the engine refuses a project name

ddev project names are global to your machine, and upkeep derives them from
`upkeep-<module>-d<core>`. So if your projects root moves — a new cockpit, or a
`--projects-root` / `UPKEEP_PROJECTS_ROOT` that differs from one you used
before — ddev still has the old path registered under that name and refuses to
configure the new one.

upkeep now catches this before it seeds anything, and tells you what to run:

```
The engine already knows a project called "upkeep-widget-d11", at a different path:
  registered: /Users/owen/.upkeep-scratch/projects/upkeep-widget-d11
  wanted:     /Users/owen/contrib/upkeep/projects/upkeep-widget-d11

If the registered path is stale, deregister it and re-run:
  ddev stop --unlist upkeep-widget-d11

That removes the engine's record of it; it does not delete the directory or
anything in it. If the registered path is the one you actually want, point
--projects-root at it instead.
```

## Disk housekeeping

```bash
upkeep status
```

summarizes the cockpit, environments, and total tracked disk usage;
`upkeep status --disk` itemizes it per module, core version, and category
(project trees, docker volumes, materialized snapshots, base artifacts,
fixture dumps) with totals. The header line reports how many modules are
registered:

```
Cockpit: /home/you/upkeep-cockpit (2 registered module(s))
```

To count them, `status` — like `base-artifacts:status` and
`base-artifacts:build` — parses `registry.yml` rather than just checking that
it exists, so a malformed registry now fails these commands with exit 2 too,
instead of being discovered later by a command that reads it.

```bash
upkeep prune --all
```

is a **dry run** — it lists deletion candidates (environment trees, docker
volumes, materialized fixture snapshots) with reclaimable sizes and deletes
nothing until you add `--yes`. Narrow it with `--trees`, `--snapshots`, or
`--projects`, age-gate with `--older-than=30d`, and keep the newest N
snapshots per project with `--keep-latest=N`. Everything pruned regenerates
on demand.

`--older-than` measures an environment's age from when it was **last used**,
not when it was created: each environment's `.upkeep-env.yml` carries a
`last_used_at` timestamp, stamped at provision and re-stamped every time the
environment is reused. An environment created months ago but checked against
this morning is not a deletion candidate. Environments provisioned before this
field existed parse fine — the key is optional — and fall back to their
creation date until the next time they are used.

Protected regardless of flags: base artifacts, committed fixture dumps
(`.sql.gz`), and keep-marked items — `touch <project>/.keep` keeps a whole
environment, and an `<artifact>.keep` sibling (e.g.
`materialized/<name>.sql.keep`) keeps a single snapshot. Environments are
always disposed through the engine adapter, so containers and named volumes
are released together with the tree.

## Exit codes

Every Upkeep command answers with one of exactly three codes. This is a
contract you can script against, and it is the same contract for all of them —
not just `check` and `review`:

| Code | Meaning |
| ---- | ------- |
| 0 | the command did what was asked |
| 1 | the work it supervised failed — a red check, a merge GitLab refused, a non-zero command run through `exec` |
| 2 | Upkeep could not do the job, so there is no verdict — no cockpit or registry, no token, an unregistered module, an untracked core version, an argument Upkeep rejected, an unresolvable MR, an engine or API failure |

The distinction that matters to a script: **1 means look at the subject, 2
means look at your setup.**

Consequences worth knowing:

- **No GitLab token exits 2** — from every command that needs one
  (`api:probe`, `check`, `review`, `dashboard`, `merge`, `notes`, `issue`,
  `needs-work`, `modules:add`), because no credential means no verdict was
  produced. The one deliberate exception is `patches`, whose token-less mode
  is documented and degraded rather than broken: it warns once, scans without
  cross-referencing merge requests, and still exits 0.
- **`merge` exits 1 when any merge failed**, and 2 if GitLab rejected the
  credential (HTTP 401).
- **`upkeep exec` does not pass the child's exit code through** — see
  [Interact with an environment](#interact-with-an-environment).
- Anything Upkeep itself could not do exits 2, including a malformed
  `registry.yml`, a module missing from it, a core version the module's entry
  does not track, a merge-request argument that is not a positive integer, and
  a per-MR directory under `<cockpit>/results/` that exists but cannot be
  read. That last one used to be reported as "never checked" — indistinguishable
  from a genuinely unchecked MR, and enough to make the fast-lane gate withhold
  a merge for a reason invisible to you.
- A command that could not write what it was asked to write **fails** rather
  than printing success — `init` and `modules:add` in particular. Every file
  Upkeep produces is written to a temporary file and renamed into place, so a
  reader never sees a half-written registry and a failed write is never
  reported as a completed one.

The error messages are deliberately uniform, so the same situation reads the
same way whichever command you hit it from. An unregistered module lists the
modules that *are* registered; an untracked core version names the ones the
entry does track and tells you to add it to `core_versions` in `registry.yml`;
a missing cockpit always says to run `upkeep init` or point `--cockpit` /
`UPKEEP_COCKPIT` at an existing one.

One caveat: a command line the Symfony console cannot even parse — an unknown
command, an unknown option, a missing required argument — is rejected by the
console before Upkeep's contract applies, and exits **1**. The three codes
above describe every invocation Upkeep actually runs.

## Command reference

| Command | Description |
| ------- | ----------- |
| `upkeep init [<dir>]` | Scaffold a new cockpit: `registry.yml`, `base-artifacts/`, `fixtures/`, `projects/` |
| `upkeep modules` | List the modules registered in the cockpit registry |
| `upkeep modules:add` | Register maintained modules from your git.drupalcode.org memberships (interactive opt-in) |
| `upkeep api:probe <module>` | Probe the GitLab API for a module: open MRs and head pipeline status |
| `upkeep base-artifacts:build --version=N [--force] [--scratch-dir=DIR]` | Build the canonical per-core base artifacts (resolved tree + clean-install dump) |
| `upkeep base-artifacts:status` | List built core versions with dates and sizes |
| `upkeep dashboard [<module>] [--version=N] [--refresh[=MODULE]] [--no-patches] [--all]` | Per-module overview; name a module (or `--all`) for individual MR and patch rows |
| `upkeep check <module> <mr> [--version=N] [--fixture=NAME]` | Full isolated check flow for one MR |
| `upkeep review <module> <mr> [--version=N]` | Apply an MR to a running site and print its browsable URL |
| `upkeep dev <module> [--version=N] [--branch=B]` | Prepare an environment for active development: provision if needed, optionally check out a branch, print the path |
| `upkeep exec <module> [--version=N] -- <command...>` | Run a command in the module's environment directory |
| `upkeep env:path <module> [--version=N]` | Print the absolute path of a module's environment directory |
| `upkeep issue <module> <mr> [--no-open]` | Show the linked drupal.org issue and open it in the browser |
| `upkeep needs-work <module> <mr> [--version=N] [--dry-run] [--no-open]` | Post the local check results as a comment on the merge request |
| `upkeep issues <module> [--status=S] [--unclaimed]` | Every open issue and what has been contributed to it |
| `upkeep start <module> <issue> [--version=N] [--branch=B] [--base=B]` | Provision and open a work branch for an issue (resumes if it exists) |
| `upkeep publish <module> <issue> [--title=T] [--target=B] [--draft]` | Push the work branch and open its merge request |
| `upkeep patches [--module=NAME] [--without-mr]` | drupal.org issues in Needs Review / RTBC carrying patch files, and the state of any MR beside them |
| `upkeep patch:check <module> <issue> [--version=N] [--file=NAME\|--url=URL\|--latest] [--fixture=NAME]` | Run one drupal.org patch through the full isolated check flow |
| `upkeep patch:apply <module> <issue> [--version=N] [--file=NAME\|--url=URL\|--latest]` | Download and apply a patch, then print the environment path |
| `upkeep merge --fast-lane` | Per-MR human-approved merges of READY-AUTO rows only |
| `upkeep notes <module>` | Paste-ready Markdown release notes since the last tag |
| `upkeep status [--disk]` | Cockpit state; `--disk` itemizes measured disk usage |
| `upkeep ui [--port=N] [--no-open]` | Serve the cockpit in a browser on localhost; runs until interrupted |
| `upkeep prune [--trees\|--snapshots\|--projects\|--all] [--older-than=T] [--keep-latest=N] [--yes]` | Reclaim disposable state; dry-run without `--yes` |

Global per-command options: `--cockpit`, and (where environments are
involved) `--projects-root`. Every command answers on the
[0/1/2 exit-code contract](#exit-codes). Run `upkeep help <command>` for the
full text of any command.

## Upgrading an existing cockpit

Nothing needs rebuilding, but two one-off housekeeping steps are worth doing
on a cockpit created before the caches were tightened:

```bash
chmod -R go-rwx <cockpit>/results <cockpit>/cache
```

New files under `results/` and `cache/dashboard/` are written `0600` inside
`0700` directories, but files already on disk keep the mode they were created
with until they are re-written. The command above strips all group and other
access from what is already there, in one pass. (Both directories are created
on demand, so on a cockpit that has never run a `check` or a `dashboard` there
is nothing to chmod and the command reports them missing — that is fine.) If
your cockpit is in version control, add `results/` and `cache/` to its ignore
file.

`.upkeep-env.yml` gained an optional `last_used_at` key (see
[Disk housekeeping](#disk-housekeeping)). Existing environments parse
unchanged and need no rebuild.

## Design

The architecture — the adapter boundary that keeps engine specifics out of
the orchestrator, the base-artifact cold-start strategy, the fixture model,
and the DA-policy analysis behind the fast lane — is written up in
[docs/contrib-maintainer-design.md](docs/contrib-maintainer-design.md).

## License

MIT.
