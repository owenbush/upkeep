---
id: 12
group: "orchestrator-commands"
dependencies: [9, 11]
status: "pending"
created: 2026-07-29
skills:
  - symfony-console
  - php
complexity_score: 4
---
# Implement the dashboard command and fast-lane gate classification

## Objective
Implement `upkeep dashboard`: one table of every open MR across all registered modules and core versions — columns MODULE, MR, CORE, TITLE, CI, LOCAL, STATUS — with the derived status produced by the fast-lane gate classifier (READY-AUTO for trusted bot compat MRs with green CI and green local checks; REVIEW with reason; BLOCKED with reason). The gate is deliberately conservative: anything not matching the trusted pattern routes to manual review.

## Skills Required
`symfony-console` for the command and table rendering; `php` for the gate classifier.

## Acceptance Criteria
- [ ] `upkeep dashboard` against the real registry renders one row per open MR per tracked core version, with live CI status from GitLab; output matches the design doc's dashboard sketch shape.
- [ ] The gate classifier is a pure, unit-tested function: given (MR author, source branch, title, CI status, local check results, target core version) it returns READY-AUTO only when ALL hold — author/branch match task 2's verified bot pattern parameterized by target core version, CI green, local checks green; every other combination yields REVIEW or BLOCKED with a machine-readable reason. `vendor/bin/phpunit` covers: bot MR all-green → READY-AUTO; non-bot author all-green → REVIEW; bot MR red CI → BLOCKED(CI); bot MR red local check → REVIEW(with failing check name); unknown/missing CI → not READY-AUTO.
- [ ] LOCAL column shows the latest cached local check result for that (module, MR, core) if one exists, else `-`; the dashboard itself never triggers environment provisioning (read-only aggregation, verified: running it creates no new ddev projects).
- [ ] Closed-endpoint conditions from the client render as an explicit cell state (e.g. `n/a (403)`), not a crash — verified with the client's typed failure injected in a test.
- [ ] `upkeep dashboard --version=<N>` filters to one target core version.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Symfony Console Table rendering; sensible sort (module, then MR).
- A local results cache: task 13's `check` runs persist their `CheckResult`s per (module, MR iid, core version, MR head SHA) under the cockpit; the dashboard reads this cache and shows staleness (result for an older SHA renders as stale/`-`).
- The gate's bot pattern (author username(s), branch pattern) lives in config with defaults from task 2's verification — parameterized by target core version, not hard-coded to one.

## Input Dependencies
- Task 9's client (MR + pipeline data, typed failures).
- Task 11's `CheckResult` shape (cache format shared with task 13).

## Output Artifacts
- The dashboard command and the gate classifier + results cache, consumed directly by task 14 (fast-lane merge acts on READY-AUTO rows) and task 17 (tests target the classifier).

## Implementation Notes
The gate classifier is the highest-consequence pure logic in the tool (a misclassification merges the wrong thing — mitigated by conservatism plus the per-MR human approval in task 14). Keep it a standalone, dependency-free class so task 17's tests hit it directly.

<details>
<summary>Detailed steps</summary>

1. Define the results-cache layout under the cockpit (e.g. `<cockpit>/results/<module>/<mr-iid>/<core>/<sha>.json`) and a small reader/writer service shared with task 13.
2. Dashboard flow: registry → for each module, client `openMergeRequests` → for each MR × each tracked core version, assemble row (CI from head pipeline; LOCAL from cache; STATUS from gate). Render table. `--version` filter.
3. Gate classifier per the acceptance truth table; reasons are enum-like strings (`ci-red`, `local-failed:<check>`, `not-bot-author`, ...).
4. Unit tests for the classifier truth table and a rendering test with injected fake client returning typed failures.
</details>
