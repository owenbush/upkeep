---
id: 3
group: "quality-tooling"
dependencies: [1, 2]
status: "pending"
created: 2026-08-02
skills:
  - github-actions
  - php
complexity_score: 3
---
# Wire quality gates into the CI workflow

## Objective

Extend the existing CI workflow so lint, static analysis, and coverage all run
on every push and pull request across the PHP 8.2/8.3/8.4 matrix, reported but
not yet blocking.

## Skills Required

`github-actions` for workflow authoring and `shivammathur/setup-php`
configuration; `php` for invoking the tooling consistently with local usage.

## Acceptance Criteria

- [ ] `.github/workflows/ci.yml` sets a coverage driver via `shivammathur/setup-php` instead of `coverage: none`.
- [ ] The workflow runs lint (PHP_CodeSniffer) and static analysis (PHPStan) as named steps, using the Composer scripts from task 1 rather than duplicating the command lines.
- [ ] The existing steps — `composer validate --strict`, `composer install`, PHPUnit, and the `./bin/upkeep list` smoke test — all remain present and passing.
- [ ] The work stays in the single existing `tests` job so one status check continues to represent the build; no new workflow file is created.
- [ ] Lint and analysis steps do not fail the build yet (use `continue-on-error` or equivalent), because remediation has not happened.
- [ ] A pushed branch produces a green CI run on all three matrix legs, and the run log visibly shows the lint, analysis, and coverage steps executing.
- [ ] Step names make the four gates legible at a glance in the Actions UI.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- The existing matrix is `php: ["8.2", "8.3", "8.4"]` with `fail-fast: false`; preserve it.
- `shivammathur/setup-php@v2` supports `coverage: pcov` and `coverage: xdebug`.
- No docker, network access, or GitLab token may be required by any step.

## Input Dependencies

- Task 1: the `lint` and `analyse` Composer scripts and their config files.
- Task 2: the PHPUnit coverage configuration.

## Output Artifacts

- Updated `.github/workflows/ci.yml` running all four gates.
- A green CI run demonstrating the gates execute.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Edit `.github/workflows/ci.yml`. In the `Set up PHP` step, change
   `coverage: none` to the driver chosen in task 2 (`pcov` expected).

2. Add two steps after `Install dependencies` and before `Run PHPUnit`:
   - `Lint (PSR-12)` running `composer lint`
   - `Static analysis (PHPStan max)` running `composer analyse`

   Mark both with `continue-on-error: true` for now. Task 18 removes that flag
   once remediation is complete. Add a brief inline comment saying exactly
   that, so the temporary state is not mistaken for the intended end state.

3. Change the PHPUnit step to produce coverage output so the figure is visible
   in the run log.

4. Keep everything inside the existing `tests` job. Do not split into separate
   jobs — the plan's integration strategy is explicitly that one status check
   represents the build.

5. Wall-clock note: enabling coverage slows every matrix leg. If this becomes a
   problem, the fallback recorded in the plan is to enforce the threshold on a
   single matrix leg while running the suite uncovered on the others, keeping
   the gate authoritative without triplicating it. Do not implement that
   fallback pre-emptively — only if the run time is actually a problem.

6. Verify by pushing a branch and confirming all three legs go green with the
   new steps visible in the logs.

</details>
