# CLAUDE.md — upkeep

Maintenance orchestrator CLI for contributed Drupal modules (PHP >= 8.2,
Symfony Console). User-facing docs: `README.md`; architecture and rationale:
`docs/contrib-maintainer-design.md`.

## Layout

- `bin/upkeep` — the console entry point **and the composition root**: it
  registers every command and is the only file outside `src/Adapter/` allowed
  to name a concrete engine. It builds one `Adapter\DdevContribAdapterFactory`
  and injects it into the commands that need an environment.
- `src/Adapter/` — the engine adapter: everything ddev / ddev-drupal-contrib
  specific (provisioning, MR checkout, patch application, check execution,
  teardown, the ddev-upkeep fixture add-on). Engine pinned:
  ddev-drupal-contrib 1.1.5 (`Adapter\EngineAddOn`). Commands receive
  `Adapter\EngineAdapterFactory` and never construct an engine themselves.
  Two operations put the working copy on a branch of upkeep's own —
  `applyMr` (`mr-<iid>`) and `applyPatch` (`patch-<nid>`) — and
  `Adapter\ProjectRegistration` guards the one collision the layout makes
  structural: engine project names are global to the machine while the projects
  root is configurable, so a moved root collides with whatever the old one
  registered. Checked *before* provisioning does any work (the engine only
  raises it at `ddev config`, after a seeded codebase and a cloned repo), and
  the engine's own refusal is translated for the case `describe` cannot see —
  the record survives, the directory it names is gone. Both messages name the
  recovery command and say that it deregisters rather than deletes.
  `Adapter\ManagedBranch` names both prefixes so base-branch resolution
  rejects either as a base. A managed branch used as a base would silently
  stack one contribution on another. `applyPatch` commits what it applies
  (checks must run against a clean tree) and resets the branch from the base
  every time (a re-roll is tested alone, not stacked on the last one). Applying
  escalates: straight, then `--3way`, then `-C1` (reduced context). The third
  rung is load-bearing — drupal.org generates patches against an export whose
  files may carry a trailing blank line the repository does not, so a hunk
  header promises seven context lines for a six-line file and git refuses the
  whole patch over one of them; without `-C1` essentially every Project Update
  Bot patch reads as stale. A non-exact rung succeeding is logged, because a
  hunk placed on one line of context is a weaker guarantee. Only when all three
  fail is it an `AdapterException`, built from `git apply --stat` and
  `--check -v` so the message names which files are stale and what context git
  could not find.
- **phpstan and phpcs run from inside the module, as CI does.**
  `Adapter\CheckScript`. Both discover their configuration from the *current
  working directory*, and upkeep ran them from the project root — where the
  only config is the gitlab_templates default it had just downloaded. A module
  shipping its own `phpstan.neon` or `phpcs.xml.dist` had its level, baseline,
  ignores and ruleset silently ignored. CI does the opposite deliberately:
  `.phpstan-base` opens with `cd $DRUPAL_PROJECT_FOLDER` and fetches the
  template only in the `else`; `.phpcs-base` opens with `cd $CI_PROJECT_DIR`
  and looks for `{.,}phpcs.xml{.dist,}` first. Two consequences worth knowing:
  `--autoload-file` becomes load-bearing once the working directory is not the
  project root (PHPStan resolves Drupal through the site autoloader and cannot
  find it from inside the module — CI passes it for the same reason), and the
  fallback config is written **at the project root, never into the module**.
  CI writes it beside the code because the container is disposable; here the
  module directory is a git checkout whose cleanliness the next `applyPatch`
  or `startWork` refuses on, so two untracked files there would break the tool.
  **No shell variable of upkeep's own, no `cd`, one line.** All three are paid
  for. `ddev exec` re-joins its arguments and hands the result to a shell that
  expands the string *before* the container's shell runs it, so an assignment
  and its use in one command cannot work — `ROOT=$(pwd) && … "$ROOT/x"` dies
  with `ROOT: unbound variable`, as an earlier `MODULE=…` did. Only variables
  already in the container's environment (`$DDEV_DOCROOT`,
  `$DRUPAL_PROJECTS_PATH`) survive, which is why the long-standing scripts
  worked and two rewrites did not. The `cd` went for a different reason: CI
  runs these from inside the module because there the module repo root *is*
  where composer put `vendor/`, while under ddev-drupal-contrib `vendor/` is
  at the project root — so from inside the module every vendor-relative path
  in a ruleset breaks (`Referenced sniff "./vendor/drupal/coder/…" does not
  exist`, on a real module). So the working directory stays at the project
  root and the module's config is *named* rather than discovered. Which one
  applies is decided in PHP from `test -f` probes run in the tool's own
  precedence order, and guarded steps are braced because `&&` and `||` bind
  equally left-to-right. Two tests hold the invariants, because nothing in the
  suite executes what these build: no variable outside the container's
  environment, and no `cd`. **And using a module's configuration means
  installing what it references**: `Adapter\ModuleDevRequirements` reads the
  module's own `require-dev` and provisions it alongside the toolchain,
  because those configurations point at the *module's* packages —
  field_visibility_conditions' ruleset references
  `./vendor/phpcompatibility/php-compatibility/…`, which its composer.json
  requires and the site does not. CI has it because `composer install` runs in
  the module repository; here the module is a path repository, and **composer
  never installs a path dependency's require-dev**. A failed install warns
  rather than refusing: a version conflict in a linting dependency must not
  take down phpunit, the install check and the smoke test, and phpcs names a
  missing sniff itself. **It runs whether or not the toolchain is already
  there.** The toolchain gate returns early once phpunit/phpstan/phpcs are in
  vendor/bin, and putting this inside that block made it do nothing on every
  *existing* environment — which is every environment after the first. The
  packages are gated on their own absence from `vendor/` instead, so a reused
  environment costs a directory test rather than a composer round trip.
