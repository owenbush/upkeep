---
id: 11
group: "orchestrator-core"
dependencies: [10, 5]
status: "completed"
created: 2026-07-29
skills:
  - php
  - ddev
complexity_score: 5
complexity_notes: "Second half of the decomposed adapter work: the four per-MR operations on top of task 10's lifecycle."
---
# Implement adapter MR, fixture, check, and serve operations

## Objective
Complete the ddev engine adapter: `apply_mr(ref)` checks out an MR branch into the environment's module working copy (native-base only), `load_fixture(name)` invokes the add-on's fixture mechanism, `run_checks()` executes the engine's CI-aligned checks and returns structured results, and `serve()` returns the project's browsable URL with the module enabled.

## Skills Required
`php` for the adapter methods and result modeling; `ddev` for the engine's check commands and in-container operations.

## Acceptance Criteria
- [ ] `apply_mr` on a real open MR: fetches the MR ref from git.drupalcode.org into the module working copy and checks out its branch against its native base; `git -C <module> rev-parse --abbrev-ref HEAD` shows the MR branch. The checkout survives a subsequent composer operation (path-repo/symlink protection — verify the branch is still checked out after `ddev composer install`).
- [ ] `load_fixture` shells to the add-on's `ddev fixture-load` (no duplicated logic) and surfaces its success/failure; a fixture load observably changes DB content (spot query before/after).
- [ ] `run_checks` runs the engine's `ddev phpunit`, `ddev phpstan`, `ddev phpcs`, plus module install/enable (`drush pm:install <module>`) and a basic functional smoke (front page returns HTTP 200 with the module enabled), returning a structured per-check pass/fail with captured output — demonstrated live on one real MR with at least one check output shown.
- [ ] `serve` enables the module if needed and returns the `*.ddev.site` URL; fetching it returns HTTP 200.
- [ ] A deprecation/upgrade-status report is included in `run_checks` when the engine provides it for the target core (else marked "unavailable" in the result, not silently omitted).

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- MR refs on drupalcode GitLab: `refs/merge-requests/<iid>/head` fetch pattern; native-base checkout only (backport testing is out of scope — reject with a clear error if the MR's target branch mismatches the checked-out base).
- Engine check commands as provided by the pinned `ddev-drupal-contrib` version; all invocation details stay inside the adapter.
- Structured `CheckResult` collection (name, status, duration, output excerpt) — the dashboard renders these.

## Input Dependencies
- Task 10's interface and lifecycle implementation.
- Task 5's fixture-load command (invoked, not reimplemented).

## Output Artifacts
- A complete engine adapter, consumed by task 13 (`check`/`review` commands), task 12 (local-check statuses on the dashboard), and task 14 (fast-lane local gate inputs).

## Implementation Notes
Never use `--prefer-source` to obtain the module working copy — Composer would consider itself the owner and may delete branches or the checkout. The engine's `symlink-project`/`poser` mechanism (or a path repository) established in task 10 is what makes the MR checkout safe; `apply_mr` operates on that working copy with plain git.

<details>
<summary>Detailed steps</summary>

1. `apply_mr`: in the module working copy — `git fetch origin refs/merge-requests/<iid>/head:mr-<iid>`, verify the MR's target branch equals the working copy's base branch (error otherwise: out-of-scope backport), `git checkout mr-<iid>`.
2. `load_fixture`: `ddev fixture-load <name>` via Process; map exit code to typed result.
3. `run_checks`: run each check sequentially, capturing exit codes and output; module install check is `ddev drush pm:install <module> -y`; smoke is `curl` against the project URL post-install. Timebox each check with a generous per-check timeout; a timeout is a failure with reason.
4. `serve`: ensure started + module installed, return URL.
5. Live verification on one real MR (the acceptance list's demonstrations) — capture terminal output as the task's evidence.
</details>
