---
id: 14
group: "orchestrator-commands"
dependencies: [12]
status: "pending"
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
- [ ] `upkeep merge --fast-lane` with at least one READY-AUTO row present: shows the row's full context (module, MR, title, CI, local results summary), prompts per-MR with explicit choices (merge / skip / quit), and performs the merge ONLY after an affirmative response — demonstrated live on a real designated MR, with the MR verifiably `merged` afterwards via the GitLab UI or `api:probe`.
- [ ] Declining ("skip") performs no API write for that MR; quitting exits the loop immediately; there is no flag that merges without prompting (verify `--help` output offers none).
- [ ] Rows not READY-AUTO are never offered for merge (REVIEW/BLOCKED rows are listed only in a non-actionable summary).
- [ ] Degraded path: when the client returns `EndpointClosed` for merge, the approved action prints the exact browser merge URL for that MR and marks the row handled-manually — covered by a unit test with the typed failure injected.
- [ ] A merge failure (conflict, 405, permissions) is reported per-MR with the reason and the loop continues to the next row without aborting.

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

<details>
<summary>Detailed steps</summary>

1. Assemble current rows via the shared dashboard service; partition READY-AUTO vs rest.
2. Loop READY-AUTO rows: render context block → prompt (merge/skip/quit) → on merge: freshness re-check (client re-fetch MR + head pipeline; compare SHA + statuses; on drift, print reason and demote/skip) → client `merge` → report outcome. On `EndpointClosed`: print browser merge URL.
3. End-of-run summary: merged / handed-to-browser / skipped / demoted counts.
4. Unit tests: freshness-drift demotion, degraded path, and that no code path merges without an affirmative prompt response (assert the prompt is consulted).
</details>