- **An MR is checked as CI checks it: the merge, not the branch.** GitLab
  publishes two refs per merge request — `/head` is the contributor's branch,
  `/merge` is that branch merged into the **current** tip of the target — and
  CI analyses `/merge`. `applyMr` fetched `/head`, so upkeep and CI were
  reading different trees on any merge request whose branch had fallen behind.
  Measured on pathauto: **23 of 25 open merge requests have a merge tree that
  differs from their head tree**, with branches 7 to 41 commits behind the
  target. This is not staleness a fetch can fix — an MR branch is one commit
  of work on top of the target *as it was months ago*, and the tree CI runs
  exists on neither side until GitLab computes it. Same failure as a patch on
  a stale base, through the other door. `MrCheckout::preferredRef()` picks the
  merge ref from what `ls-remote` actually advertises (one lightweight round
  trip, against a command that is about to provision an environment), and
  falls back to `/head` **loudly**: GitLab computes no merge ref for a merge
  request that conflicts with its target, so the fallback is a diagnosis, not
  a detail. **Evidence is keyed on the merge ref's SHA**
  (`Gitlab\MergeRevision`, `ModuleSnapshot::$mergeRefShas`, one
  `/merge_ref` call per open MR at refresh), because the merge tree moves when
  *either* side does: keyed on the head SHA a result would still read as
  current after the **target** gained a commit, which is the same
  evidence-about-another-tree problem one level down. No merge ref means the
  head SHA is the revision — the adapter checks the branch there too, so both
  halves fall back together. `merge --fast-lane` re-reads the merge ref at
  prompt time for the same reason it re-reads the MR. Neither ref at all is a refusal — that is a wrong iid, not a state
  to guess about. The "head moved since it was fetched" note is now conditional
  on having used the head ref: a merge commit is never the MR's head SHA, so
  comparing them would warn on every healthy run. **Cached MR results written
  before this describe the branch, not the merge** — clear
  `<cockpit>/results/` once after upgrading.
- **The base branch is fetched before anything is cut from it.**
  `Adapter\BaseRefresh` (Update by default, Skip only via `--no-update`) and
  `Adapter\BaseBranchUpdate`. A module working copy is cloned once and was
  then never fetched again on any path that cuts a branch, so its `2.0.x`
  stayed frozen at the day of the clone. That alone would only make a verdict
  old; what makes it *wrong* is that drupal.org's CI does not check your
  branch — it checks `refs/merge-requests/<iid>/merge`, your work merged into
  the **current** tip of the target. Live proof of the cost: a base sixteen
  months stale, a target since rewritten for Drupal 12 (module file moved to
  OOP hooks, a `use` import removed), and a patch that only added a function.
  Git merged it without a conflict; in the merged file the new block was the
  only remaining reference to the imported class, with no import, so it
  resolved to the global namespace. Local green, CI red, one line, no
  explanation, hours lost. So `applyPatch`, `startWork` and `promotePatch` now
  `git fetch origin <base>` and cut from `FETCH_HEAD` (never `origin/<base>` —
  a remote-tracking ref is a property of how the clone was configured;
  FETCH_HEAD is always written). The local base is fast-forwarded when it can
  be and **never reset**: one carrying local commits is left alone and said
  so, since discarding somebody's unpushed work to tidy a check is not a trade
  upkeep makes. A failed fetch is the one place here that **refuses instead of
  degrading** — every other degraded path produces a visibly reduced answer,
  while this one produces a verdict indistinguishable from a good one that
  then gets cached as evidence the fast-lane gate reads. Resuming an existing
  work branch fetches nothing; the refresh belongs to cutting a new branch.
- **Publishing goes to an issue fork, never to origin.** On drupal.org a merge
  request always comes from `issue/<machine-name>-<nid>` and is opened *across*
  projects into the canonical one — measured on pathauto, 100 of 100 open MRs
  come from a fork and none from the project. So `GitlabClient::issueFork()`
  resolves it (**null when absent, not a failure** — an unstarted issue is a
  state, not an error), `pushWork()` takes an `Adapter\GitRemote` naming where
  to push, and `createMergeRequest(..., into: $project)` sets
  `target_project_id`. The remote is `issue-<nid>`, named per issue because one
  environment serves every issue for a (module x core) and a generic `fork`
  remote would be silently re-pointed. `mergeRequestForBranch(..., from: $fork)`
  narrows by source project: branch names are `<nid>-<slug>` and so collide
  across every fork of an issue. **upkeep does not create the fork** —
  drupal.org mints it *and* links it to the issue, so it is a browser handoff
  like the issue status and the credit; publish refuses before pushing
  anything. Origin is never pushed to or altered, so fetch stays anonymous and
  read-only work needs no key.
- **Two refusals, opposite answers.** `Adapter\PushRefusal` tells
  *authentication* (GitLab does not know you — SSH key) from *authorization*
  (it knows you and says no). On drupal.org the second is the ordinary state of
  a fresh issue fork: creating one does not grant push access, which is a
  separate button on the issue. Authorization is matched first because its
  output can carry "denied" too, and it is the more specific diagnosis. An
  unrecognised refusal gets no guessed diagnosis, and git's own output is
  always kept above whatever is added. `Project::canPush()` asks the same
  question *before* pushing — **null is unknown, never "no"**, since an
  unauthenticated read omits `permissions` entirely and refusing on that would
  block pushes that would work.
- `Adapter\DrupalCodeRemote` holds the clone URL and the SSH host. Push URLs
  are **never assembled** — they are GitLab's own `ssh_url_to_repo`, because
  git.drupalcode.org serves the web and the API while the SSH remote it
  advertises is git.drupal.org; a URL built by swapping the scheme points at a
  host that does not answer. SSH rather than the PAT because **upkeep never
  hands git a credential**: every way of doing that writes the token to
  `.git/config`, to a credential store, or to argv `ps` can read, and each
  would be a second exception to the no-token-in-children rule. A refused push
  names the SSH-key recovery, against the host actually in the remote, because
  git's own message suggests a password and GitLab will never accept one.
