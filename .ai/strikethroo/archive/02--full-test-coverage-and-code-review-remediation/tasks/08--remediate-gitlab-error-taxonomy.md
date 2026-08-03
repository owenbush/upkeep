---
id: 8
group: "review-and-remediation"
dependencies: [5]
status: "completed"
created: 2026-08-02
skills:
  - php
  - api-client
complexity_score: 5
---
# Remediate GitLab error-taxonomy consistency

## Objective

Make the `src/Gitlab/` exception hierarchy and failure handling internally
consistent, so that every API failure mode maps to exactly one well-defined
type with predictable propagation.

## Skills Required

`php` for the refactor; `api-client` for correct HTTP error-condition modelling
against `symfony/http-client`.

## Acceptance Criteria

- [ ] Every best-practice finding concerning the `Gitlab` namespace in `review-findings.md` is resolved, and the record is updated with each resolution.
- [ ] Each of `ApiFailure`, `EndpointClosed`, `NotFound`, `RateLimited`, and `TransportError` has a documented, non-overlapping trigger condition, and the hierarchy's relationships are consistent.
- [ ] Every HTTP failure mode reachable from `GitlabClient` maps to exactly one of those types; no failure escapes as a raw Symfony HttpClient exception.
- [ ] Tests cover the mapping from HTTP status and transport condition to exception type, using the existing mocked-HTTP approach.
- [ ] `vendor/bin/phpunit` passes.
- [ ] `grep -r "ddev" src/ --exclude-dir=Adapter` returns nothing.
- [ ] No network access is introduced — all GitLab tests remain mocked.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- `src/Gitlab/` contains 13 classes: `GitlabClient`, `TokenResolver`, the
  models (`MergeRequest`, `MergeRequestList`, `Pipeline`, `PipelineStatus`,
  `Project`, `Tag`), and the exception types (`ApiFailure`, `EndpointClosed`,
  `NotFound`, `RateLimited`, `TransportError`).
- The client targets git.drupalcode.org and uses `symfony/http-client`.
- No backwards-compatibility constraint applies: exception class names,
  hierarchy, and constructor signatures may all change.

## Input Dependencies

- Task 5: the findings record, specifically the `Gitlab` best-practice
  findings.

## Output Artifacts

- A consistent `Gitlab` error taxonomy.
- Tests covering status-to-exception mapping.
- Updated `review-findings.md` with resolutions recorded.
- A settled `Gitlab` class structure that task 10 then types to PHPStan max.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Read `review-findings.md` and work only the `Gitlab` findings assigned here.

2. Start by tabulating the current state: for each exception type, what
   actually throws it and under what condition. Overlaps and gaps in that table
   are the defects to fix. `EndpointClosed` and `NotFound` in particular are
   easy to conflate — a 404 from a missing project and a deliberately disabled
   endpoint are different conditions and callers may need to distinguish them.

3. Decide whether these types share a common base. A single base exception for
   the namespace usually makes `catch` blocks in the command layer far
   simpler, but only if the distinctions callers actually need are preserved.
   Let the findings and the real call sites decide, not symmetry for its own
   sake.

4. Ensure nothing escapes as a raw `Symfony\Component\HttpClient` exception —
   transport failures, timeouts, and malformed responses all need mapping.
   `TransportError` presumably owns most of these; confirm it covers them all.

5. Use the existing mocked-HTTP test approach; do not introduce real network
   calls. `symfony/http-client` provides `MockHttpClient` and `MockResponse`
   for exactly this.

6. No-BC applies: rename, re-parent, or merge exception types freely if the
   findings justify it. Update every call site.

7. Coordinate with task 9, which handles the command-layer and adapter-boundary
   findings. If a `Gitlab` change affects how commands catch and report errors,
   note it in the task output so task 9 picks it up — the two run in parallel.

</details>
