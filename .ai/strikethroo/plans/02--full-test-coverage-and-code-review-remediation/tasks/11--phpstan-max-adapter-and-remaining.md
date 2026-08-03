---
id: 11
group: "standards-remediation"
dependencies: [4, 9]
status: "pending"
created: 2026-08-02
skills:
  - php
  - static-analysis
complexity_score: 6
complexity_notes: "Largest namespace surface (Adapter's 22 classes plus nine further namespaces) with process-layer nullable returns; mechanical once the boundary pattern from task 10 is available."
---
# Reach PHPStan level max across the adapter and remaining namespaces

## Objective

Eliminate every remaining PHPStan level-max error across `src/Adapter/`,
`src/Command/`, `src/Workflow/`, `src/Cockpit/`, `src/BaseArtifact/`,
`src/Dashboard/`, `src/Drupal/`, `src/Gate/`, `src/Maintenance/`,
`src/Results/`, and `tests/`.

## Skills Required

`php` for the type work; `static-analysis` for PHPStan generics, array shapes,
and null-handling idioms.

## Acceptance Criteria

- [ ] `vendor/bin/phpstan analyse --no-progress` reports zero errors across the entire configured scope, including `tests/`.
- [ ] No `phpstan-baseline.neon` exists.
- [ ] `grep -rn "phpstan-ignore" src/ tests/` returns either nothing, or only hits with an adjacent comment justifying why the error cannot be fixed at the source.
- [ ] Null-handling around `Process::getExitCode()` is resolved by narrowing rather than by suppression.
- [ ] `grep -r "ddev" src/ --exclude-dir=Adapter` returns nothing.
- [ ] `vendor/bin/phpunit` passes.
- [ ] `vendor/bin/phpcs` still reports zero violations.
- [ ] The overall PHPStan error count has gone from the task 1 baseline to zero.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- `src/Adapter/` is the largest namespace at 22 classes and wraps
  `symfony/process`. `Process::getExitCode()` returns `?int`, which the
  codebase currently coalesces with `?? -1` in several places.
- Enums (`CheckStatus`, `CheckType`, `PipelineStatus`, `GateStatus`,
  `Category`, `PruneScope`) are already strongly typed and should need little
  work.
- `tests/` is in the analysis scope and must also reach zero errors; test
  doubles and fakes commonly need generic annotations.

## Input Dependencies

- Task 4: a PSR-12-clean codebase.
- Task 9: the settled command layer and adapter boundary.
- Task 10 (soft): the boundary-narrowing pattern. Follow it if available so the
  codebase ends with one approach rather than two.

## Output Artifacts

- A level-max-clean codebase across all remaining namespaces.
- Any justified `@phpstan-ignore` annotations, which task 17 audits.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Run `composer analyse` for the working list. This is the largest of the two
   PHPStan tasks by file count; work namespace by namespace rather than trying
   to hold the whole set at once.

2. Follow the boundary-narrowing pattern task 10 establishes for the `Gitlab`
   side. Consistency between the two matters more than local elegance in
   either.

3. The `Process` null-handling is the characteristic issue here. The current
   `$process->getExitCode() ?? -1` pattern silences the nullable but encodes
   "unknown" as a magic number. Prefer narrowing at the point where the
   exit code is actually known to exist, or model the unknown case explicitly
   in `CapturedProcess`. Either is better than propagating `-1` as if it were a
   real exit status.

4. The adapter boundary is a hard invariant and type work can breach it subtly —
   for instance by moving a container-name type outward to satisfy a signature.
   Run `grep -r "ddev" src/ --exclude-dir=Adapter` after each namespace, not
   only at the end.

5. `tests/` is in scope. Fakes of `EngineAdapterInterface` and mocked HTTP
   commonly need generic annotations to satisfy level max. Do not exclude
   `tests/` from analysis to make this easier.

6. Fix at the source. `@phpstan-ignore` requires an adjacent justifying
   comment; task 17 audits every one.

7. Re-run `composer lint` afterwards to confirm PSR-12 cleanliness held.

</details>
