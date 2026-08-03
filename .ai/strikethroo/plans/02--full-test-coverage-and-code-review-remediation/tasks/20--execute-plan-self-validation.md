---
id: 20
group: "enforcement-and-docs"
dependencies: [18, 19]
status: "pending"
created: 2026-08-02
skills:
  - phpunit
  - cli-testing
complexity_score: 4
---
# Execute the plan's self-validation procedure

## Objective

Run all thirteen self-validation steps from the plan against the finished
implementation, record the output of each, and report any that fail.

## Skills Required

`phpunit` for the coverage and suite verification steps; `cli-testing` for the
binary, exit-code, and token-leak verification steps.

## Acceptance Criteria

- [ ] `composer install --no-interaction` succeeds in a clean checkout with no unresolved dev dependencies.
- [ ] `vendor/bin/phpunit --coverage-text` reports 100% line coverage for `src/`; the per-namespace table is captured as evidence.
- [ ] Deleting one assertion-bearing test causes `vendor/bin/phpunit` to **fail** on the coverage threshold; the file is then restored.
- [ ] `vendor/bin/phpstan analyse --no-progress` reports zero errors, and no `phpstan-baseline.neon` exists.
- [ ] `vendor/bin/phpcs` reports zero errors and zero warnings.
- [ ] `grep -r "ddev" src/ --exclude-dir=Adapter` produces no output.
- [ ] `grep -rn "codeCoverageIgnore\|phpstan-ignore\|phpcs:ignore" src/` shows every hit with an adjacent justification; the total count is reported.
- [ ] `./bin/upkeep list` registers every command and exits 0.
- [ ] The exit-code contract is confirmed from the shell: a passing scenario gives `$?` 0, a failed check gives 1, an infrastructure failure gives 2.
- [ ] With `UPKEEP_GITLAB_TOKEN` set to a sentinel and a subprocess failure forced, the sentinel appears in no output and no written result file.
- [ ] With `UPKEEP_GITLAB_TOKEN` unset, the tool reports the missing credential using the `describeSources()` wording and emits no token material.
- [ ] The full suite passes with networking disabled and docker stopped.
- [ ] CI is green on all three matrix legs, with lint, analysis, and coverage steps visible in the log.
- [ ] Every step's output is recorded; any failure is reported rather than worked around.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- These are the thirteen steps from the plan's "Self Validation" section,
  executed in order.
- Steps 3, 9, and 10 require deliberately breaking or manipulating state;
  each must be fully reverted.
- The sentinel-token step is the CLI-level counterpart to the unit-level
  redaction test from task 6.

## Input Dependencies

- Task 18: the enforced gates.
- Task 19: the updated documentation.
- All prior tasks, transitively — this validates the completed plan.

## Output Artifacts

- A recorded validation report covering all thirteen steps with their output.
- Confirmation that the plan's success criteria are met, or a clear statement
  of which are not.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Work the plan's Self Validation section in order and capture the actual
   output of each step. The point is evidence, not assertion — "verified" with
   no captured output is not a validation.

2. Step 3 (deleting a test to prove the coverage gate fails) overlaps with
   task 18's negative test. Run it again here anyway: task 18 proved the gate
   worked at that moment, and this confirms it still holds against the finished
   tree.

3. For the exit-code checks, use the temporary-cockpit approach from the task
   12 harness so no real environment is touched. Check `$?` directly in the
   shell rather than inferring the code from output text.

4. For the sentinel-token step, set `UPKEEP_GITLAB_TOKEN` to something
   unmistakable, force a subprocess failure, then grep the combined output
   **and** any files written under the cockpit's `results/` directory. The
   cached-results path is the easier one to forget and is a real persistence
   sink.

5. Restore everything: the deleted test, the environment variables, and any
   fixture state. Finish with `git status --porcelain` clean.

6. **Report failures as failures.** If a step does not pass, say so with the
   output, rather than adjusting the step until it does. A validation
   procedure that cannot fail has no value, and the honest result is more
   useful than a green report.

</details>