- `src/Command/` — one class per CLI command; thin, delegating to the
  namespaces below. All extend `Command\UpkeepCommand`, which owns the shared
  option surface, the resolution seam, and the exit-code mapping.
- `src/Cockpit/` — cockpit directory + `registry.yml` module registry.
  **The registry is a watchlist, not a gate** (`docs/any-module.md`). It was
  doing two jobs — how to find a module and which cores to test it on, *and*
  which modules the surveys cover — and the first is now derivable:
  `project/<name>` is drupal.org's convention (`PruneExecutor` already assumed
  it) and the usable cores are the ones `ArtifactLayout::versionsOnDisk()`
  finds. So `Cockpit\ModuleResolution` gives **subject** commands (`check`,
  `review`, `dev`, `exec`, `env:path`, `issue`, `issues`, `needs-work`,
  `patch:*`, `start`, `publish`) any module, registered or not, while
  **survey** commands (`dashboard`, `patches`, `notes`, `modules`, `status`,
  `prune`, the UI) keep iterating the watchlist and `requireModule()` stays
  strict for narrowing into it. A registry entry always wins where there is
  one: a maintainer's `core_versions` is a deliberate statement and outranks
  anything inferred. `Module::$watched` carries that provenance, because it
  changes what a refusal can honestly say — an unavailable core is "add it to
  core_versions in registry.yml" for a watched module and "no base artifacts
  for core N, build one" for a derived one, and telling somebody to edit a
  file that does not mention their module is worse than not answering. A derived module's cores are **newest first**, because
  `selectCoreVersion()` documents `core_versions[0]` as the default and
  `versionsOnDisk()` sorts ascending — passed through unchanged, asking about
  an unregistered module answered for the *oldest* core built on the machine. What is **not** dropped is the refusal — a name that
  cannot be a Drupal machine name is refused outright, and a derived name that
  404s is offered the watched name it nearly matched
  (`ModuleResolution::projectFailure()`, shared by the two places that report
  it). That hint lives at the *failure*, not at resolution: until drupal.org
  says there is nothing there, an unheard-of name is an ordinary request.
- **Reading needs no credential.** git.drupalcode.org serves a public
  project's merge requests, refs, forks and raw files anonymously — measured
  across upkeep's whole read surface — so requiring a token to *look* was a
  restriction upkeep imposed rather than one GitLab does. `GitlabClient`'s
  token is nullable and the header is **omitted** when absent, never sent
  empty: GitLab reads a present-but-empty PRIVATE-TOKEN as a bad credential
  and answers 401. `GitlabClientFactory::readOnly()` always returns a client
  and notes the degraded mode once, naming what it costs — a private project
  answers an anonymous read with **404, not 401**, because GitLab hides
  existence, so a module you can see while signed in reads as missing. Writes
  (`merge`, `postNote`, `createMergeRequest`) refuse **in the client**, before
  any request, so no command can reach one down this path by forgetting to
  check. Which commands may take it is declared, not inferred:
  `AbstractMrCommand::readsOnly()` defaults to false and `check`/`review`
  override it, pinned by `CommandSurfaceTest` — a future write command that
  forgets gets the strict path.
- `src/Gitlab/` — git.drupalcode.org API client, token resolution
  (`UPKEEP_GITLAB_TOKEN` env, else `~/.config/upkeep/drupal-pat`), MR/pipeline
  models, and the sealed `Gitlab\ApiFailure` taxonomy (`Unauthorized` 401,
  `EndpointClosed` 403, `NotFound` 404, `RateLimited` 429, `RequestRejected`
  other 4xx/5xx, `MalformedResponse`, `TransportError`, `ResourceMissing`).
  Client methods return an `ApiFailure` rather than throwing, so callers
  `match` on the condition. Never log or print a token.
- `src/Drupal/` — drupal.org API client and issue models (status, priority,
  file attachments, MR ↔ issue references). Two api-d7 shapes are load-bearing
  and both cost an extra request: `field_project_machine_name` exists on
  *project* nodes only, so an issue listing is filtered by the project's node
  id (resolved once per project per run) and never by machine name — filtering
  a listing by machine name matches nothing and reads exactly like "no issues";
  and an attachment is always returned as a bare reference
  (`{"file":{"uri":…,"id":…}}`) with no name, so `DrupalOrgClient` dereferences
  each one against `/file/<fid>.json` (once per distinct file per run) before
  building the `Issue`. Without that, every issue has zero attachments. Those
  lookups run `DrupalOrgClient::MAX_CONCURRENT` (8) at a time — measured ~50ms
  per request against ~530ms serialised on a cold cache. Concurrency is by
  *creating* requests before consuming any, never `stream()`: that yields HTTP
  errors by throwing out of the generator, where a per-response catch cannot
  reach them, so one 404 attachment would take the whole batch down.
  **The client never fails loudly and never fails silently either**: every
  request that does not answer is recorded in `warnings()`, because returning
  less data is indistinguishable at the call site from there *being* less data
  — the exact under-reporting the patch surface exists to prevent. Commands
  surface it through `UpkeepCommand::reportScanWarnings()`. Both clients set
  `max_duration` as well as `timeout`, because Symfony's `timeout` is the
  *idle* timeout and bounds nothing on its own.
- `src/Gate/` — fast-lane gate classification (READY-AUTO / REVIEW / BLOCKED).
  **`BLOCKED` is set by red CI and by nothing else** — the gate never inspects
  mergeability, so it does not mean merge conflicts whatever older docs said.
