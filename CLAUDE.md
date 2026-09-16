# CLAUDE.md — upkeep

Maintenance orchestrator CLI for contributed Drupal modules. One static Go
binary (Go >= 1.24, cobra). User-facing docs: `README.md`; architecture and
rationale: `docs/contrib-maintainer-design.md`.

This was a PHP application until it was rewritten in Go; the PHP was the
specification for that port and is now only in git history. Where this file
says why something is the way it is, the reason usually predates the rewrite
and was learned the hard way — see `docs/go-port.md`.

## Layout

- `cmd/upkeep` — the console entry point **and the composition root**: it
  registers every command and is the only file outside `internal/adapter/` allowed
  to name a concrete engine. It builds one `adapter.DdevContribFactory`
  and injects it into the commands that need an environment.
- **The fixture add-on's command names are one constant, checked against the
  add-on itself.** `adapter.FixtureAddOn*::LOAD_COMMAND` is
  `upkeep-fixture-load`, and `MARKER` is derived from it (ddev names a host command after the file it came
  from). They used to be two independent strings and they disagreed: the
  adapter ran `ddev upkeep-fixture-load` while owenbush/ddev-upkeep has always
  published `fixture-load` — its README, bats tests and recorded end-to-end run
  all use the short name, and the `upkeep-` prefix appears there only on
  snapshot names and environment variables. So **`--fixture=NAME` could never
  have worked**, and the installed-probe looked for a file that is never
  written, re-fetching the add-on on every call. The unit suite was green and
  could not have been otherwise — the engine fake records whatever string it is
  handed, and the marker fixture is built from the same constant under test, so
  both halves agreed with each other and with nothing real.
  `internal/adapter/fixtureaddon_test.go` reads the add-on's own
  `install.yaml` and compares. It takes a local checkout via
  `UPKEEP_ADDON_SOURCE` first and an API read otherwise; the read needs no
  credential now the add-on is public, so its CI step is unconditional and a
  skip is a failure. **The release is pinned** (`FixtureAddOn::VERSION`), as
  `EngineAddOn` pins ddev-drupal-contrib: a published add-on with no pin means
  every environment tracks whatever its latest release happens to be, so a
  breaking change over there arrives everywhere at once — the command rename
  below would have been exactly that. The install probe is the command file
  **and** a version stamp (`FixtureAddOn::STAMP`), because a file's presence
  says nothing about which release wrote it; the marker alone would pin every
  existing environment to whatever it installed first. An override
  (`UPKEEP_ADDON_SOURCE`) pins no version — a checkout has no release — and
  stamps its source, so moving between a checkout and the release re-installs. The commands are **namespaced**
  (`upkeep-fixture-*`) because ddev gives every add-on's host commands one flat
  namespace per project, so a name as general as `fixture-load` claims ground
  this add-on has no business claiming. That rename lands in the add-on first:
  the probe looks for the command *file*, so upkeep expecting a name the
  installed add-on does not publish reinstalls it on every call and then fails.
