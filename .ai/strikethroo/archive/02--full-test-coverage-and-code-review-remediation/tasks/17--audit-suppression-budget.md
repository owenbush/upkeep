---
id: 17
group: "enforcement-and-docs"
dependencies: [13, 14, 15, 16]
status: "completed"
created: 2026-08-02
skills:
  - php
  - code-review
complexity_score: 3
---
# Audit and justify the suppression budget

## Objective

Review every coverage, static-analysis, and style suppression in the codebase
as a single set, confirm each is justified, remove those that are not, and
report the total.

## Skills Required

`php` for assessing whether a suppressed line is genuinely unreachable;
`code-review` for judging justification quality across the whole set.

## Acceptance Criteria

- [ ] `grep -rn "codeCoverageIgnore\|phpstan-ignore\|phpcs:ignore\|phpcs:disable" src/ bin/ tests/` produces a complete list, and every hit has an adjacent comment justifying it.
- [ ] Each `@codeCoverageIgnore` is classified as either a defensive branch against a state the type system already excludes, or a platform path that cannot occur on the CI matrix. Anything fitting neither is removed and the line covered properly, or escalated.
- [ ] Any suppression that turns out to hide a real defect is reported, and the defect recorded in `review-findings.md`.
- [ ] The total suppression count, broken down by type, is reported in the task output.
- [ ] After any removals, `vendor/bin/phpunit --coverage-text` still reports 100% line coverage, `vendor/bin/phpstan analyse` reports zero errors, and `vendor/bin/phpcs` reports zero violations.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- Three suppression mechanisms are in scope: `@codeCoverageIgnore` (and its
  `Start`/`End` variants), `@phpstan-ignore` (and `@phpstan-ignore-next-line`),
  and `phpcs:ignore` / `phpcs:disable`.
- No `phpstan-baseline.neon` may exist — a baseline is a bulk suppression and
  was explicitly rejected during planning.

## Input Dependencies

- Tasks 13, 14, 15, 16: all coverage work complete, so the full suppression set
  exists.
- Tasks 4, 10, 11: the style and static-analysis suppressions.

## Output Artifacts

- A reviewed, justified suppression set.
- The reported total, which is a stated success criterion of the plan.
- Any newly discovered defects added to `review-findings.md`.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. This task exists because a 100% coverage bar can be satisfied dishonestly.
   The plan is explicit: a green build with an unexamined ignore list satisfies
   the letter of the criteria and none of the intent. Review the budget **as a
   whole**, which is precisely what accreting suppressions task by task
   prevents anyone from doing.

2. Produce the full list first, then assess each entry individually. For each
   `@codeCoverageIgnore`, ask: is this genuinely unreachable, or was it merely
   inconvenient to cover? Only two answers are legitimate — a defensive branch
   the type system already excludes, or a platform path impossible on the CI
   matrix. "Hard to set up" is not on that list.

3. Where a suppression is not justified, the fix is to cover the line properly
   or fix the underlying error. If doing so requires a code change, make it —
   no backwards-compatibility constraint applies.

4. Confirm no `phpstan-baseline.neon` has appeared. It is the bulk-suppression
   form of the same problem and was rejected during clarification.

5. If a suppression turns out to be hiding a real defect rather than an
   unreachable line, that is a finding: record it in `review-findings.md` and
   report it, rather than quietly fixing and moving on.

6. Report the total with its breakdown. This number is a stated success
   criterion, and it is the honest counterpart to the 100% figure.

</details>
