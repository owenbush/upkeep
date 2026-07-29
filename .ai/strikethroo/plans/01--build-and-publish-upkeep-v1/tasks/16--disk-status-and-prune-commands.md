---
id: 16
group: "orchestrator-commands"
dependencies: [10, 3]
status: "pending"
created: 2026-07-29
skills:
  - symfony-console
  - ddev
complexity_score: 4
---
# Implement disk status and prune commands

## Objective
Implement the maintenance surface: `upkeep status --disk` reports disk usage per module/version/category, and `upkeep prune` variants (`--trees`, `--snapshots`, `--projects`, `--all`, with `--older-than=<duration>`) reclaim disposable state — dry-run by default on anything destructive, with hard protection for canonical artifacts (base artifacts, keep-marked snapshots, committed `tests/fixtures/` dumps).

## Skills Required
`symfony-console` for the commands; `ddev` for volume/project reclamation via the adapter's teardown.

## Acceptance Criteria
- [ ] `upkeep status --disk` prints a table attributing real measured sizes (module × core-version trees, materialized snapshots, project volumes, base artifacts) with totals — spot-check one row against `du -sh` of the same path.
- [ ] Every `prune` variant run without `--yes` (or equivalent confirmation flag) performs NO deletion and prints the candidates with reclaimable sizes and a total — verified by running each variant and confirming `status --disk` output is unchanged afterwards.
- [ ] A confirmed `prune --trees --older-than=30d` deletes only qualifying disposable trees; base artifacts, keep-marked snapshots, and any `tests/fixtures/` dump are provably untouched (list before/after).
- [ ] `prune --projects` tears down stale environments through the adapter's teardown (volumes actually reclaimed per task 3's table — `docker volume ls` confirms); `prune --snapshots` honors keep-latest-N per module.
- [ ] A pruned environment is regenerable: after pruning a tree, `upkeep check` for that (module × core-version) re-provisions successfully (run it).
- [ ] Protection logic is unit-tested: candidate selection never yields a path under `base-artifacts/`, a keep-marked snapshot, or a committed fixtures directory (`vendor/bin/phpunit` passes with these cases).

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Age determination from the per-project meta task 10 stores (creation/last-used timestamps; update last-used on each adapter reuse).
- Deletion of environments only via the adapter's `teardown` — no raw `docker`/`ddev` calls in command code.
- Duration parsing for `--older-than` (`30d`, `12h`); default when omitted: no age filter (candidates listed, still dry-run).

## Input Dependencies
- Task 10's project meta and teardown.
- Task 3's reclamation table (what each mechanism actually frees) — encoded in the adapter, relied on here.

## Output Artifacts
- `status --disk` and the prune command family; validated end-to-end in task 19's checklist and targeted by task 17's protection tests.

## Implementation Notes
The safety property is the product here: dry-run-by-default plus canonical-artifact protection is what makes aggressive reclamation acceptable. Implement candidate selection as a pure function over an inventory snapshot so the protection tests are trivial to write.

<details>
<summary>Detailed steps</summary>

1. Inventory service: walk the cockpit's projects root, base artifacts, snapshot store, and registered modules' `tests/fixtures/`; produce a typed inventory (path, category, module, core version, size via `du`, age, canonical flag, keep flag).
2. `status --disk`: render the inventory grouped module → version → category with totals.
3. Candidate selector (pure): inventory + variant + age filter → deletion candidates; canonical/keep/committed always excluded regardless of flags.
4. Prune command: render candidates + total reclaim; without confirmation flag stop there; with it, execute (trees: rm via filesystem; projects: adapter teardown; snapshots: keep-latest-N then delete rest) and report actual freed space.
5. Unit tests for the selector's protection guarantees and age filtering; live verification per acceptance criteria.
</details>