- `internal/adapter/` — the engine adapter: everything ddev / ddev-drupal-contrib
  specific (provisioning, MR checkout, patch application, check execution,
  teardown, the ddev-upkeep fixture add-on). Engine pinned:
  ddev-drupal-contrib 1.1.5 (`internal/adapter/addon.go`). Commands receive
  `adapter.Factory` and never construct an engine themselves.
  Two operations put the working copy on a branch of upkeep's own —
  `applyMr` (`mr-<iid>`) and `applyPatch` (`patch-<nid>`) — and
  `adapter.ProjectRegistration` guards the one collision the layout makes
  structural: engine project names are global to the machine while the projects
  root is configurable, so a moved root collides with whatever the old one
  registered. Checked *before* provisioning does any work (the engine only
  raises it at `ddev config`, after a seeded codebase and a cloned repo), and
  the engine's own refusal is translated for the case `describe` cannot see —
  the record survives, the directory it names is gone. Both messages name the
  recovery command and say that it deregisters rather than deletes.
  `adapter.IsManagedBranch` names both prefixes so base-branch resolution
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
  `internal/adapter/ddev_checks.go`. Both discover their configuration from the *current
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
  installing what it references**: `adapter.ModuleDevRequirements` reads the
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
  the module's `vendor/bin`, and putting this inside that block made it do
  nothing on every
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
  (`gitlab.MergeRevision`, `ModuleSnapshot::$mergeRefShas`, one
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
  `adapter.BaseRefresh` (Update by default, Skip only via `--no-update`) and
  `adapter.DescribeBaseUpdate`. A module working copy is cloned once and was
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
  state, not an error), `pushWork()` takes an `adapter.GitRemote` naming where
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
- **Two refusals, opposite answers.** `adapter.PushAuthorizationHelp` tells
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
- `internal/adapter/remote.go` holds the clone URL and the SSH host. Push URLs
  are **never assembled** — they are GitLab's own `ssh_url_to_repo`, because
  git.drupalcode.org serves the web and the API while the SSH remote it
  advertises is git.drupal.org; a URL built by swapping the scheme points at a
  host that does not answer. SSH rather than the PAT because **upkeep never
  hands git a credential**: every way of doing that writes the token to
  `.git/config`, to a credential store, or to argv `ps` can read, and each
  would be a second exception to the no-token-in-children rule. A refused push
  names the SSH-key recovery, against the host actually in the remote, because
  git's own message suggests a password and GitLab will never accept one.
- **Child-process output is progress, not payload.** ddev, composer and git
  print through `cli.LiveStatus`: on a terminal, the latest line sits on
  one line that overwrites itself and is erased when the child exits; under
  `-v` every line is kept; anywhere that is not a terminal nothing extra is
  written, because overwriting a line in a CI log produces escape codes, not
  progress. It used to be all or nothing, and both ends were wrong for the
  same reason. All of it buried the few lines a command exists to print — a
  fresh provision is hundreds of lines and `dev`'s four-line answer scrolled
  off the top. None of it, the fix for that, made a wedged `ddev start`
  indistinguishable from a slow one: after a reboot it stalled on "Starting
  Mutagen sync process..." and upkeep said "starting it" and then nothing.
  The line is cleared from `ProcessRunner`'s `onIdle` hook, which fires in a
  `finally` as each child exits, however it exits — the one moment nothing
  can be mid-print, so a stage line or a results table never lands glued onto
  the end of it. The indicator advances only when output arrives; frozen is
  the point, since that is what waiting looks like, and a spinner on a timer
  would keep turning while nothing happened. **`env:path` and `exec` stay
  silent on purpose**: the first prints a path meant to be captured
  (`cd $(upkeep env:path …)`) and the second passes through your own command's
  output, so a status line in either would end up inside what you asked for.
- `internal/cli/command/` — one class per CLI command; thin, delegating to the
  namespaces below. All extend `internal/cli`, which owns the shared
  option surface, the resolution seam, and the exit-code mapping.
- `internal/cockpit/` — cockpit directory + `registry.yml` module registry.
  **The registry is a watchlist, not a gate** (`docs/any-module.md`). It was
  doing two jobs — how to find a module and which cores to test it on, *and*
  which modules the surveys cover — and the first is now derivable:
  `project/<name>` is drupal.org's convention (`PruneExecutor` already assumed
  it) and the usable cores are the ones `ArtifactLayout::versionsOnDisk()`
  finds. So `cockpit.ResolveModule` gives **subject** commands (`check`,
  `review`, `dev`, `exec`, `env:path`, `issue`, `issues`, `needs-work`,
  `notes`, `patch:*`, `start`, `publish`, `api:probe`) any module, registered
  or not, while **survey** commands (`dashboard`, `patches`, `modules`,
  `status`, `prune`) keep iterating the watchlist and `requireModule()` stays
  strict for narrowing into it. `notes` and `api:probe` read as survey
  commands in older prose and are neither: nothing iterates, and
  `NotesCommand::resolveProjectPath()` falls back to treating the argument as
  a project path — the one place a missing registry is deliberately not an
  error. A registry entry always wins where there is
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
- `internal/gitlab/` — git.drupalcode.org API client, token resolution
  (`UPKEEP_GITLAB_TOKEN` env, else `~/.config/upkeep/drupal-pat`), MR/pipeline
  models, and the sealed `gitlab.Failure` taxonomy (`Unauthorized` 401,
  `EndpointClosed` 403, `NotFound` 404, `RateLimited` 429, `RequestRejected`
  other 4xx/5xx, `MalformedResponse`, `TransportError`, `ResourceMissing`).
  Client methods return an `ApiFailure` rather than throwing, so callers
  `match` on the condition. Never log or print a token.