- `Dashboard\Guidance` turns a row into a plain-English status and the command
  to run for it, which is what the dashboard renders by default; the gate's own
  reason tokens move behind `-v`. The *order* the reasons are considered is the
  design: several apply at once and only one phrase can show, so they rank by
  what blocks progress — nothing upkeep can fix, then your evidence the work is
  wrong, then missing evidence. **Every row yields a command** — red CI and
  draft are *modifiers* on the status, not reasons to suggest nothing, since
  both are exactly when a maintainer wants the branch locally; a property test
  over every subset of gate reasons holds that. `Command\Glossary` +
  `upkeep explain` define every term the tool prints, which nothing did before:
  the patch arrow existed only in a source comment. The overview's columns use
  the same words as the rows they summarise — `PATCH ISSUES` (not `PATCHES`,
  which would collide with a row's file count) and `CI FAILED` (not `BLOCKED`,
  which is what that verdict is actually set by). There is no `REVIEW` column:
  it counted everything neither ready nor CI-failed, i.e. every row.
- **An MR belongs to the issue its fork was made for.**
  `IssueReference::fromForkPath()` reads the nid out of `issue/<module>-<nid>`,
  and `GitlabClient::issueForkNids()` maps every fork of a project in one
  request (source-project id => nid) rather than one per MR. It outranks every
  other signal in `Contribution::pair()` because it is a fact about how the
  repository came to exist. It is also the **only** thing that pairs a Project
  Update Bot MR: those are titled "Automated Project Update Bot fixes" on
  branch `project-update-bot-only`, and say only "Relates to #NNN" — which
  `extractOwning()` rejects by design so a bot cannot suppress an issue's
  patches by mentioning it. Correct, and it left every bot MR paired to
  nothing.
- **"Has this been done?" for issues kept open on purpose.** Bot compatibility
  issues stay open by convention so the bot can post again, so an open one may
  have landed months ago. `mergedMergeRequests()` fetches merged MRs alongside
  open ones (without which a landed issue reads as untouched),
  `Contribution::landed()` names the merge and `hasWorkNewerThanLanding()`
  is the discriminator — anything on the issue newer than the merge is new
  work. **It never says "resolved" and never changes a status**: both readings
  are statements about evidence, and closing is a judgement about the
  convention. Live proof of the two shapes: conditions_helper #3596502 (active,
  MR merged 2026-06-12) vs field_visibility_conditions #3598272 (needs review,
  open draft). **Both views** carry it, through one implementation:
  `Contribution::renderMergeRequestCell()` is static because
  the dashboard row renders the same cell and only sometimes holds an `Issue`.
  A landing outranks every other reading of the row. It is a fact about the
  *issue*, not about a merge request — the row a maintainer is staring at is
  usually the bot's open draft while the work that landed came from a different
  MR entirely — which is why a row keyed on the issue gets it for nothing.
  `ModuleSnapshot` carries `mergedMrData` and `forkNids`; a snapshot written
  before they existed reads as "nothing known to have merged", i.e. the old
  behaviour.
- `Dashboard\LocalEvidence` is what a row knows locally **across every core
  that applies**, now that core is evidence rather than identity. Several
  cores can disagree and the cell is one string, so **worst case wins and
  names its core** (`fail 10`, `stale 10`, `pass 11 · ? 10`); every core is
  listed under `-v`. `FastLaneGate::classify()` takes the applicable cores and
  this, and is stricter than what it replaced: **every** applicable core must
  be green, where the old (subject x core) rows let a merge request green on 11
  and unchecked on 10 present a READY-AUTO row that the fast lane then took.
  `attentionCore()` is where a row's NEXT command gets its `--version=`, so the
  cell and the command cannot name different cores. Its `$byCore` is typed
  `array<array-key, …>` deliberately — PHP stores `"10" => x` as `10 => x`, so
  no caller can supply string keys and declaring them would be a type false at
  every call site.
- **A branch supports several cores at once.** `Drupal\CoreCompatibility`
  reads `core_version_requirement` from a branch's info.yml (fetched with
  `GitlabClient::fileContents()`, one request per branch a row could sit on)
  and answers which tracked cores apply. `ModuleSnapshot::$coreConstraints`
  caches the **raw constraint**, not a resolved answer — the registry's tracked
  cores can change between the fetch and the read, and a snapshot holding
  "10,11" would be answering a question nobody had asked yet.
  `RowFactory::applicable()` narrows per row, which matters more now the fast
  lane needs *every* applicable core green: an unchecked core the branch never
  claimed would deny a merge on its own.
  Measured live: pathauto's *single* 8.x-1.x declares `^10.2 || ^11 || ^12`,
  so treating core as part of a row's identity multiplied every row by a test
  matrix while saying nothing new — see `docs/dashboard-row-model.md`. Matching
  uses composer/semver by interval intersection, not a regex over majors:
  `^10.2` does declare core 10, and only an interval gets that right. **Null is
  "cannot tell", never "supports nothing"**, and an empty intersection returns
  the tracked set unchanged — a module vanishing from the dashboard is the
  worst failure mode this tool has. `MrContextResolver` also **refuses a core
  the merge request's target branch does not declare** — checking a branch on
  a core it never claimed fails at composer resolution and reads as though the
  contribution is broken. That matters more now the core can be inferred from
  the disk for an unregistered module: without it upkeep would pick a core and
  then blame the module for it. The suggestion names only cores that are both
  declared *and* built here. Unreadable info.yml, unparseable constraint,
  closed endpoint: all silence, because refusing on not-knowing blocks work
  over a file that merely failed to fetch.
- **A patch belongs to the branch its issue is filed against.**
  `Drupal\IssueVersion` turns the issue's version into a base branch and
  `PatchApplication::$baseBranch` carries it to the adapter, which prefers it
  over whatever the working copy sits on — without it a 2.0.0 issue's patch was
  applied to the default 1.0.x and read as needing a re-roll. **It never
  parses authoritatively**: it proposes candidates most-specific-first
  (`2.0.0` → `2.0.x` → `2.x`) and the caller intersects them with
  `GitlabClient::branchNames()`, because the field holds whatever anyone typed
  — sampled live: `2.0.0`, `8.0.x-dev`, `4.6.x-dev`, `5.1`, `6.14`, and `x.y.z`
  56 times. Every failure (no version, no token, unreadable branches, no match)
  falls back to the old behaviour and says so; none refuses, because this
  exists to be right more often, not to add a way to be stopped.
