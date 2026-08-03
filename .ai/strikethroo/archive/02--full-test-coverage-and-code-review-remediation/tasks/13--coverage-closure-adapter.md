---
id: 13
group: "test-coverage"
dependencies: [6, 11]
status: "completed"
created: 2026-08-02
skills:
  - phpunit
  - php
complexity_score: 6
complexity_notes: "The hardest coverage surface: 22 classes orchestrating subprocesses and filesystem state, where covering without executing real commands requires working through existing seams."
---
# Close coverage to 100% across the adapter namespace

## Objective

Bring `src/Adapter/` to 100% line coverage using the existing adapter seams and
fakes, without executing ddev, docker, or any real subprocess orchestration.

## Skills Required

`phpunit` for test authoring and coverage analysis; `php` for introducing seams
where a class proves untestable.

## Acceptance Criteria

- [ ] `vendor/bin/phpunit --coverage-text` reports 100% line coverage for `src/Adapter/`.
- [ ] Coverage was driven by the measured report, not by the list of classes lacking a `*Test.php` file.
- [ ] Every `@codeCoverageIgnore` added carries an adjacent comment justifying why the line is genuinely unreachable.
- [ ] No test requires docker, the network, or a GitLab token — the suite passes with docker stopped and networking disabled.
- [ ] Tests assert on behaviour, not merely on line traversal; no test executes a path without asserting an outcome.
- [ ] `grep -r "ddev" src/ --exclude-dir=Adapter` returns nothing.
- [ ] `vendor/bin/phpunit` passes and still completes in seconds.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- 22 classes, including `DdevContribAdapter`, `ProcessRunner`, `VolumeProbe`,
  `ThrowawaySite`, `MrCheckout`, `EngineAddOn`, `FixtureAddOn`, and the value
  objects and enums (`CapturedProcess`, `CheckResult`, `CheckRunResult`,
  `CheckStatus`, `CheckType`, `Environment`, `EnvironmentMeta`, `ProjectName`,
  `ProjectsRoot`, `ServeResult`, `SnapshotLayout`, `WorkingCopyStatus`,
  `ModuleWiring`, `AdapterException`).
- The engine is pinned to ddev-drupal-contrib 1.1.5 in `Adapter\EngineAddOn`;
  the code references it but the suite must never invoke it.
- `EngineAdapterInterface` is the existing fake seam.

## Input Dependencies

- Task 6: the redaction changes to `ProcessRunner` and its tests.
- Task 11: the level-max type work across `src/Adapter/`.
- Task 2: the coverage report that identifies the actual gaps.

## Output Artifacts

- 100% line coverage for `src/Adapter/`.
- Any new seams introduced to make classes testable.
- Justified `@codeCoverageIgnore` annotations, which task 17 audits.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Start from `vendor/bin/phpunit --coverage-text` filtered to `src/Adapter/`.
   **The measured report is the authority.** The plan flags that many of the
   22 classes are enums and value objects already covered transitively, while
   some classes that do have test files will show uncovered branches. Do not
   work from filename matching.

2. Do not reach for real ddev execution to cover `DdevContribAdapter`. That
   class is covered through the adapter's own seams. Reaching for docker would
   destroy the suite's hermetic property, which is an explicit success
   criterion of this plan.

3. Where a class proves untestable without contortion, **change the class** —
   introduce a seam, inject a collaborator. No backwards-compatibility
   constraint applies, so this is available throughout. What is not available
   is lowering the bar or writing a test that asserts nothing.

4. The legitimate uses of `@codeCoverageIgnore` are: defensive branches against
   states the type system already excludes (common after task 11's narrowing
   work), and platform paths that cannot occur on the CI matrix. Every one
   needs an adjacent comment saying which it is and why. Task 17 audits the
   whole budget and reports its size — an unexamined ignore list would satisfy
   the letter of the 100% criterion and none of its intent.

5. Watch for tests that execute lines without asserting behaviour. Coverage
   measures execution, not verification; a suite can hit 100% while proving
   very little. The plan treats that as a defect.

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

**Reconciling this with the 100% bar**: these are not in conflict. Value
objects, enums, and trivial accessors reach 100% *transitively*, through the
behaviour tests that exercise the classes using them. Cover them that way.
Writing a dedicated test asserting that a readonly constructor assigns its
properties is exactly the gold-plating this philosophy forbids, and it is not
how the coverage bar should be met.

</details>
