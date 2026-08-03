---
id: 4
group: "standards-remediation"
dependencies: [1]
status: "pending"
created: 2026-08-02
skills:
  - php
  - coding-standards
complexity_score: 4
---
# Remediate all PSR-12 violations

## Objective

Bring `src/`, `bin/`, and `tests/` to zero PHP_CodeSniffer violations against
PSR-12, fixing at the source rather than suppressing.

## Skills Required

`php` for the code edits; `coding-standards` for correct PSR-12 interpretation
and safe use of `phpcbf` auto-fixing.

## Acceptance Criteria

- [ ] `vendor/bin/phpcs` exits 0 with zero errors and zero warnings.
- [ ] `vendor/bin/phpunit` still passes — no behavioural change was introduced by the style fixes.
- [ ] `grep -rn "phpcs:ignore\|phpcs:disable" src/ bin/ tests/` returns either nothing, or only hits that carry an adjacent comment justifying why the violation cannot be fixed at the source.
- [ ] `grep -r "ddev" src/ --exclude-dir=Adapter` returns nothing — the adapter boundary is intact.
- [ ] The violation count has gone from the task 1 baseline to zero.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- `vendor/bin/phpcbf` auto-fixes the majority of PSR-12 violations
  (whitespace, brace placement, import ordering); the residue is manual.
- `bin/upkeep` is an extensionless PHP file and is in scope.
- Files use `declare(strict_types=1);` and `final readonly class` idioms —
  preserve them.

## Input Dependencies

- Task 1: `phpcs.xml.dist`, the `lint` Composer script, and the baseline
  violation count.

## Output Artifacts

- A PSR-12-clean codebase across `src/`, `bin/`, and `tests/`.
- Any justified suppression annotations, which task 17 will audit.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Run `composer lint` to see the current violations.

2. Run `vendor/bin/phpcbf` to auto-fix what can be auto-fixed. Review the diff
   before committing — `phpcbf` is generally safe but can reformat in ways that
   hurt readability in edge cases.

3. Fix the remaining violations by hand. Re-run `composer lint` until it
   reports zero.

4. Suppression is a last resort. If a violation genuinely cannot be fixed at
   the source, add `phpcs:ignore` **with an adjacent comment explaining why**.
   The plan treats unjustified suppressions as findings in their own right, and
   task 17 audits every one of them.

5. After the fixes, run `vendor/bin/phpunit` to confirm nothing broke. Style
   fixes should be behaviour-preserving; if a test fails, the change was not
   purely stylistic and needs investigating.

6. Run the adapter-boundary guard
   (`grep -r "ddev" src/ --exclude-dir=Adapter`) and confirm it is silent. It
   should be unaffected by style work, but it is a hard invariant and cheap to
   check.

7. Do this before the PHPStan work in tasks 10 and 11 — both touch the same
   files, and getting the mechanical formatting churn out of the way first
   keeps those diffs readable.

</details>
