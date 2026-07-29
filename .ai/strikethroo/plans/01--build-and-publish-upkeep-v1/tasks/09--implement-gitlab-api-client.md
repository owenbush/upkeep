---
id: 9
group: "orchestrator-core"
dependencies: [2, 7]
status: "pending"
created: 2026-07-29
skills:
  - php
  - gitlab-api
complexity_score: 4
---
# Implement the git.drupalcode.org GitLab API client

## Objective
Implement the orchestrator's GitLab client for git.drupalcode.org: authenticate with the maintainer's existing Git-access PAT via the `PRIVATE-TOKEN` header; list open MRs across registered modules; read pipeline/CI status; read tags and merged MRs since a tag (for release notes); perform a single MR merge. Built for a block-by-default instance: a 403 is an expected, typed condition — never a crash — and the client is rate-limit-friendly.

## Skills Required
`php` for the client and its typed results; `gitlab-api` for endpoint semantics on a stock GitLab instance.

## Acceptance Criteria
- [ ] With a real PAT configured, a thin debug command (e.g. `upkeep api:probe <module>`) prints: open MR count, one MR's IID/title/author/source-branch, and its head pipeline status — matching what the GitLab web UI shows for that module.
- [ ] Endpoints task 2 verified as closed return a typed `EndpointClosed` result carrying the HTTP status and the browser fallback URL — demonstrated by probing a known-closed endpoint (or a forced 403 in tests) without an unhandled exception.
- [ ] The PAT is read from environment/config, never appears in logs or exception messages (grep the debug output for the token value: zero matches).
- [ ] Rate-limit friendliness: responses are cached per-invocation (no duplicate GET for the same resource within one command run), and HTTP 429 produces a clear retry-later error, verified by a unit test with a mocked client.
- [ ] `vendor/bin/phpunit` passes with tests for: MR list parsing, pipeline status mapping, 403 → `EndpointClosed`, 429 handling, and merge-call request shape (mocked transport; no live merge in tests).

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- GitLab REST API v4; project path encoding (`project%2F<module>`); endpoints: project lookup, `merge_requests?state=opened&scope=all`, single MR, MR head pipeline, tags, merged MRs (`state=merged&updated_after=...` or compare via tag date), `PUT .../merge`.
- An HTTP client behind an interface so tests mock the transport (symfony/http-client is fine).
- Typed result objects for MR, pipeline status, and error conditions — the dashboard and gate consume these, not raw JSON.

## Input Dependencies
- Task 2's endpoint access matrix and bot-MR pattern (the client encodes which endpoints may be closed; author/branch fields must be surfaced for the gate).
- Task 7's scaffold (config for PAT, registry for module → project path).

## Output Artifacts
- `Upkeep\Gitlab` client + typed models, consumed by tasks 12 (dashboard), 13 (check/review), 14 (merge), 15 (notes).

## Implementation Notes
Design the merge operation as a single-action call with no batching affordance at the client level — the DA-policy stance (one human-approved action at a time) is enforced structurally, not just in the command layer. Expose MR author username and source branch on the MR model; the fast-lane gate (task 12) keys off exactly those fields per task 2's verified pattern.

<details>
<summary>Detailed steps</summary>

1. `GitlabClient` with constructor-injected transport + base URL + token provider. Methods: `project(path)`, `openMergeRequests(project)`, `mergeRequest(project, iid)`, `headPipeline(project, iid)`, `tags(project)`, `mergedSince(project, tagOrDate)`, `merge(project, iid)`.
2. Every method returns a typed success or a typed failure (`EndpointClosed(status, browserUrl)`, `RateLimited`, `NotFound`, `TransportError`). No method throws for HTTP-level outcomes.
3. In-run memoization keyed by URL for GETs.
4. `api:probe` debug command wiring it to the registry (kept — it is the live verification surface for this and later tasks).
5. PHPUnit tests with a mock transport per the acceptance list. No live-network tests in CI.
</details>