- `upkeep issues` reads merge requests from the dashboard snapshot when there
  is one and **live when there is not**. It used to be cache-only, to spare a
  never-refreshed cockpit "a credential error" — anonymous reads removed that
  constraint, and the watchlist split made the gap harmful: an unwatched module
  has no snapshot and cannot be given one (`dashboard --refresh` surveys the
  watchlist), so every issue looked unclaimed and NEXT said `upkeep start` on
  work somebody had already done. The fork map is fetched with the merge
  requests, because it is the only thing that pairs a Project Update Bot MR to
  its issue. Any failure costs the CONTRIBUTION column, never the list.
- **The issue loop** (`issues` / `start` / `publish`) is the entry point the
  tool lacked: every other verb begins at a contribution, so writing a fix
  happened outside it. `Drupal\IssueStatus::open()` is the canonical scan —
  the old Needs Review + RTBC pair is `awaitingReview()` and saw 42 of
  pathauto's 93 open issues. `Adapter\IssueBranch` names a maintainer's own
  branch to drupal.org's `<nid>-<slug>` convention; it is **not** an
  `Adapter\ManagedBranch`, and the distinction is load-bearing: `mr-<iid>` and
  `patch-<nid>` are reset with `checkout -B` on every apply, which against a
  work branch would destroy commits held nowhere else. The two are disjoint by
  construction (no managed prefix starts with a digit; a work branch always
  does), `startWork()` never resets, `pushWork()` never forces, and
  `MrCheckout::resolveBaseBranch()` refuses a work branch as a base — cutting a
  disposable branch from it would test the contribution *plus* unpushed work
  and report a verdict on the contribution alone. `publish` opens merge
  requests and never merges: proposing work for review is the opposite of the
  risk the one-approval-per-merge stance manages.
- `src/Patches/` — the patch-contribution surface. `Patches\PatchSelector`
  decides *which* patch on an issue was meant (`--file` pins, `--latest` takes
  the newest, one candidate settles itself, several are `ambiguous` so the
  command can prompt — an unmatched `--file` is a refusal, never a fallback);
  `Patches\PatchFetcher` is the download boundary (http(s) only, name reduced
  to a safe basename, body sniffed for a diff header, size capped) and the only
  thing that writes a patch to disk. Also holds how an issue's work arrived
  (`Patches\ContributionKind`:
  patch-only / patch + MR / patch with an empty MR / MR-only / nothing) and the
  issue-plus-its-MRs pairing `patches` renders. An MR counts as covering an
  issue only when it claims authorship of it
  (`Drupal\IssueReference::extractOwning`, which unlike `extract()` rejects a
  bare "Relates to #NNN" mention) **and** carries changes
  (`Gitlab\MergeRequest::carriesChanges()`, which keys on
  `diff_refs.base_sha != head_sha` — `changes_count` is null on an empty MR
  and `detailed_merge_status` reads `draft_status` for a draft, so both lie).
  Unknown emptiness always reads as real work; nothing may treat it as empty.
  `Patches\PatchAttribution` is the commit message a *promoted* patch travels
  under (`patch:promote`): promoting is the one operation here that moves
  another person's work into history under whoever pushes it, so the message
  names the account that posted the file, in drupal.org's own
  `Issue #NNN by author: Title` convention — which `IssueReference` already
  parses, so the resulting MR pairs with its issue by the ordinary rule.
  **No `--author`, no `Co-authored-by:`, ever**: both want an email, api-d7
  publishes a username and a profile URL and no address, and a synthesised one
  would put a guess about somebody's identity into permanent history. An
  unresolvable author is warned about and the commit says the work is not the
  promoter's — silence there is the exact misappropriation the attribution
  exists to prevent. The author costs one extra request and no more: the file
  resource's `owner` reference is already in the payload the client
  dereferences for the filename (`IssueFile::$ownerUid`), so only
  `DrupalOrgClient::user()` is new work, once per promotion.
  `check --working-copy` runs the same suite against whatever the working copy
  holds, needing no MR and no token, and **caches nothing**: every other
  verdict is keyed by a subject and a revision so staleness is detectable, and
  a working copy has neither — an entry keyed on a guess would put
  permanently-fresh evidence in front of the fast-lane gate. It closes a
  dangling instruction: `start` had been telling people to run
  `upkeep check <module> --branch`, a flag that was never built.
  `patch:promote` stops at the commit — `publish` is the outward-facing half,
  and it routes through `EngineAdapterInterface::promotePatch()`, which
  delegates to `startWork()` rather than reimplementing it: a work branch may
  hold the only copy of something, so it must never meet `applyPatch`'s
  `checkout -B`.
