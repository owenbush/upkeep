---
id: 2
group: "quality-tooling"
dependencies: []
status: "completed"
created: 2026-08-02
skills:
  - phpunit
  - php
complexity_score: 3
---
# Add coverage driver and PHPUnit coverage reporting

## Objective

Make test coverage measurable locally by adding a coverage driver and
configuring PHPUnit to produce a coverage report, without yet enforcing a
threshold.

## Skills Required

`phpunit` for coverage configuration in `phpunit.xml.dist`; `php` for driver
installation and extension configuration.

## Acceptance Criteria

- [ ] A coverage driver (PCOV preferred, Xdebug acceptable) is available and documented as a development prerequisite.
- [ ] `phpunit.xml.dist` retains its existing `bootstrap`, `colors`, `cacheDirectory`, `failOnRisky`, and `failOnWarning` settings unchanged.
- [ ] `phpunit.xml.dist` produces a coverage report covering `src/` only.
- [ ] `vendor/bin/phpunit --coverage-text` prints a per-namespace coverage table and an overall line-coverage percentage.
- [ ] The measured baseline line-coverage percentage for `src/` is recorded in the task output.
- [ ] No coverage threshold is enforced yet — a run below 100% still exits 0.
- [ ] The suite still runs offline: `vendor/bin/phpunit` passes with docker stopped and no network.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- PCOV is preferred over Xdebug: it is substantially faster and the enforced
  metric is line coverage, which PCOV supports fully.
- Known tradeoff: PCOV cannot produce branch or path coverage. If branch
  coverage is ever required the driver must change to Xdebug. Line coverage is
  what this plan enforces, so PCOV is sufficient.
- The existing `<source><include><directory>src</directory></include></source>`
  block already scopes coverage correctly; extend rather than replace it.

## Input Dependencies

None. This is a zero-dependency foundation task.

## Output Artifacts

- Updated `phpunit.xml.dist` with coverage reporting configured.
- Documented coverage-driver prerequisite.
- Baseline line-coverage percentage, reported for downstream coverage tasks.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Confirm which driver is present: `php -v` will list PCOV or Xdebug if
   loaded. If neither is available locally, install PCOV
   (`pecl install pcov`) or document the requirement clearly.

2. In `phpunit.xml.dist`, add a `<coverage>` element configuring report output.
   Add an HTML or text report as convenient, but the text report is what the
   acceptance criteria and later validation depend on. Keep the existing
   `<source>` block — it already restricts coverage to `src/`, which is
   correct; `tests/` must not be included in the coverage metric.

3. Do NOT add `requireCoverageMetadata`, and do NOT set a minimum coverage
   threshold in this task. Enforcement is task 18's job, deliberately deferred
   until the coverage work is actually complete. Setting it now would fail
   every intermediate run.

4. Run `vendor/bin/phpunit --coverage-text` and record the overall line
   coverage figure plus the per-namespace breakdown. Tasks 13 through 16
   consume this to target their work — note that the report, not the count of
   missing `*Test.php` files, is the authority on what is uncovered. Many value
   objects and enums will already show as covered transitively.

5. Verify the hermetic property survived: stop docker, disable networking, and
   confirm `vendor/bin/phpunit` still passes.

</details>
