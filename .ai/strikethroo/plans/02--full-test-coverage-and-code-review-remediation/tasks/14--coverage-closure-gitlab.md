---
id: 14
group: "test-coverage"
dependencies: [6, 10]
status: "pending"
created: 2026-08-02
skills:
  - phpunit
  - php
complexity_score: 5
---
# Close coverage to 100% across the GitLab namespace

## Objective

Bring `src/Gitlab/` to 100% line coverage using mocked HTTP, including the full
error taxonomy and every token-resolution branch, without any network access.

## Skills Required

`phpunit` for test authoring and coverage analysis; `php` for HTTP mocking and
fixture construction.

## Acceptance Criteria

- [ ] `vendor/bin/phpunit --coverage-text` reports 100% line coverage for `src/Gitlab/`.
- [ ] Every exception type reachable from `GitlabClient` is covered by a test that triggers its actual condition, not by direct instantiation.
- [ ] All token-resolution branches are covered: env var set, env var empty or whitespace, config file readable, config file empty, config file absent, and the `XDG_CONFIG_HOME` versus `HOME` fallback in `defaultConfigFile()`.
- [ ] No test performs real network I/O — the suite passes with networking disabled.
- [ ] No test reads or writes the real `~/.config/upkeep/drupal-pat`.
- [ ] Every `@codeCoverageIgnore` added carries an adjacent justifying comment.
- [ ] `vendor/bin/phpunit` passes.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- 13 classes: `GitlabClient`, `TokenResolver`, the models (`MergeRequest`,
  `MergeRequestList`, `Pipeline`, `PipelineStatus`, `Project`, `Tag`), and the
  exception types (`ApiFailure`, `EndpointClosed`, `NotFound`, `RateLimited`,
  `TransportError`).
- `symfony/http-client` provides `MockHttpClient` and `MockResponse`.
- `TokenResolver` accepts an injectable `$configFile` in its constructor —
  use it for fixtures rather than touching the real path.

## Input Dependencies

- Task 6: the token-redaction changes and their tests.
- Task 10: the settled error taxonomy (task 8, via task 10) and the boundary
  typing.
- Task 2: the coverage report identifying actual gaps.

## Output Artifacts

- 100% line coverage for `src/Gitlab/`.
- Justified `@codeCoverageIgnore` annotations, which task 17 audits.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Work from the measured coverage report for `src/Gitlab/`, not from a list of
   classes missing a `*Test.php` file. Several models are value objects already
   covered transitively.

2. Cover exception types by triggering their real conditions through
   `MockHttpClient` — a 404 response producing `NotFound`, a 429 producing
   `RateLimited`, a transport failure producing `TransportError`, and so on.
   Directly instantiating an exception to cover its constructor is the kind of
   coverage-without-verification the plan treats as a defect.

3. `TokenResolver` has more branches than it appears: `resolve()` covers env
   var set, env var whitespace-only, config file readable with content, config
   file readable but empty, and config file absent. `defaultConfigFile()` adds
   the `XDG_CONFIG_HOME` set, unset, and empty-string cases plus the `HOME`
   fallback to `~`. Cover all of them by injecting `$configFile` and
   manipulating the environment with save/restore in `setUp()`/`tearDown()`.

4. Never point a test at the real `~/.config/upkeep/drupal-pat`. Use a
   temporary fixture file.

5. Sentinel discipline carries over from task 6: if a test involves a token
   value, use an obvious sentinel so any leak into output is unmistakable.

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

**Reconciling this with the 100% bar**: value objects and models reach 100%
transitively through the client tests that produce them. Do not write dedicated
tests asserting that a readonly model's constructor assigns its properties.

</details>
