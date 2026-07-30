---
id: 6
group: "ddev-addon"
dependencies: [5]
status: "completed"
created: 2026-07-29
skills:
  - bats
  - ddev-addon
complexity_score: 3
---
# Add-on integration tests for the fixture round-trip

## Objective
Extend the template's bats test harness with integration tests covering the add-on's real behavior — the fixture round-trip, resolution order, sanitization default, and prune safety — and verify the add-on end-to-end against at least one real maintained module, per the plan.

## Skills Required
`bats` for the test harness the template ships; `ddev-addon` for driving ddev projects inside tests.

## Acceptance Criteria
- [ ] `bats tests` exits 0 locally and covers at least: create → mutate → load restores state; second load uses the snapshot path; module fixture shadows library fixture of the same name; prune removes only materialized snapshots; missing-fixture-name exits non-zero.
- [ ] The sanitize-by-default behavior for module-repo destinations is asserted (output contains the sanitize step, or `--no-sanitize` skips it).
- [ ] The GitHub Actions test workflow from the template runs these tests green on push (check `gh run list --repo owenbush/ddev-upkeep` shows a passing run).
- [ ] A manual end-to-end pass against one real maintained module (real `ddev-drupal-contrib` project, real module checkout) is executed and its transcript recorded in the PR/commit message or a `docs/` note in the add-on repo.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- The template's existing bats setup and CI workflow — extend, don't replace.
- A real module checkout with `ddev-drupal-contrib` for the manual pass (user names the module; task 2's chosen module is a natural candidate).

## Input Dependencies
- Task 5's implemented commands.

## Output Artifacts
- Passing test suite and green CI in `owenbush/ddev-upkeep`, gating task 19 (publication requires green CI on the published tag).

## Implementation Notes
2026-07-30: CI-green verification is DEFERRED to task 19 with the user's approval — the GitHub account's Actions credits are exhausted until they reset in a few days, so the "CI workflow runs green on push" criterion cannot be verified now. Local bats green (`bats tests --filter-tags '!release'` → 4/4 ok) is the accepted evidence; the pushed workflow run's queued/blocked state is recorded in the task 6 report.

Test philosophy (apply verbatim): **write a few tests, mostly integration.** Meaningful tests verify custom business logic, critical paths, and edge cases specific to this application — test *your* code, not the framework or library. Write tests for: custom business logic, critical workflows and data transformations, edge cases and error conditions for core functionality, integration points. Do NOT write tests for: third-party functionality (ddev itself, mysqldump), framework features, trivial operations, or obvious functionality that would break immediately if incorrect. Combine related scenarios into single tests; favor integration and critical-path coverage over per-command micro-tests.

<details>
<summary>Detailed steps</summary>

1. Follow the template's tests README for how its bats harness provisions a scratch ddev project per test run.
2. One bats file covering the round-trip story in sequence (create, mutate via `drush sql:query`, load, assert restored value, load again, assert snapshot path taken from output). One file for resolution order + prune safety + error exits. That's the whole suite — resist adding per-flag tests.
3. Push and confirm the CI workflow passes; fix environment differences (CI lacks drush? gate sanitize assertions accordingly) rather than weakening assertions.
4. Manual pass: in the real module's project, run the create/load/list/prune cycle once; capture the terminal transcript.
</details>