- `internal/drupal/` — drupal.org API client and issue models (status, priority,
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
  a whole-request deadline as well as an idle one: an idle timeout bounds
  nothing on its own.
- `internal/gate/` — fast-lane gate classification (READY-AUTO / REVIEW / BLOCKED).
  **`BLOCKED` is set by red CI and by nothing else** — the gate never inspects
  mergeability, so it does not mean merge conflicts whatever older docs said.
- `dashboard.Guidance` turns a row into a plain-English status and the command
  to run for it, which is what the dashboard renders by default; the gate's own
  reason tokens move behind `-v`. The *order* the reasons are considered is the
  design: several apply at once and only one phrase can show, so they rank by
  what blocks progress — nothing upkeep can fix, then your evidence the work is
  wrong, then missing evidence. **Every row yields a command** — red CI and
  draft are *modifiers* on the status, not reasons to suggest nothing, since
  both are exactly when a maintainer wants the branch locally; a property test
  over every subset of gate reasons holds that. `command.Glossary` +
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
- `results.LocalEvidence` is what a row knows locally **across every core
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
- **A branch supports several cores at once.** `drupal.CoreCompatibility`
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
  is interval intersection, not a regex over majors — computed here rather than
  by a library, and verified against 18,273 cases generated from
  composer/semver with no divergence:
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
  `drupal.BranchCandidates` turns the issue's version into a base branch and
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
  happened outside it. `drupal.IssueStatus::open()` is the canonical scan —
  the old Needs Review + RTBC pair is `awaitingReview()` and saw 42 of
  pathauto's 93 open issues. `adapter.IssueBranch` names a maintainer's own
  branch to drupal.org's `<nid>-<slug>` convention; it is **not** an
  `adapter.IsManagedBranch`, and the distinction is load-bearing: `mr-<iid>` and
  `patch-<nid>` are reset with `checkout -B` on every apply, which against a
  work branch would destroy commits held nowhere else. The two are disjoint by
  construction (no managed prefix starts with a digit; a work branch always
  does), `startWork()` never resets, `pushWork()` never forces, and
  `MrCheckout::resolveBaseBranch()` refuses a work branch as a base — cutting a
  disposable branch from it would test the contribution *plus* unpushed work
  and report a verdict on the contribution alone. `publish` opens merge
  requests and never merges: proposing work for review is the opposite of the
  risk the one-approval-per-merge stance manages.
- `internal/patches/` — the patch-contribution surface. `patches.Select`
  decides *which* patch on an issue was meant (`--file` pins, `--latest` takes
  the newest, one candidate settles itself, several are `ambiguous` so the
  command can prompt — an unmatched `--file` is a refusal, never a fallback);
  `patches.Fetcher` is the download boundary (http(s) only, name reduced
  to a safe basename, body sniffed for a diff header, size capped) and the only
  thing that writes a patch to disk. Also holds how an issue's work arrived
  (`patches.Kind`:
  patch-only / patch + MR / patch with an empty MR / MR-only / nothing) and the
  issue-plus-its-MRs pairing `patches` renders. An MR counts as covering an
  issue only when it claims authorship of it
  (`drupal.ExtractIssue::extractOwning`, which unlike `extract()` rejects a
  bare "Relates to #NNN" mention) **and** carries changes
  (`gitlab.MergeRequest::carriesChanges()`, which keys on
  `diff_refs.base_sha != head_sha` — `changes_count` is null on an empty MR
  and `detailed_merge_status` reads `draft_status` for a draft, so both lie).
  Unknown emptiness always reads as real work; nothing may treat it as empty.
  `patches.Attribution` is the commit message a *promoted* patch travels
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
  **A patch that will not apply is the start of a re-roll, not a dead end.**
  Applying escalates (straight, `--3way`, `-C1`) and only then reports; the
  report distinguishes two failures that want opposite things.
  `PatchCheckout::cutFromReleaseTarball()` reads the failing context for the
  lines drupal.org's packaging script appends to every `.info.yml` at release
  time — `version`, `project`, `datestamp`. A patch generated from an unpacked
  release carries them as *context*, no commit has ever had them, and it
  therefore cannot apply to a checkout however fresh the branch is; telling
  somebody to "re-roll against 1.0.x" sends them looking for changes nobody
  made. Found on static_setting_contexts #3603341, where the report blamed a
  file that had not moved. Either way the refusal ends by naming
  `patch:promote --partial`, which applies every hunk that still fits onto the
  issue work branch and leaves the rest as `.rej` files — the work of
  re-rolling rather than a description of it. It **never commits**: a
  promotion's commit carries the patch author's name
  (`patches.Attribution`), and half their patch plus a pile of rejects is
  not what they wrote, which is the misattribution that rule exists to
  prevent. Exit 1, not 0, because the patch did not apply and a script reading
  success would publish half a contribution. Nothing fitting at all refuses as
  before — an empty working copy beside rejects is no head start. The apply
  reads `git apply --reject`'s *output*, never its status: it exits 1 even
  when it applied most of the patch. `adapter.PatchPromotion` carries which
  outcome happened, because "committed at this SHA" and "partly applied,
  nothing committed" are different answers and a nullable string would not
  have said which.
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
- `internal/dashboard/`, `internal/results/` — dashboard row assembly, cached check
  results (`<cockpit>/results/`). The dashboard is two-tier: bare `dashboard`
  renders `dashboard.ModuleSummary` (one line per module), naming a module or
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
  `results.MergeRequestKey` keeps MR and patch results in separate path namespaces
  (`<iid>` vs `patch-<nid>`) — both subjects are identified by a number and
  nothing keeps the ranges apart, and a patch verdict
  read as an MR verdict would put unmergeable evidence in front of the
  fast-lane gate. Patch results are keyed by `patches.Revision` (a hash of
  the patch's source URL, not its bytes — the dashboard must judge staleness
  from the attachment list without downloading anything).
- `internal/baseartifact/` — per-core base tree + clean-install dump build/scan.
  Lifecycle in full (building, rebuilding, what propagates, sharp edges):
  `docs/base-artifacts.md`.
  **A core major in pre-release is built deliberately, and everything after
  that follows it.** `drupal/recommended-project:^12` resolves to nothing
  while 12 is in alpha — packagist carried exactly one 12.x release when this
  was written — and composer says only that no matching version was found.
  That is not an edge case for this tool: a Drupal major spends months in
  alpha and beta, and *that is when compatibility work happens*, since the
  Project Update Bot merge requests the fast lane exists to merge are about
  the unreleased core. `baseartifact.AssertStability` holds the whole answer.
  `--stability` is a **flag, never a fallback**: substituting a pre-release
  when a stable constraint finds nothing would make every later verdict a
  statement about a tree nobody asked for, and a base artifact set is the one
  place a silent substitution is least acceptable. A failed *stable* resolve
  gets the hint appended to composer's own words; once a stability was given
  it does not, because then the constraint is not the obvious suspect and
  repeating advice already taken buries what composer said. Nothing downstream
  takes the flag — `drupal/core-dev:^12` fails to resolve for exactly the same
  reason, so `ensureCheckToolchain()` derives the stability from the artifact
  meta's resolved `core_version` (`baseartifact.StabilityOf`, which reproduces
  composer's own `VersionParser::parseStability`). Derived rather than asked
  for again: a second flag could disagree with the tree it is installing into,
  and this way 13 and everything after it need no change here. The suffix goes
  on core's constraint only — `drupal/coder@alpha` carries no version
  constraint at all and would admit an alpha of a package with nothing to do
  with the seeded core.
  **A rebuild is staged beside the live set, never over it.** `--force` used to
  `rm -rf` the version directory and *then* resolve into the empty space, so a
  routine "pick up the newer alpha" rebuild that hit a network blip left the
  core with no artifact set at all — every environment for it unusable, the
  previous set unrecoverable. The build now resolves into
  `<base-artifacts>/.building-d<major>-<hex>/<major>/` and swaps at the end:
  the outgoing set is renamed aside, the incoming one takes the name, the
  staging directory goes. Two renames in one directory, so the exposure is
  bounded by those rather than by the minutes a resolve and a site install
  take, and a failed build costs the attempt only (the run says the existing
  set is untouched, because the other reading is that everything is gone). The
  leading dot is load-bearing: `versionsOnDisk()` matches whole numbers, so a
  build in progress is invisible to `base-artifacts:status`, to prune, and to
  the core inference that gives an unregistered module its versions — a
  half-built tree must never read as a core somebody can be offered. A staging
  directory left by a killed build is collected by the next build for that
  core; nothing else would, since prune protects the whole base-artifacts
  directory. The residual window is the two renames, and the failure messages
  name every path rather than deleting anything. There is deliberately **no**
  restore branch for a failed second rename: both renames need write
  permission on the same two directories, so it cannot be reached once the
  first has succeeded, and the message reports the staging directory as a
  whole instead of branching on a state that cannot occur.
- `internal/maintenance/` — prune/status inventory and selection.
- `internal/workflow/` — shared MR- and patch-flow context and the exit-code contract
  (0 did what was asked / 1 the supervised work failed / 2 upkeep could not do
  the job).
- `internal/filesystem/` — the single write path (`FileWriter`: atomic
  temp-file + `rename()`, explicit modes, reports a write that does not land)
  and `Canonicalize`/`IsWithin` containment. `Commit` holds the only direct
  file write in `internal/`.
- `internal/security/` — `Redactor` (scrubs credential material out of process
  output before it is logged, rendered, or cached) and `ScrubbedEnvironment`
  (removes `UPKEEP_GITLAB_TOKEN` from every child process environment).
- `internal/notes/`, `internal/config/` — release-notes drafting; config resolution.
- **There is no browser UI, and that is a decision.** `upkeep ui` served the
  cockpit as a local web page: a second renderer over the same core, actions
  shelling out to `cmd/upkeep` so exit codes and redaction were inherited
  rather than reimplemented. It was removed. Nothing forced it to track the
  core it rendered, and it fell behind twice — the row-model rework, and then
  the change that made `issues` read merge requests live when there is no
  snapshot, which its state builder never got. The result was a page showing
  no issues for every module while the CLI showed them, which is worse than
  having no page. The structural problem underneath: every action handed you
  back to the terminal the moment it failed, so it was a read-mostly view
  competing with a CLI that reads better, and the one action worth a button —
  merging — is deliberately absent under the DA one-approval stance. It is in
  git history if it is ever worth reviving; reviving it means solving "what
  makes this stay in step with the core", not porting the code.
- **Shell completion** — `upkeep completion <shell>` is cobra's own,
  and command names complete for free. What does not is *values*, so
  `UpkeepCommand::complete()` suggests the registry's module machine names for
  the `module` argument and the named module's tracked cores for `--version`.
  It **may never throw**: completion runs on every press of TAB, and an
  exception would spill a stack trace across the prompt — so an unresolvable
  cockpit or an unparseable registry suggests nothing and the ordinary run a
  moment later reports it properly. Values nothing local can enumerate (MR
  IIDs, issue nids) are deliberately not completed: that would be a network
  round trip per keystroke.
- Tests sit beside the code they cover, as `_test.go` files.

## Quality gates

Four, matching the PHP's, and all blocking in CI
(`.github/workflows/go.yml`, on Go 1.24 and 1.25):

```bash
gofmt -l cmd internal   # must print nothing
go vet ./...
./coverage.sh           # the suite, and the per-package coverage floor
go run ./cmd/upkeep --help
```

**`composer install` breaks the Go build**, and that is not a bug in either.
Go switches to vendor mode whenever a `vendor/` directory sits next to
`go.mod`, and Composer's is exactly that. `vendor/` is gitignored and CI never
sees both — the Go jobs run no `composer install` and the PHP jobs run no Go —
so it bites only a working copy that has done both. `rm -rf vendor` puts the
Go side right; `composer install` puts the PHP side back. It resolves for good
when `internal/` goes.

CI also runs `go test -race ./...`, the invariant tests named separately so a
red build is legible, and a `go mod tidy` job — which earned its place
immediately by catching cobra and yaml.v3 marked `// indirect` when both are
direct dependencies, something no amount of local testing would notice with a
warm module cache.

**The coverage floor is per package, not one number** (`coverage.sh`). This
is the port of the PHP's 100%-line coverage threshold, and being
per-package is the point: statement coverage is not line coverage, and one
overall figure lets a well-covered package pay for a bare one. Each floor is
where that package actually stands, so the only direction it moves is up — the
script fails a package that drops below its floor **and** one that rises above
it without the floor being raised, because a floor left behind stops being a
gate. Lowering one is a decision, written in that file where a reviewer sees
it. Three packages are excluded, each with a written reason. **Never lower a
floor to make a change pass**; that is the exact move the gate exists to
prevent.

**Mutation testing is the verification discipline.** After each package, break
the behaviours it claims one at a time and confirm the suite catches each. A
survivor is either a missing test — write it — or a genuinely equivalent
mutant, documented at the code. This is not ceremony: it has caught assertions
that could never fail (a table row searched for `"keep"` when every path in it
contains "up**keep**"; column offsets measured in bytes while the table pads in
runes; a whole coloriser unexercised because a test's output is not a
terminal), and each of those was invisible to reading.

## Ported from PHP

This was a PHP application, rewritten command for command in Go.
`docs/go-port.md` is the document for that: how it was done, what it found,
and the eight defects it found in the PHP on the way. Do not restate it here —
a second copy of the truth loses, which is why `docs/commands.md` is generated
rather than written.

**The corpora are the PHP's answers, and they are still the specification.**
`registry.yml`, `meta.yml`, the dashboard snapshot's raw api-d7 payloads, the
patch cache's safe filenames, the "did you mean" edit distance, and the
`core_version_requirement` semantics are each held to a committed corpus that
the PHP generated — `corpus.json` and `testdata/`. The generators went with
the implementation they drove and are in git history; the answers stay,
because a cockpit written by the old binary is still a cockpit this one has to
read.

Where the port deliberately differs from what the PHP did, the reason is **in
a comment at the code** rather than here: the stage lines that go to stderr
rather than stdout, the `--draft` prefix that is not doubled, the merge-ref
keying that the PHP got wrong in three places. Each says why where it happens.

### Structural invariants

`internal/invariant/` holds checks over the source itself, for properties no
single code path shows. They are ordinary tests, and they are stricter than the
PHP greps they replace:

- **Only `internal/proc` may start a child process.** Stricter than the PHP
  invariant, which only requires each construction to scrub the environment.
- **Engine specifics stay inside `internal/adapter`.** An AST scan for
  `{ddev, docker, colima, mutagen, drush, podman}` case-insensitively outside
  that package, skipping comments. It earns its keep: it refused a
  `--scratch-dir` help string that named Docker.

### Shapes Go forced

- **Package cycles.** PHP tolerates `Adapter ↔ Maintenance` and
  `Adapter ↔ BaseArtifact`; Go does not. Broken with neutral types
  (`adapter.ProjectVolume`) and consumer-declared interfaces
  (`baseartifact.InstallSite`, `maintenance.Teardown`, `dashboard.Reader`).
  Declare the interface where it is *used*, not where it is implemented.
- **No base class.** The PHP has twenty-one commands extending
  `internal/cli`. `internal/cli` does those jobs instead, which is the
  better shape for them: what they share is behaviour applied to a command, not
  identity.
- **One composition root.** `cmd/upkeep/main.go` builds a
  `command.Surface` and hands it to `command.NewRoot`. It and
  `adapter.DdevContribFactory` are the only places engine selection happens.

## Hard rule: the adapter boundary

Engine specifics (ddev, ddev-drupal-contrib, docker, container/volume names)
live **only** in `internal/adapter/`. The rest of the orchestrator talks to
`EngineAdapterInterface`, obtained from an injected `EngineAdapterFactory`,
and must stay engine-agnostic. Guard:

```bash
grep -ri "ddev" internal/ --exclude-dir=Adapter
```

must return nothing. **The `-i` is load-bearing.** The guard was previously
written case-sensitively, and passed only because five command classes
constructed `DdevContribAdapter` with a capital D — nominally satisfied,
substantively breached. Engine selection now happens once, in `cmd/upkeep`.
If a change needs engine knowledge outside `internal/adapter/`, grow the adapter
interface instead.

The same rule holds on the Go side, where it is a test rather than a grep
(`internal/invariant/adapter_boundary_test.go`) — an AST scan, so a comment
mentioning ddev is fine and a string literal is not. Growing
`adapter.Engine` is the answer there too.

## Conventions

- Commands resolve the cockpit as `--cockpit` > `UPKEEP_COCKPIT` > cwd, and
  the projects root as `--projects-root` > `UPKEEP_PROJECTS_ROOT` >
  `<cockpit>/projects` (when that directory exists) > `~/.upkeep/projects`.
- The resolved projects root — and `base-artifacts:build --scratch-dir` — must
  canonicalise to a path under `$HOME`, or the command is refused with exit 2.
  This is functional, not just hardening: the tree is bind-mounted into the
  Docker VM and macOS providers only share the home directory, so an
  environment outside it can never start. Containment is checked on the
  canonical path (`filesystem.Canonicalize`), so `..` and symlink escapes are
  caught. **There is no escape hatch**; adding one is a decision, not a patch.
- `--version` is *always* the target-core selector, never an app-version flag
  — on check/review/dev/exec/env:path/needs-work, as a filter over assembled
  rows on dashboard, and on `base-artifacts:build` (which used to spell it
  `--core`). The application-level `-V/--version` is deliberately removed in
  `cmd/upkeep`.
- The one MR-IID rule lives in `cli.MrIID`: a positive integer,
  or exit 2. `!0` is not a merge request, so it is refused rather than turned
  into a confusing 404.
- Exit codes are a CLI-wide contract, mapped once in `cli.Run` and expressed
  by the `workflow` constants: **0** the command did what was asked, **1** the
  work it supervised failed, **2** upkeep could not do the job. A command
  returns `workflow.OK` or `workflow.Failed`; returning an `error` is the
  documented way to report a 2, so no command has to remember the number.
- Every write goes through `filesystem.Commit` — atomic, checked, explicit
  mode. Do not add a raw `os.WriteFile`; an ignored error is how `init` used
  to report "Cockpit created" over a registry that never landed. Caches holding
  token-scoped remote data or raw check output are `0600` in `0700`
  directories (`filesystem.ModePrivate` / `ModePrivateDir`).
- `UPKEEP_GITLAB_TOKEN` is never forwarded to a child process
  (`security.ScrubbedEnvironment`), and process output is passed
  through `security.Redactor` before it is logged, rendered or cached.
- **Every suppression requires a written justification** — a comment on the
  line above saying what is being suppressed and why it cannot be fixed. That
  applies to `//nolint`, a `t.Skip` that is not conditional on the environment,
  and lowering a floor in `coverage.sh`. The repository currently has **zero**
  `//nolint`; that is the state to preserve. Verify with:

  ```bash
  grep -rn "nolint" cmd/ internal/
  ```

  Prefer restructuring the code over suppressing the tool. An untested error
  path is exactly the kind that reports success over a failure.
- Merges are one-human-approval-per-MR by DA policy; never add a batch or
  unattended merge path (see README "Policy stance"). Held by
  `internal/cli/command/merge_test.go`, which asserts a second merge request
  needs its own answer.
