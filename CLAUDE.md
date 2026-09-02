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
- `src/Command/` — one class per CLI command; thin, delegating to the
  namespaces below. All extend `Command\UpkeepCommand`, which owns the shared
  option surface, the resolution seam, and the exit-code mapping.
- `src/Cockpit/` — cockpit directory + `registry.yml` module registry.
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
  `patch↑` existed only in a source comment. The overview's columns use the same
  words as the rows they summarise — `PATCH ISSUES` (not `PATCHES`, which would
  collide with a row's file count) and `CI FAILED` (not `BLOCKED`, which is what
  that verdict is actually set by). There is no `REVIEW` column: it counted
  everything neither ready nor CI-failed, i.e. every row.
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
  two counts of the same thing that can disagree are worse than one. Subjects
  (MRs, patch issues) are counted once per module; verdicts and check evidence
  are counted per (subject x core), because they genuinely differ per core. A dashboard row is a merge request, a patch
  contribution, or a module failure; only the first carries a `GateVerdict`, so
  `isReadyAuto()` is false for a patch row *by construction* rather than by a
  check someone has to remember. `Results\ResultKey` keeps MR and patch results
  in separate path namespaces (`<iid>` vs `patch-<nid>`) — both subjects are
  identified by a number and nothing keeps the ranges apart, and a patch verdict
  read as an MR verdict would put unmergeable evidence in front of the
  fast-lane gate. Patch results are keyed by `Patches\PatchRevision` (a hash of
  the patch's source URL, not its bytes — the dashboard must judge staleness
  from the attachment list without downloading anything).
- `src/BaseArtifact/` — per-core base tree + clean-install dump build/scan.
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
vendor/bin/phpunit # the suite, with the 100% line-coverage floor enforced
./bin/upkeep list  # the binary still boots
```

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
lines (6334/6334), methods (734/734) and classes (148/148), 1296 tests.

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
