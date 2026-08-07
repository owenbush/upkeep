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
  `Adapter\ManagedBranch` names both prefixes so base-branch resolution
  rejects either as a base. A managed branch used as a base would silently
  stack one contribution on another. `applyPatch` commits what it applies
  (checks must run against a clean tree) and resets the branch from the base
  every time (a re-roll is tested alone, not stacked on the last one); a patch
  that will not apply after a three-way retry is an `AdapterException` phrased
  as a review finding, because "needs a re-roll" is what it tells a maintainer.
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
- `src/Workflow/` — shared MR-flow context and the exit-code contract
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
lines (4889/4889), methods (562/562) and classes (125/125), 1036 tests.

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