- `src/Dashboard/`, `src/Results/` — dashboard row assembly, cached check
  results (`<cockpit>/results/`). The dashboard is two-tier: bare `dashboard`
  renders `Dashboard\ModuleSummary` (one line per module), naming a module or
  passing `--all` renders the rows. The summary is aggregated from exactly the
  rows the drill-down would print, never recounted from the underlying data —
  two counts of the same thing that can disagree are worse than one. **A row is
  one issue's work on one module branch** — identity is `(module, issue,
  branch)` and evidence is per core (`docs/dashboard-row-model.md`). That
  removed two multipliers which added rows without adding information: an issue
  appeared once per merge request *and* again as a patch row, which is why
  `RowFactory` carried a filter whose only job was suppressing the duplicate it
  had just made; and every row was multiplied by the module's tracked cores
  even though a branch supports several at once. The one multiplier left is
  real — an issue backported to two branches is two pieces of work — and a
  merge request claiming no listed issue keeps a row of its own, which 33 of
  pathauto's 162 do. Subjects (MRs, patch issues) are still counted once per
  module. A row with no *open* merge request carries no `GateVerdict`, so
  `isReadyAuto()` is false for it *by construction* rather than by a check
  someone has to remember. `RowAssembler` has no snapshot and so groups
  nothing — one row per merge request, which is what the fast lane wants; the
  *classification* is identical, which is the part that must not drift.
  The ISSUE cell used to be filled by a per-merge-request drupal.org lookup —
  **155 requests on a pathauto refresh**, plus an attachment lookup per file on
  each. A row *is* an issue now and takes it from the open-issue scan the
  snapshot already holds, so that fetch and `ModuleSnapshot::issue()` are gone.
  Verified end to end against live pathauto data rather than fixtures
  (`docs/dashboard-row-model.md` §7): 244 rows became 115, and of the 48
  merge requests left unpaired, 46 name an issue that is genuinely closed and
  2 name none — no pairing misses.
  `Results\ResultKey` keeps MR and patch results in separate path namespaces
  (`<iid>` vs `patch-<nid>`) — both subjects are identified by a number and
  nothing keeps the ranges apart, and a patch verdict
  read as an MR verdict would put unmergeable evidence in front of the
  fast-lane gate. Patch results are keyed by `Patches\PatchRevision` (a hash of
  the patch's source URL, not its bytes — the dashboard must judge staleness
  from the attachment list without downloading anything).
- `src/BaseArtifact/` — per-core base tree + clean-install dump build/scan.
  Lifecycle in full (building, rebuilding, what propagates, sharp edges):
  `docs/base-artifacts.md`.
  **A core major in pre-release is built deliberately, and everything after
  that follows it.** `drupal/recommended-project:^12` resolves to nothing
  while 12 is in alpha — packagist carried exactly one 12.x release when this
  was written — and composer says only that no matching version was found.
  That is not an edge case for this tool: a Drupal major spends months in
  alpha and beta, and *that is when compatibility work happens*, since the
  Project Update Bot merge requests the fast lane exists to merge are about
  the unreleased core. `BaseArtifact\CoreConstraint` holds the whole answer.
  `--stability` is a **flag, never a fallback**: substituting a pre-release
  when a stable constraint finds nothing would make every later verdict a
  statement about a tree nobody asked for, and a base artifact set is the one
  place a silent substitution is least acceptable. A failed *stable* resolve
  gets the hint appended to composer's own words; once a stability was given
  it does not, because then the constraint is not the obvious suspect and
  repeating advice already taken buries what composer said. Nothing downstream
  takes the flag — `drupal/core-dev:^12` fails to resolve for exactly the same
  reason, so `ensureCheckToolchain()` derives the stability from the artifact
  meta's resolved `core_version` (`CoreConstraint::stabilityOf()`, which is
  composer's own `VersionParser::parseStability`). Derived rather than asked
  for again: a second flag could disagree with the tree it is installing into,
  and this way 13 and everything after it need no change here. The suffix goes
  on core's constraint only — `drupal/coder@alpha` carries no version
  constraint at all and would admit an alpha of a package with nothing to do
  with the seeded core.
  **Known sharp edge, documented rather than fixed:** `--force` removes the
  existing version directory *before* resolving, and a failed build removes
  the partial set too, so a forced rebuild that fails leaves the core with no
  artifact set at all and every environment for it unusable until one builds.
  Building to a sibling directory and swapping on success would remove it.
- `src/Maintenance/` — prune/status inventory and selection.
- `src/Workflow/` — shared MR- and patch-flow context and the exit-code contract
  (0 did what was asked / 1 the supervised work failed / 2 upkeep could not do
  the job).
- `src/Filesystem/` — the single write path (`FileWriter`: atomic
  temp-file + `rename()`, explicit modes, raises on a write that does not
  land) and `PathGuard` canonicalisation/containment. `FileWriter` holds the
  only `file_put_contents` in `src/`.
- `src/Security/` — `SecretRedactor` (scrubs credential material out of
  process output before it is logged, rendered, or cached) and
  `CredentialEnvironment` (removes `UPKEEP_GITLAB_TOKEN` from every child
  process environment).
- `src/Notes/`, `src/Config/` — release-notes drafting; config resolution.
- `src/Ui/` — the browser UI (`upkeep ui`), a **second renderer over the same
  core**, never a second source of truth: `Ui\StateBuilder` reads
  `Dashboard\RowFactory`, and actions shell out to `bin/upkeep` itself, so exit
  codes, adapter behaviour and redaction are inherited rather than
  reimplemented. `Ui\Api` is the whole request surface as one pure
  Request→Response function; `bin/upkeep-ui-router.php` is the only place that
  touches a superglobal or emits a byte. Four properties are load-bearing:
  every path (assets included) sits behind `Ui\LaunchToken`, compared with
  `hash_equals` — and a token in the URL beats the cookie, because the URL is
  the operator deliberately presenting a credential while the cookie is only
  what an earlier visit left behind; the other order made a restart
  unrecoverable, every fresh launch link being shadowed by the previous run's
  cookie. The token stays in the address bar deliberately: stripping it read as
  tidier and stranded people, since a restart mints a new one and a cleaned URL
  has nothing left to present. The cookie is not a duplicate — the page's own
  subresources and API calls carry no query string, and it brings
  `SameSite=Strict` with it. The point of any of this is that a localhost port
  is reachable by any page the operator visits, and this one has a GitLab PAT
  behind it — refusals are a flat identical 404 with exactly one exception,
  a GET of `/`, which serves a self-contained explanation because a per-run
  token plus a URL the page strips plus a silent refusal otherwise leaves a tab
  open across a restart unable to recover or to say why; and `upkeep ui`
  refuses a port that is already answering *before* minting or printing
  anything, because a second run otherwise announces a fresh URL, fails to
  bind, and leaves the port replying with the previous run's token — a link
  dead the moment it was written; **the browser never supplies argv** — `Ui\Jobs\JobAction` is a
  closed whitelist of recipes whose parameters are validated into shapes they
  already had to have; jobs record their exit status to a sentinel file so they
  outlive the server (`JobStore` reconciles); and the page polls with a byte
  offset rather than holding a stream, because the built-in server has few
  workers and an offset is the only thing a client must remember across a
  refresh. The page has two views over one snapshot — contribution rows and the
  whole issue queue — and offers `start` and `publish` alongside the check
  actions. `publish` is the only action reaching outside the machine, so the
  page confirms it before asking; `start` needs no guard because it resumes
  rather than resets. **Merging is deliberately absent** — the DA stance is one human
  approval per merge and a button that POSTs an action name is not the per-MR
  prompt that earns it; that needs its own design before it needs code.
  `Ui\UiServer` is the single documented exception to the
  credential-scrubbing invariant (see
  `tests/Security/ProcessEnvironmentInvariantTest`), because its children are
  upkeep itself; served job output is redacted again on the way out regardless.
- **Shell completion** — `upkeep completion <shell>` is Symfony Console's own,
  and command names complete for free. What does not is *values*, so
  `UpkeepCommand::complete()` suggests the registry's module machine names for
  the `module` argument and the named module's tracked cores for `--version`.
  It **may never throw**: completion runs on every press of TAB, and an
  exception would spill a stack trace across the prompt — so an unresolvable
  cockpit or an unparseable registry suggests nothing and the ordinary run a
  moment later reports it properly. Values nothing local can enumerate (MR
  IIDs, issue nids) are deliberately not completed: that would be a network
  round trip per keystroke.
- `tests/` — PHPUnit, mirroring `src/`.

## Quality gates

Four gates, all blocking in CI (`.github/workflows/ci.yml`, on PHP 8.2, 8.3
and 8.4). Run all four before calling a change done:

```bash
composer lint      # phpcs — PSR-12 over src/, bin/, tests/
composer analyse   # phpstan analyse --no-progress — level max
vendor/bin/phpunit # the unit suite, with the 100% line-coverage floor enforced
./bin/upkeep list  # the binary still boots
```

**There is a fifth suite, and it is not one of the gates.**
`vendor/bin/phpunit` runs the `unit` suite only (`defaultTestSuite`); the
`integration` suite crosses the container boundary and needs docker, so it
runs explicitly:

```bash
tests/Integration/fixture/setup.sh /tmp/contract        # a bare ddev project
UPKEEP_DDEV_PROJECT=/tmp/contract \
  vendor/bin/phpunit --testsuite=integration --no-coverage
