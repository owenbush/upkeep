---
id: 16
group: "test-coverage"
dependencies: [7, 11]
status: "pending"
created: 2026-08-02
skills:
  - phpunit
  - php
complexity_score: 5
---
# Close coverage to 100% across the remaining namespaces

## Objective

Bring the remaining namespaces — `BaseArtifact`, `Cockpit`, `Config`,
`Dashboard`, `Drupal`, `Gate`, `Maintenance`, `Notes`, `Results`, and
`Workflow` — to 100% line coverage.

## Skills Required

`phpunit` for test authoring and coverage analysis; `php` for fixture
construction and introducing seams where needed.

## Acceptance Criteria

- [ ] `vendor/bin/phpunit --coverage-text` reports 100% line coverage for each of `src/BaseArtifact/`, `src/Cockpit/`, `src/Config/`, `src/Dashboard/`, `src/Drupal/`, `src/Gate/`, `src/Maintenance/`, `src/Notes/`, `src/Results/`, and `src/Workflow/`.
- [ ] Gate classification is covered for all three outcomes: READY-AUTO, REVIEW, and BLOCKED.
- [ ] The exit-code contract in `src/Workflow/` is covered at the unit level, complementing the end-to-end assertions from task 12.
- [ ] `registry.yml` parsing is covered including malformed and missing-field cases.
- [ ] Every `@codeCoverageIgnore` added carries an adjacent justifying comment.
- [ ] No test touches a real cockpit, the real `~/.upkeep/`, docker, or the network.
- [ ] `vendor/bin/phpunit` passes and still completes in seconds.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- Roughly 43 classes across ten namespaces, spanning fast-lane gate
  classification, dashboard row assembly, cached results, base-artifact build
  and scan, prune/status inventory, release-notes drafting, and config
  resolution.
- Filesystem-touching namespaces (`BaseArtifact`, `Cockpit`, `Results`) need
  temporary-directory fixtures.
- Task 7 may have changed on-disk formats or write behaviour; test against the
  post-remediation shape.

## Input Dependencies

- Task 7: the filesystem and result-file remediation and its tests.
- Task 11: the level-max type work across these namespaces.
- Task 2: the coverage report identifying actual gaps.

## Output Artifacts

- 100% line coverage for all remaining namespaces, completing the `src/` total
  alongside tasks 13, 14, and 15.
- Justified `@codeCoverageIgnore` annotations, which task 17 audits.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Work from the measured coverage report. A large share of these classes are
   enums and value objects (`Category`, `PruneScope`, `GateStatus`,
   `InventoryItem`, `DashboardRow`, `CachedResult`, `ArtifactRecord`,
   `ByteFormat`) that will already be covered transitively by the behaviour
   tests around them.

2. `src/Gate/` is small but carries real business logic — fast-lane
   classification into READY-AUTO, REVIEW, and BLOCKED. This is exactly the
   custom decision logic the test philosophy says to prioritise. Cover the
   boundaries between the three outcomes, not just one example of each.

3. `src/Workflow/` owns the exit-code contract. Task 12 asserts it end to end;
   cover the mapping logic directly here too, since it is the tool's
   machine-readable interface.

4. `src/Cockpit/` parses `registry.yml`. Cover malformed YAML, missing required
   fields, and empty registries — error paths in config parsing are where real
   users hit problems.

5. `ByteFormat` in `src/Maintenance/` is a formatting helper: cover its
   boundaries (zero, exact powers of 1024, very large values) in one test
   rather than one test per unit suffix.

6. Use temporary-directory fixtures for anything filesystem-touching and clean
   up in `tearDown()`.

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

**Reconciling this with the 100% bar**: the enums and value objects listed
above reach 100% transitively through the tests that exercise the logic
consuming them. Cover them that way rather than writing dedicated
constructor-assignment tests — this namespace group has the highest
concentration of such classes and is where that mistake is most tempting.

</details>
