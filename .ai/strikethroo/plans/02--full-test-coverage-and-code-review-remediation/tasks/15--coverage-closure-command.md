---
id: 15
group: "test-coverage"
dependencies: [12]
status: "pending"
created: 2026-08-02
skills:
  - phpunit
  - symfony-console
complexity_score: 5
---
# Close coverage to 100% across the command namespace

## Objective

Bring `src/Command/` to 100% line coverage, driving the 21 command classes
through the hermetic CLI harness rather than by instantiating them directly.

## Skills Required

`phpunit` for coverage analysis; `symfony-console` for `CommandTester`-driven
testing of command behaviour.

## Acceptance Criteria

- [ ] `vendor/bin/phpunit --coverage-text` reports 100% line coverage for `src/Command/`.
- [ ] Commands are exercised through the task 12 harness and `CommandTester`, not by direct instantiation and method calls.
- [ ] Every command registered by `bin/upkeep` has at least one test that runs it to completion and asserts on its output and exit code.
- [ ] Error and failure paths within commands are covered, not only happy paths.
- [ ] Every `@codeCoverageIgnore` added carries an adjacent justifying comment.
- [ ] `./bin/upkeep list` exits 0 and every command still registers.
- [ ] `vendor/bin/phpunit` passes with docker stopped, networking disabled, and no GitLab token, still completing in seconds.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- 21 classes in `src/Command/`, including `AbstractMrCommand` and
  `VersionOptionInput`, plus whatever structure task 9 left behind.
- The task 12 harness supplies the temporary cockpit, the fake engine, and
  mocked GitLab.
- Commands are thin by convention and delegate to the domain namespaces, so
  most command-layer coverage comes from driving them end to end.

## Input Dependencies

- Task 12: the hermetic CLI harness and its existing exit-code and precedence
  tests, which this task extends to the full command set.

## Output Artifacts

- 100% line coverage for `src/Command/`.
- Justified `@codeCoverageIgnore` annotations, which task 17 audits.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Extend the task 12 harness rather than building a parallel approach. If the
   harness needs another capability to reach a command, add it there so every
   command test shares one path.

2. Enumerate the registered commands from `bin/upkeep` and check each against
   the coverage report. The 21 classes include several with no dedicated test
   file today — `ApiProbeCommand`, `BaseArtifactsBuildCommand`,
   `BaseArtifactsStatusCommand`, `CheckCommand`, `InitCommand`,
   `ModulesCommand`, `NotesCommand`, `ReviewCommand` — but confirm against the
   measured report rather than assuming that list is exactly the gap.

3. Cover failure paths, not just success. Commands map domain failures onto the
   0/1/2 exit-code contract, and the mapping is the interesting behaviour. A
   command tested only on its happy path leaves its most important logic
   unverified.

4. Drive through `CommandTester`. Instantiating a command and calling
   `execute()` directly bypasses the input parsing and application wiring that
   these tests exist to verify.

5. Keep it hermetic: temporary cockpits, faked engine, mocked HTTP, environment
   saved and restored.

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

Applied here: this is the namespace where "mostly integration" is most
literally right. Do not write unit tests for `configure()` methods or option
definitions — run the command and assert what it did.

</details>