```

It exists because the four gates are structurally blind to a whole class of
failure: **three bugs shipped past a fully green suite in two days**, all of
them at the point where a built string meets a real container — a command that
could not survive `ddev exec`, a `cd` copied from CI whose precondition
ddev-drupal-contrib does not meet, and a dependency the path-repository layout
never installs. Unit tests inspect what the adapter builds; nothing in them
executes it. These do. The fixture is deliberately *not* a Drupal site: every
fact under test is about the layout, and a bare ddev project starts in about a
minute where installing Drupal takes fifteen — which is the half that makes
container CI flaky and then ignored. `.github/workflows/integration.yml` runs
the docker and no-docker jobs per PR, and fails if the contract tests skip
themselves, because a job that goes green having executed nothing is the hole
this closes.

**`.github/workflows/full-check.yml` is the nightly tier**: it provisions a
real Drupal site and runs `check --working-copy` twice (the second run is the
reused-environment path, which is where the toolchain gate silently skipped
work) and `patch:check` against a real patch. It needs **no credential and
writes nothing** — `check --working-copy` touches no GitLab client at all, and
the patch surface is GitLab-free by design and degrades to null without a
token, so no merge request is opened and no branch pushed. `publish` is the
only command that writes, and its git half is covered offline in
`tests/Integration/PublishPushTest.php` against a `file://` bare repository —
including the `pre-receive hook declined` a fresh issue fork gives you, which
a hook reproduces on demand and the real thing does not. What the nightly
asserts is the **exit-code contract, not the verdict**: 0 and 1 both mean
upkeep worked, and only 2 fails the job. Pinning an outcome would turn a
re-rolled patch into a red build.
Integration tests are excluded from the coverage floor: they exercise a
fraction of `src/` by design, and running them under the threshold extension
would either fail the build or force the floor down.

**Run `composer install` after pulling.** Adding a dependency is a one-line
change to composer.json that leaves every existing checkout broken until it
reinstalls. That is not hypothetical: `composer/semver` was added, and the
next `dashboard --refresh` on a stale checkout died with an uncaught
`Class "Composer\Semver\VersionParser" not found`. No gate caught it —
`./bin/upkeep list` boots the application and reaches no dependency-using
path, so it exited 0 with the package gone, and the suite runs where every
package is present by construction. `Workflow\RuntimeRequirements` now
compares composer.json's `require` against `Composer\InstalledVersions` at the
top of `bin/upkeep` and refuses with exit 2 and the recovery command, so the
boot gate does catch it — and reads composer.json rather than a hand-kept
list, since a hand-kept list is what the offending commit would have forgotten
to update.

**`composer.lock` is committed, and `config.platform.php` is pinned to
`8.2.0`.** Upkeep is cloned and run, not required as a library, so the lock is
the artefact that says what a checkout should hold. The platform pin is what
makes that safe: without it, resolution follows whatever PHP the resolver
happens to be on, and a lock built on 8.4 pulled in Symfony 8.1 — which
requires `php >=8.4.1` and would have failed `composer install` outright on
the 8.2 and 8.3 CI legs. It was already causing quieter trouble: local runs
were on Symfony 8.1 while CI's 8.2 leg resolved 7.x, so the four gates were
being run against a dependency tree no user had. Pinned to the lowest
supported version, everyone installs the same tree. The `|| ^8.0` half of the
Symfony constraints is therefore never exercised by `composer install`; only
an explicit `composer update` on PHP 8.4 reaches it.

