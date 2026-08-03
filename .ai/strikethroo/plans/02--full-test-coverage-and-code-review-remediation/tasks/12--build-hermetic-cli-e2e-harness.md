---
id: 12
group: "test-coverage"
dependencies: [6, 7, 9, 10, 11]
status: "pending"
created: 2026-08-02
skills:
  - phpunit
  - symfony-console
complexity_score: 7
complexity_notes: "Builds reusable test infrastructure and its first consumers together. Kept as one task because a harness with no tests has no runnable acceptance criterion, and its shape is only validated by the tests that use it."
---
# Build the hermetic CLI end-to-end harness and cover the exit-code contract

## Objective

Create a reusable harness that drives commands through the console entry point
against a temporary cockpit with the engine and GitLab faked, and use it to
cover the exit-code contract and the cockpit and projects-root resolution
precedence.

## Skills Required

`phpunit` for the harness and test structure; `symfony-console` for
`CommandTester` and application wiring.

## Acceptance Criteria

- [ ] A reusable harness constructs the console application the way `bin/upkeep` does, against a per-test temporary cockpit that is torn down afterwards.
- [ ] The harness supplies a fake `Adapter\EngineAdapterInterface` and mocked GitLab HTTP, reusing the existing fakes rather than duplicating them.
- [ ] Tests assert exit code 0 for a passing run, 1 for a failed check, and 2 for an infrastructure failure, driven through `CommandTester`.
- [ ] Tests cover all three branches of cockpit resolution: `--cockpit`, `UPKEEP_COCKPIT`, and cwd fallback.
- [ ] Tests cover all three branches of projects-root resolution: `--projects-root`, `UPKEEP_PROJECTS_ROOT`, and the `~/.upkeep/projects` default.
- [ ] A test asserts that `--version` on check/review/dashboard selects the target Drupal core version and is not treated as an application-version flag.
- [ ] These tests live in the existing `unit` testsuite; no second testsuite is configured.
- [ ] `vendor/bin/phpunit` passes with docker stopped, networking disabled, and `UPKEEP_GITLAB_TOKEN` unset, and still completes in seconds.
- [ ] No test touches the real `~/.upkeep/`, the real `~/.config/upkeep/`, or any real cockpit.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- Symfony Console's `CommandTester` (or `ApplicationTester`) drives commands and
  exposes output and exit status.
- The existing suite already fakes `Adapter\EngineAdapterInterface` and mocks
  GitLab HTTP — reuse those seams.
- The exit-code contract lives in `src/Workflow/`: 0 pass, 1 check failed, 2
  infrastructure.
- Environment-variable-dependent tests must save and restore the environment so
  they do not leak state between tests.

## Input Dependencies

- Tasks 6, 7, 9, 10, 11: the settled CLI surface, error reporting, and types.
  Writing these tests before remediation would mean writing them twice.
- Task 9 specifically: the list of any command name, option, or exit-code
  changes.

## Output Artifacts

- The reusable CLI e2e harness, which task 15 uses for command coverage.
- Tests covering the exit-code contract, both resolution precedences, and the
  `--version` semantics.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Read `bin/upkeep` first and mirror how it constructs and configures the
   `Application`. The harness should exercise the real wiring — that is the
   point of these tests — with only the engine and GitLab swapped for fakes.

2. Create the temporary cockpit per test under `sys_get_temp_dir()`, populate
   the minimum `registry.yml` and directory structure needed, and remove it in
   `tearDown()`. Never point at the real `~/.upkeep/` or the maintainer's
   cockpit.

3. For the environment-variable branches (`UPKEEP_COCKPIT`,
   `UPKEEP_PROJECTS_ROOT`, `UPKEEP_GITLAB_TOKEN`), capture the prior value in
   `setUp()` and restore it in `tearDown()`. Leaked environment state between
   tests produces failures that only reproduce in a particular test order and
   are miserable to debug.

4. The `--version` test matters because the flag is a documented source of
   confusion: on check/review/dashboard it selects the *target Drupal core
   version*, never the application version. Assert the target-core behaviour
   explicitly.

5. Put these tests in the existing `unit` testsuite. There is deliberately no
   second suite — nothing here is slow or environment-dependent, so a separate
   suite would add configuration for no benefit.

6. Confirm the hermetic property directly: stop docker, disable networking,
   unset `UPKEEP_GITLAB_TOKEN`, and run the suite. It must pass in seconds.

### Test philosophy: "write a few tests, mostly integration"

**Definition.** Meaningful tests verify custom business logic, critical paths,
and edge cases specific to this application. Test *your* code, not the
framework or library.

**When TO write tests:**
- Custom business logic and algorithms.
- Critical user workflows and data transformations.
- Edge cases and error conditions for core functionality.
- Integration points between components.
- Complex validation logic or calculations.

**When NOT to write tests:**
- Third-party library functionality.
- Framework features.
- Simple CRUD operations without custom logic.
- Trivial getters/setters or static configuration.
- Obvious functionality that would break immediately if incorrect.

**Test task creation rules:**
- Combine related test scenarios into a single test rather than splitting per
  operation.
- Favor integration and critical-path coverage over per-method unit tests.
- Avoid one test per CRUD operation.
- Question whether simple functions need a dedicated test.

Applied here: do not test that Symfony Console parses options — that is
framework behaviour. Test that *this application's* resolution precedence,
exit-code contract, and target-core semantics are correct.

</details>
