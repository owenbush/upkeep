---
id: 9
group: "review-and-remediation"
dependencies: [5]
status: "pending"
created: 2026-08-02
skills:
  - php
  - symfony-console
complexity_score: 5
---
# Remediate command-class duplication and adapter-boundary compliance

## Objective

Resolve the best-practice findings in the command layer — duplication across
the 21 command classes and misplaced responsibilities — and fix any breach of
the adapter boundary.

## Skills Required

`php` for the refactor; `symfony-console` for correct command, input, and
output design.

## Acceptance Criteria

- [ ] Every best-practice finding concerning `src/Command/`, `src/Workflow/`, or the adapter boundary in `review-findings.md` is resolved, and the record is updated with each resolution.
- [ ] `grep -r "ddev" src/ --exclude-dir=Adapter` returns nothing.
- [ ] Commands remain thin and delegate to the domain namespaces, per the documented convention.
- [ ] The exit-code contract (0 pass / 1 check failed / 2 infrastructure) is preserved in behaviour and is expressed in one place rather than duplicated across commands.
- [ ] `./bin/upkeep list` exits 0 and every command still registers.
- [ ] `vendor/bin/phpunit` passes.
- [ ] Any change to a command name, option, or exit code is explicitly listed in the task output, since task 19 must document it and task 12 must test against the final surface.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- `src/Command/` holds 21 classes including `AbstractMrCommand` (the existing
  shared base) and `VersionOptionInput`.
- `src/Workflow/` owns the shared MR-flow context and the exit-code contract.
- Documented conventions that must survive: cockpit resolved as `--cockpit` >
  `UPKEEP_COCKPIT` > cwd; projects root as `--projects-root` >
  `UPKEEP_PROJECTS_ROOT` > `~/.upkeep/projects`; `--version` on
  check/review/dashboard is the *target core* selector, never an app-version
  flag.
- **Hard policy constraint**: merges are one-human-approval-per-MR by Drupal
  Association policy. Never add a batch or unattended merge path. This is a
  policy stance, not a design preference.
- No backwards-compatibility constraint applies: command names, options, and
  exit codes may change.

## Input Dependencies

- Task 5: the findings record, specifically the command-layer and
  adapter-boundary findings.

## Output Artifacts

- A deduplicated command layer.
- A settled CLI surface that task 12 writes end-to-end tests against.
- A list of any CLI-surface changes, for tasks 12 and 19.
- Updated `review-findings.md` with resolutions recorded.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Read `review-findings.md` and work only the findings assigned here.

2. `AbstractMrCommand` already exists as the shared base for MR-flow commands.
   Prefer extending that existing seam over inventing a new abstraction —
   PRE_PLAN's simplicity principles apply, and "build abstractions when simple
   solutions suffice" is a named anti-pattern.

3. The adapter boundary is the project's central invariant: engine specifics
   (ddev, ddev-drupal-contrib, docker, container and volume names) live only in
   `src/Adapter/`, and everything else talks to `EngineAdapterInterface`. When
   deduplicating, watch for the tempting shortcut that pulls engine knowledge
   up into a shared command base. If a change needs engine knowledge outside
   the adapter, grow the interface instead. Run the grep guard after every
   structural change, not just at the end.

4. The exit-code contract lives in `src/Workflow/`. If it is currently
   re-implemented across command classes, consolidating it is exactly the kind
   of finding this task exists to fix — and it makes task 12's end-to-end
   assertions meaningful.

5. Do not add a batch or unattended merge path under any circumstances, even if
   deduplication seems to invite one. This is a DA policy constraint recorded
   in the README's policy stance.

6. No-BC applies, so you may rename commands or change flags where a finding
   justifies it. But every such change has downstream cost: task 12 tests the
   surface and task 19 documents it. List them explicitly in your output.

7. Coordinate with task 8 (running in parallel on the `Gitlab` namespace). If
   its error-taxonomy changes affect how commands catch and report failures,
   pick that up here.

</details>
