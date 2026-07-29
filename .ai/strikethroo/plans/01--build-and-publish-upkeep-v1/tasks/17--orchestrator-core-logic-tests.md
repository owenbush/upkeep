---
id: 17
group: "testing"
dependencies: [12, 14, 16]
status: "pending"
created: 2026-07-29
skills:
  - phpunit
complexity_score: 3
---
# Consolidated tests for the orchestrator's high-consequence logic

## Objective
Ensure the orchestrator's three highest-consequence pure-logic areas have a coherent, complete test pass: the fast-lane gate classifier (misclassification merges the wrong thing), the merge command's freshness/approval guards, and the prune candidate selector's protection guarantees — consolidating and gap-filling the per-task tests written in tasks 9–16 into one reviewed suite.

## Skills Required
`phpunit` — this task is entirely test authoring/review against existing code.

## Acceptance Criteria
- [ ] `vendor/bin/phpunit` passes the full suite, and the CI workflow runs it green.
- [ ] Gate classifier: the complete truth table is covered including adversarial rows — bot author but wrong branch pattern → REVIEW; matching pattern but for a different core version → not READY-AUTO; missing/unknown CI state → not READY-AUTO.
- [ ] Merge guards: tests prove no code path reaches the client's merge call without an affirmative prompt response, and that head-SHA/CI drift between classification and approval demotes instead of merging.
- [ ] Prune protection: property-style cases prove candidates never include base artifacts, keep-marked snapshots, or committed `tests/fixtures/` paths for any variant/flag combination exercised.
- [ ] GitLab client degradation: 403 → typed `EndpointClosed` and 429 → retry-later are asserted (may already exist from task 9 — verify present, don't duplicate).
- [ ] No new tests for trivial functionality were added (rendering cosmetics, config getters); the suite remains focused per the test philosophy below.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- PHPUnit with mocked transport/adapter — zero network, zero ddev in tests.
- Extend the existing suite in place; refactor production code only if a seam is genuinely missing for a required assertion.

## Input Dependencies
- Task 12's classifier, task 14's merge command, task 16's selector (plus task 9's client tests as the baseline).

## Output Artifacts
- The consolidated green suite gating task 19 (publication requires green CI).

## Implementation Notes
Test philosophy (apply verbatim): **write a few tests, mostly integration.** Meaningful tests verify custom business logic, critical paths, and edge cases specific to this application — test *your* code, not the framework or library. Write tests for: custom business logic and algorithms, critical workflows and data transformations, edge cases and error conditions for core functionality, integration points between components, complex validation logic. Do NOT write tests for: third-party library functionality, framework features, simple CRUD without custom logic, trivial getters/setters, or obvious functionality that would break immediately if incorrect. Combine related scenarios into single tests; favor integration and critical-path coverage over per-method unit tests; question whether simple functions need a dedicated test at all.

<details>
<summary>Detailed steps</summary>

1. Inventory existing tests from tasks 7–16; map them against the acceptance list; write only the gaps.
2. Prefer table-driven tests for the classifier truth table (one test method, many cases).
3. For the merge-approval guarantee, use a spy client + scripted prompt responses covering merge/skip/quit/drift sequences.
4. For prune protection, generate inventories mixing canonical and disposable entries and assert the selector's exclusions across all variants.
5. Run the suite + CI; done when green.
</details>
