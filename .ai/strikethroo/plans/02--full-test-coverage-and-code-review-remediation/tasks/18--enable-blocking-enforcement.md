---
id: 18
group: "enforcement-and-docs"
dependencies: [3, 17]
status: "completed"
created: 2026-08-02
skills:
  - phpunit
  - github-actions
complexity_score: 4
---
# Enable blocking enforcement of all quality gates

## Objective

Turn the four quality gates from reporting into blocking: enforce 100% line
coverage in `phpunit.xml.dist`, and remove the non-blocking flags from the CI
lint and analysis steps.

## Skills Required

`phpunit` for coverage threshold configuration; `github-actions` for finalising
the workflow.

## Acceptance Criteria

- [ ] `phpunit.xml.dist` enforces a 100% minimum line coverage threshold, so a run below it fails.
- [ ] The `continue-on-error` flags added in task 3 are removed from the CI lint and analysis steps, along with the temporary comment explaining them.
- [ ] A deliberate negative test proves the coverage gate is live: remove or comment out one assertion-bearing test, confirm `vendor/bin/phpunit` **fails** on the coverage threshold, then restore the file and confirm it passes again.
- [ ] A deliberate negative test proves the lint gate is live: introduce a PSR-12 violation, confirm `composer lint` exits non-zero, then revert.
- [ ] A deliberate negative test proves the analysis gate is live: introduce a type error, confirm `composer analyse` exits non-zero, then revert.
- [ ] With everything restored, CI is green on all three matrix legs with no `continue-on-error` remaining.
- [ ] `git status --porcelain` is clean after the negative tests — every deliberate breakage was reverted.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- PHPUnit 11.5 supports coverage threshold enforcement via the `<coverage>`
  configuration; the threshold belongs in `phpunit.xml.dist` rather than a CI
  flag, so local and CI runs enforce identically.
- The existing matrix is `php: ["8.2", "8.3", "8.4"]` with `fail-fast: false`.

## Input Dependencies

- Task 3: the CI workflow with all gates wired and the `continue-on-error`
  flags to remove.
- Task 17: a clean, audited state across coverage, analysis, and style — the
  gates can only be made blocking once everything actually passes.

## Output Artifacts

- Enforced quality gates in `phpunit.xml.dist` and
  `.github/workflows/ci.yml`.
- Evidence from the three negative tests that each gate genuinely blocks.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Confirm the current state is actually clean before enabling anything:
   `vendor/bin/phpunit --coverage-text` at 100%, `composer analyse` at zero
   errors, `composer lint` at zero violations. If any of these is not clean,
   stop — enforcement is not this task's job to achieve, only to lock in.

2. Add the 100% line-coverage requirement to `phpunit.xml.dist`. Put it in the
   config file, not in a CI command-line flag: the plan's integration strategy
   is explicitly that the same gates apply locally and in CI, and a flag-only
   threshold silently does nothing for developers running the suite by hand.

3. Remove `continue-on-error` from the lint and analysis steps in
   `.github/workflows/ci.yml`, along with the comment task 3 added explaining
   that they were temporary.

4. The three negative tests are the point of this task. A gate that has never
   been observed to fail has not been shown to work — a misconfigured threshold
   that silently passes everything looks identical to a working one until it
   matters. Run each, observe the failure, then revert.

5. Be careful to fully revert each deliberate breakage. Finish by confirming
   `git status --porcelain` is empty and the suite is green.

6. Coverage slows every matrix leg. If wall-clock is now a genuine problem, the
   plan's recorded fallback is to enforce the threshold on a single matrix leg
   while running the suite uncovered on the others — keeping the gate
   authoritative without triplicating it. Only apply this if the run time is
   actually a problem; do not pre-emptively optimise.

</details>
