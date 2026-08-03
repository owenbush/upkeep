# CLAUDE.md — upkeep

Maintenance orchestrator CLI for contributed Drupal modules (PHP >= 8.2,
Symfony Console). User-facing docs: `README.md`; architecture and rationale:
`docs/contrib-maintainer-design.md`.

## Layout

- `bin/upkeep` — the console entry point; registers every command.
- `src/Adapter/` — the engine adapter: everything ddev / ddev-drupal-contrib
  specific (provisioning, MR checkout, check execution, teardown, the
  ddev-upkeep fixture add-on). Engine pinned: ddev-drupal-contrib 1.1.5
  (`Adapter\EngineAddOn`).
- `src/Command/` — one class per CLI command; thin, delegating to the
  namespaces below.
- `src/Cockpit/` — cockpit directory + `registry.yml` module registry.
- `src/Gitlab/` — git.drupalcode.org API client, token resolution
  (`UPKEEP_GITLAB_TOKEN` env, else `~/.config/upkeep/drupal-pat`), MR/pipeline
  models. Never log or print a token.
- `src/Gate/` — fast-lane gate classification (READY-AUTO / REVIEW / BLOCKED).
- `src/Dashboard/`, `src/Results/` — dashboard row assembly, cached check
  results (`<cockpit>/results/`).
- `src/BaseArtifact/` — per-core base tree + clean-install dump build/scan.
- `src/Maintenance/` — prune/status inventory and selection.
- `src/Workflow/` — shared MR-flow context and the exit-code contract
  (0 pass / 1 check failed / 2 infrastructure).
- `src/Notes/`, `src/Config/` — release-notes drafting; config resolution.
- `tests/` — PHPUnit, mirroring `src/`.

## Running tests

```bash
vendor/bin/phpunit
```

Fast (seconds), no network, no docker: engine interactions are tested against
fakes of `Adapter\EngineAdapterInterface`, GitLab via mocked HTTP.

Coverage reporting requires a coverage driver — PCOV (preferred; faster,
line-coverage only) or Xdebug (accepted; also supports branch coverage). With
PCOV installed and loaded (`php -m | grep pcov`):

```bash
vendor/bin/phpunit --coverage-text
```

prints the per-namespace coverage table plus an overall line-coverage summary,
and also writes an HTML report to `build/coverage-html/`. If PCOV isn't
auto-enabled in your `php.ini`, pass `-d pcov.enabled=1` on the command line.
No coverage threshold is enforced by `phpunit.xml.dist` yet, so a run below
100% still exits 0.

## Hard rule: the adapter boundary

Engine specifics (ddev, ddev-drupal-contrib, docker, container/volume names)
live **only** in `src/Adapter/`. The rest of the orchestrator talks to
`EngineAdapterInterface` and must stay engine-agnostic. Guard:

```bash
grep -r "ddev" src/ --exclude-dir=Adapter
```

must return nothing. If a change needs engine knowledge outside `src/Adapter/`,
grow the adapter interface instead.

## Conventions

- Commands resolve the cockpit as `--cockpit` > `UPKEEP_COCKPIT` > cwd, and
  the projects root as `--projects-root` > `UPKEEP_PROJECTS_ROOT` >
  `~/.upkeep/projects` (must stay under `$HOME` — Docker providers on macOS
  only mount the home directory).
- `--version` on check/review/dashboard is the *target core* selector, never
  an app-version flag.
- Merges are one-human-approval-per-MR by DA policy; never add a batch or
  unattended merge path (see README "Policy stance").
