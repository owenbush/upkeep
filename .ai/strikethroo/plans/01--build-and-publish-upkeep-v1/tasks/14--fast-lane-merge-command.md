---
id: 14
group: "orchestrator-commands"
dependencies: [12]
status: "completed"
created: 2026-07-29
skills:
  - symfony-console
  - gitlab-api
complexity_score: 3
---
# Implement the human-triggered fast-lane merge command

## Objective
Implement `upkeep merge --fast-lane`: present the current READY-AUTO rows one at a time; on an explicit per-MR human approval (a keypress), perform that single merge via the GitLab client — one individual action the user could have done in the browser. If task 2 verified the merge endpoint closed, the approved action instead prints/opens the browser merge URL (the documented degraded path). No batch mode exists.

## Skills Required
`symfony-console` for the interactive prompt loop; `gitlab-api` for the single-merge call semantics.

## Acceptance Criteria
- [x] `upkeep merge --fast-lane` with at least one READY-AUTO row present: shows the row's full context (module, MR, title, CI, local results summary), prompts per-MR with explicit choices (merge / skip / quit), and performs the merge ONLY after an affirmative response — implemented and covered by CommandTester tests with scripted inputs and a request-recording mock transport. The LIVE merge demonstration on a real designated MR is DEFERRED by coordinator decision (see Implementation Notes).
- [x] Declining ("skip") performs no API write for that MR; quitting exits the loop immediately; there is no flag that merges without prompting (verified: the command's option surface is exactly `--fast-lane` and `--cockpit`, and live `merge --help` output offers no bypass flag; additionally a non-interactive run merges nothing).
- [x] Rows not READY-AUTO are never offered for merge (REVIEW/BLOCKED rows are listed only in a non-actionable summary) — verified live against the real cockpit: field_visibility_conditions !2 rendered as `REVIEW (draft, ci-missing, local-failed:phpunit)` with no prompt, exit 0.
- [x] Degraded path: when the client returns `EndpointClosed` for merge, the approved action prints the exact browser merge URL for that MR and marks the row handled-manually — covered by a unit test with the typed failure injected (403 on the merge PUT).
- [x] A merge failure (conflict, 405, permissions) is reported per-MR with the reason and the loop continues to the next row without aborting — covered by a two-row test (405 on the first merge; the second row is still prompted and merged).

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Reuses task 12's gate classification and dashboard row assembly (shared service — do not re-derive status here).
- Merge via task 9's single-action `merge(project, iid)`; re-verify freshness immediately before merging (MR still open, head SHA unchanged since classification, CI still green) and demote to REVIEW if anything moved.
- Symfony Console question helper for the per-row prompt.

## Input Dependencies
- Task 12's gate classifier, row assembly, and results cache.
- Task 9's merge operation and typed failures (transitively via 12).

## Output Artifacts
- The `merge --fast-lane` command completing the fast-lane workflow; exercised again by task 17's tests (freshness re-check logic) and task 19's validation.

## Implementation Notes
The DA-policy stance is structural: one prompt, one approval, one API call. The freshness re-check before each merge is the guard against acting on stale classification (an MR updated between dashboard aggregation and approval). Keep releases untouched — this command merges; it never tags.

### Completion notes (2026-07-30)

- **Live-merge demonstration DEFERRED (coordinator decision).** No live merge call was made against git.drupalcode.org. There is no safe live merge target: the only open MR (field_visibility_conditions !2) is a draft with a genuinely failing test and correctly classifies REVIEW, and the user has placed publication/merging under an explicit hold pending their local testing. The live-merge demonstration is deferred to the user's local-testing session with a maintainer-designated safe MR — consistent with the phase-1 finding that the merge endpoint on git.drupalcode.org remains unverified (an `EndpointClosed` outcome is expected and is the tested degraded path). Simulating a merge against the real instance to satisfy the criterion was deliberately not done.
- **Live verification performed instead (zero-READY-AUTO path):** `merge --fast-lane` against the real cockpit (`/Users/owen/.upkeep-task10-scratch/cockpit`, `UPKEEP_PROJECTS_ROOT=/Users/owen/.upkeep-task10-scratch/projects`, token resolved from `~/.config/upkeep/drupal-pat`, never printed) rendered fvc !2 in the non-actionable summary as `REVIEW (draft, ci-missing, local-failed:phpunit)`, offered no prompt, exit 0. Live `merge --help` shows only `--fast-lane` and `--cockpit` — no bypass flag.
- **Row-assembly refactor:** shared service extracted as `Upkeep\Dashboard\RowAssembler` producing structured `Upkeep\Dashboard\DashboardRow` values (Project + MergeRequest + CachedResult + GateVerdict + typed failures, with the dashboard's cell formatting as row methods). `DashboardCommand` now renders `DashboardRow::toTableCells()`; its tests stayed green throughout (pure refactor).
- **Freshness re-check:** `GitlabClient::fresh()` (new, TDD'd) returns an unmemoized copy so the pre-merge re-fetch observes the MR as it is now; explicit state/SHA comparisons plus a full `FastLaneGate` re-classification demote on `state-changed:*`, `sha-drift`, CI regression, new draft, or stale/failed local evidence — and a failed re-fetch also demotes (no re-verification, no merge). The merge PUT carries the fresh head SHA as the API-side race guard.
- **Follow-up for the coordinator:** register `MergeCommand` in `bin/upkeep` (deliberately not edited this phase).

<details>
<summary>Detailed steps</summary>

1. Assemble current rows via the shared dashboard service; partition READY-AUTO vs rest.
2. Loop READY-AUTO rows: render context block → prompt (merge/skip/quit) → on merge: freshness re-check (client re-fetch MR + head pipeline; compare SHA + statuses; on drift, print reason and demote/skip) → client `merge` → report outcome. On `EndpointClosed`: print browser merge URL.
3. End-of-run summary: merged / handed-to-browser / skipped / demoted counts.
4. Unit tests: freshness-drift demotion, degraded path, and that no code path merges without an affirmative prompt response (assert the prompt is consulted).
</details>
