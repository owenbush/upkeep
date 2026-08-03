---
id: 19
group: "enforcement-and-docs"
dependencies: [18]
status: "completed"
created: 2026-08-02
skills:
  - technical-writing
  - markdown
complexity_score: 3
---
# Update project documentation for the new quality gates and any surface changes

## Objective

Bring `CLAUDE.md`, `README.md`, and `docs/contrib-maintainer-design.md` into
line with the new tooling, the enforced coverage floor, and any CLI or on-disk
format changes made during remediation.

## Skills Required

`technical-writing` for accurate, concise documentation; `markdown` for the
existing document conventions.

## Acceptance Criteria

- [ ] `CLAUDE.md`'s "Running tests" section documents the lint, static-analysis, and coverage commands alongside `vendor/bin/phpunit`, and states the enforced 100% coverage floor.
- [ ] `CLAUDE.md`'s "Conventions" section records the rule that every suppression annotation requires a written justification.
- [ ] `CLAUDE.md` still accurately describes the layout, the adapter boundary rule, and the merge policy — updated if remediation changed any of them.
- [ ] `README.md` reflects any command name, option, or exit-code change reported by task 9; if none occurred, this is explicitly confirmed.
- [ ] `docs/contrib-maintainer-design.md` reflects any architectural or on-disk format change from tasks 7, 8, and 9; if none occurred, this is explicitly confirmed.
- [ ] If an on-disk format changed, the manual rebuild required for an existing cockpit is documented.
- [ ] No `AGENTS.md` is created — this project's AI-facing file is `CLAUDE.md`.
- [ ] Every command shown in the documentation is executed and confirmed to work as written.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- `CLAUDE.md` is the AI-facing project file; there is no `AGENTS.md` in this
  repository and none should be added.
- `README.md` is the user-facing documentation and includes the "Policy stance"
  section on one-human-approval-per-MR merges.
- `docs/contrib-maintainer-design.md` covers architecture and rationale.
- `.github/workflows/ci.yml` step names serve as the at-a-glance documentation
  of the gates; confirm they read clearly.

## Input Dependencies

- Task 18: the final enforced state of the gates.
- Task 9: the list of CLI-surface changes.
- Tasks 7 and 8: any on-disk format or architectural changes.

## Output Artifacts

- Updated `CLAUDE.md`, and updated `README.md` and
  `docs/contrib-maintainer-design.md` where remediation required it.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. `CLAUDE.md` is the priority. Its "Running tests" section currently documents
   only `vendor/bin/phpunit` and would be actively wrong once the gates exist —
   a future agent reading it would not know to run lint or analysis. Add the
   Composer scripts and state the enforced floor.

2. Add the suppression-justification rule to "Conventions" so future work
   inherits it rather than rediscovering it. Task 17's audit only helps once;
   the convention is what keeps the budget honest afterwards.

3. Check the rest of `CLAUDE.md` against reality. It documents the layout
   namespace by namespace, the adapter boundary guard, the resolution
   precedence rules, and the merge policy. Remediation may have changed some of
   these — verify each rather than assuming.

4. For `README.md` and `docs/contrib-maintainer-design.md`, updates are
   conditional. If tasks 7, 8, and 9 reported no user-visible changes, say so
   explicitly in the task output rather than silently skipping — the acceptance
   criteria require confirmation either way.

5. If an on-disk format changed, document the manual cockpit rebuild it
   implies. The plan accepts this cost but requires it to be stated.

6. Run every command you document. Documentation that has not been executed is
   how the current "Running tests" section became incomplete in the first
   place.

</details>
