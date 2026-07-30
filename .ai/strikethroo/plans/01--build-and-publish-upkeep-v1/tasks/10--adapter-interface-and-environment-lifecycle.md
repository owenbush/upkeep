---
id: 10
group: "orchestrator-core"
dependencies: [8]
status: "completed"
created: 2026-07-29
skills:
  - php
  - ddev
complexity_score: 5
complexity_notes: "Decomposed from a single cross-cutting adapter task; this half owns the interface definition and project lifecycle, task 11 owns the per-MR operations."
---
# Define the adapter interface and implement environment lifecycle

## Objective
Define the plan's single most important structural boundary — the engine adapter interface (`ensure_env`, `apply_mr`, `load_fixture`, `run_checks`, `serve`, `teardown`) — and implement its lifecycle half for the `ddev-drupal-contrib` engine: `ensure_env(module, core_version)` provisions or reuses a per-(module × core-version) ddev project seeded from base artifacts, and `teardown` disposes it using the verified reclamation semantics.

## Skills Required
`php` for the interface and orchestration; `ddev` for driving project lifecycle via shell-outs.

## Acceptance Criteria
- [x] A PHP interface exists declaring all six operations with typed parameters/results; the orchestrator layer references only this interface (enforced by namespace layout: nothing outside the adapter implementation namespace mentions `ddev`), verified by `grep -r "ddev" src/ --exclude-dir=Adapter` returning nothing.
- [x] `ensure_env` for a registered module + core version, run for real: creates the ddev project (engine add-on installed, pinned version), seeds the codebase from the core version's base tree, requires the module via a Composer path-repository symlink (never `--prefer-source`), restores the clean-install snapshot — and a second call with the same arguments reuses the existing project (2.2s vs 51.0s, states "Reusing").
- [x] The provisioned project serves at its own `*.ddev.site` URL (`curl -sI` returned HTTP/2 200 for both cores) with the core version read from the project — `ddev exec drush status --field=drupal-version` reported 11.4.4 / 10.6.14 for the requested majors.
- [x] `teardown(module, core_version)` removes the project and its volumes per task 3's reclamation table; verified live for core 10: `ddev list` no longer shows it and all its volumes (mariadb, snapshots, mutagen) are gone from `docker volume ls`.
- [x] The engine add-on version is pinned in one configuration point (`EngineAddOn::VERSION` = 1.1.5 — release tags carry no `v` prefix), with a code comment naming the upgrade procedure (deliberate adapter-maintenance event).

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Project naming convention encoding (module × core-version), e.g. `upkeep-<module>-d<version>`, kept in one place.
- `Symfony\Component\Process` shell-outs to `ddev` and `composer`; working directories under a cockpit-configured projects root.
- `ddev-drupal-contrib` installation into new projects (`ddev add-on get ddev/ddev-drupal-contrib` at the pinned version); the DB restore uses the same dump-import path the fixture model uses.

## Input Dependencies
- Task 8's base artifacts and their meta (seed source, engine identity for snapshot skew detection).
- Task 3's reclamation table (teardown correctness) — transitively satisfied via task 8's dependency.

## Output Artifacts
- The adapter interface (contract for task 11's remaining operations) and a working lifecycle implementation, consumed by tasks 11, 13, and 16.

## Implementation Notes
This boundary is the design's churn absorber: all knowledge of engine command names, project layout, and ddev behavior lives here. Resist any leak — if a later task needs an engine detail, it gets a new adapter method, not a shell-out from command code.

<details>
<summary>Detailed steps</summary>

1. `src/Adapter/EngineAdapterInterface.php` (or equivalent) with the six operations; result objects for check results and serve URLs defined now even though tasks 11 implements their producers.
2. `DdevContribAdapter::ensureEnv`: compute project name/path; if existing and healthy (`ddev describe` succeeds, meta matches), reuse. Else: mkdir, copy base tree (or full resolve per task 3's verdict), `ddev config` with the engine's expected settings for that core, install pinned engine add-on, `ddev start`, symlink/require the module working copy via the engine's `symlink-project`/`poser` mechanism, import clean-install dump.
3. Store per-project meta (module, core version, seed source, engine add-on version) in a dotfile inside the project for reuse/health checks and for task 16's disk attribution.
4. `teardown`: `ddev delete -Oy` (or the exact verified command set) then remove the tree.
5. Verify live against one real module on one core version; then the same module on a second core version to prove the matrix (two projects coexist, each reporting its own core).
</details>