`composer lint:fix` (phpcbf) fixes what phpcs can fix automatically. CI also
runs `composer validate --strict` before installing, so touching
`composer.json` means re-running that too.

### Running tests

```bash
vendor/bin/phpunit
```

Fast (seconds), no network, no docker: engine interactions are tested against
fakes of `Adapter\EngineAdapterInterface` and `Adapter\EngineAdapterFactory`,
GitLab via mocked HTTP.

**The 100% line-coverage floor is enforced.** A run below it prints
`FAILURE: line coverage …% (…) is below the required minimum of 100.00%` and
exits 1. PHPUnit 11.5 has no built-in minimum-coverage option, so the gate is
a PHPUnit extension — `tests/Support/CoverageThresholdExtension.php`,
registered in `phpunit.xml.dist` rather than passed as a CI flag, so a bare
`vendor/bin/phpunit` enforces it exactly as CI does. Current state: 100.00%
lines (7352/7352), methods (847/847) and classes (162/162), 1551 tests.

Coverage requires a driver — PCOV (preferred; faster, line-coverage only) or
Xdebug (accepted; also supports branch coverage). Check with
`php -m | grep pcov`; if PCOV isn't auto-enabled in your `php.ini`, pass
`-d pcov.enabled=1`. For the per-namespace table plus the overall summary:

```bash
vendor/bin/phpunit --coverage-text
```

An HTML report is written to `build/coverage-html/` on every coverage-enabled
run. **Known limitation:** with `--no-coverage`, or with no driver installed,
there is nothing to measure — the extension warns loudly on stderr
(`WARNING: the 100.00% line-coverage threshold was NOT enforced`) and exits 0
rather than making the suite unrunnable. Never take a green run at face value
without checking that warning is absent.

## Hard rule: the adapter boundary

Engine specifics (ddev, ddev-drupal-contrib, docker, container/volume names)
live **only** in `src/Adapter/`. The rest of the orchestrator talks to
`EngineAdapterInterface`, obtained from an injected `EngineAdapterFactory`,
and must stay engine-agnostic. Guard:

```bash
grep -ri "ddev" src/ --exclude-dir=Adapter
```

must return nothing. **The `-i` is load-bearing.** The guard was previously
written case-sensitively, and passed only because five command classes
constructed `DdevContribAdapter` with a capital D — nominally satisfied,
substantively breached. Engine selection now happens once, in `bin/upkeep`.
If a change needs engine knowledge outside `src/Adapter/`, grow the adapter
interface instead.

## Conventions

- Commands resolve the cockpit as `--cockpit` > `UPKEEP_COCKPIT` > cwd, and
  the projects root as `--projects-root` > `UPKEEP_PROJECTS_ROOT` >
  `<cockpit>/projects` (when that directory exists) > `~/.upkeep/projects`.
- The resolved projects root — and `base-artifacts:build --scratch-dir` — must
  canonicalise to a path under `$HOME`, or the command is refused with exit 2.
  This is functional, not just hardening: the tree is bind-mounted into the
  Docker VM and macOS providers only share the home directory, so an
  environment outside it can never start. Containment is checked on the
  canonical path (`Filesystem\PathGuard`), so `..` and symlink escapes are
  caught. **There is no escape hatch**; adding one is a decision, not a patch.
- `--version` is *always* the target-core selector, never an app-version flag
  — on check/review/dev/exec/env:path/needs-work, as a filter over assembled
  rows on dashboard, and on `base-artifacts:build` (which used to spell it
  `--core`). The application-level `-V/--version` is deliberately removed in
  `bin/upkeep`.
- The one MR-IID rule lives in `UpkeepCommand::mrIid()`: a positive integer,
  or exit 2. `!0` is not a merge request, so it is refused rather than turned
  into a confusing 404.
- Exit codes are a CLI-wide contract, mapped once in
  `Command\UpkeepCommand::execute()` and expressed by `Workflow\ExitCode`:
  **0** the command did what was asked, **1** the work it supervised failed,
  **2** upkeep could not do the job. Commands return `ExitCode::*` and never
  Symfony's `Command::SUCCESS`/`FAILURE`/`INVALID`, whose numbers collide with
  the contract while meaning something else. Throwing a domain exception
  (`WorkflowException`, `AdapterException`, `RegistryException`,
  `FilesystemException`, `BuildException`, `MetaException`) is the documented
  way to report a 2.
- Every write goes through `Filesystem\FileWriter` — atomic, checked, explicit
  mode. Do not add a raw `file_put_contents`; a discarded return value is how
  `init` used to report "Cockpit created" over a registry that never landed.
  Caches holding token-scoped remote data or raw check output are `0600` in
  `0700` directories (`FileWriter::MODE_PRIVATE`/`MODE_PRIVATE_DIR`).
- `UPKEEP_GITLAB_TOKEN` is never forwarded to a child process
  (`Security\CredentialEnvironment::scrubbed()`), and process output is passed
  through `Security\SecretRedactor` before it is logged, rendered or cached.
- **Every suppression annotation requires a written justification** — a
  comment on the line above saying what is being suppressed and why it cannot
  be fixed. That applies to `@codeCoverageIgnore`, `@phpstan-ignore`,
  `phpcs:ignore`, `phpcs:disable` and a `phpstan-baseline.neon`. The repository
  currently has **zero** of them, and no baseline file; that is the state to
  preserve. Verify with:

  ```bash
  grep -rn "codeCoverageIgnore\|phpstan-ignore\|phpcs:ignore\|phpcs:disable" src/ tests/ bin/
  ```

  Prefer restructuring the code over suppressing the tool. An untested error
  path is exactly the kind that reports success over a failure.
- Merges are one-human-approval-per-MR by DA policy; never add a batch or
  unattended merge path (see README "Policy stance"). Asserted structurally by
  `tests/Command/CommandSurfaceTest.php`.
