---
id: 7
group: "orchestrator-core"
dependencies: []
status: "completed"
created: 2026-07-29
skills:
  - symfony-console
  - composer
complexity_score: 3
---
# Scaffold the upkeep CLI package with cockpit config and module registry

## Objective
Turn this repository into the `owenbush/upkeep` Composer package: a Symfony Console application exposing the `upkeep` binary, plus the cockpit concept — a control-project directory containing the module registry (which modules are maintained, where their repos live) and locations for base artifacts and the shared fixture library — loaded by every command.

## Skills Required
`symfony-console` for the CLI skeleton; `composer` for package metadata and the vendor binary.

## Acceptance Criteria
- [ ] `composer install` succeeds in this repo; `composer validate` passes; the package name is `owenbush/upkeep` with `bin/upkeep` declared in `bin`.
- [ ] `./bin/upkeep list` runs and shows the application name/version and registered command namespaces (commands themselves arrive in later tasks; a placeholder `upkeep init` that scaffolds a cockpit is included here).
- [ ] `upkeep init <dir>` creates a cockpit skeleton: a registry config file (with a commented example module entry: machine name, git.drupalcode.org project path, tracked core versions), plus empty `base-artifacts/` and `fixtures/` directories.
- [ ] Registry loading is a small typed service: given a cockpit path (flag `--cockpit` or env var, with cwd-detection default), commands can enumerate registered modules; a wrong path or malformed registry produces a clear non-zero error (verify by running against a bogus path).
- [ ] PHPUnit is wired (`vendor/bin/phpunit` runs, even with a single registry-loading test) and a GitHub Actions workflow runs it on push.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- PHP (target the currently-supported PHP versions consistent with supported Drupal cores), Symfony Console, symfony/yaml or JSON for the registry format (pick YAML for hand-editability).
- PSR-4 autoloading under a single `Upkeep\` namespace; keep structure conventional (`src/`, `bin/`, `tests/`).
- The git repo must be initialized here (`git init`) — this directory predates any VCS.

## Input Dependencies
None — first-wave scaffold. (Task 1's Packagist verification gates only publication, not local scaffolding.)

## Output Artifacts
- The runnable `upkeep` application skeleton and cockpit/registry services that every subsequent orchestrator task (8–17) builds on.

## Implementation Notes
Keep the cockpit format minimal: exactly what the plan needs (module list with project paths and core versions, artifact/fixture paths) — no speculative config options. The registry schema will be documented in task 18; keep the example entry in the scaffold authoritative.

<details>
<summary>Detailed steps</summary>

1. `git init`; author `composer.json` (name, description, license, `bin/upkeep`, PHP requirement, symfony/console + symfony/yaml + a PSR-18-capable HTTP client dependency deferred to task 9 — don't add it yet).
2. `bin/upkeep`: standard Symfony Console single-app bootstrap; application name "Upkeep", version "dev".
3. `src/Cockpit/` — a `Cockpit` value object (paths) and `ModuleRegistry` loader (parse YAML, validate required fields per module, throw with actionable message on malformed input). `upkeep init` writes the skeleton files with the commented example.
4. One PHPUnit test: registry loads a valid fixture file and rejects a malformed one. Add `.github/workflows/ci.yml` running `composer validate` + phpunit on PHP matrix.
5. Commit as the initial commit series of this repo (do not push anywhere yet; GitHub repo creation happens in task 19).
</details>
