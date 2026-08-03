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
  specific (provisioning, MR checkout, check execution, teardown, the
  ddev-upkeep fixture add-on). Engine pinned: ddev-drupal-contrib 1.1.5
  (`Adapter\EngineAddOn`). Commands receive `Adapter\EngineAdapterFactory` and
  never construct an engine themselves.
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
  file attachments, MR ↔ issue references).
- `src/Gate/` — fast-lane gate classification (READY-AUTO / REVIEW / BLOCKED).
- `src/Dashboard/`, `src/Results/` — dashboard row assembly, cached check
  results (`<cockpit>/results/`).
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
lines (4035/4035), methods (470/470) and classes (110/110), 834 tests.

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
