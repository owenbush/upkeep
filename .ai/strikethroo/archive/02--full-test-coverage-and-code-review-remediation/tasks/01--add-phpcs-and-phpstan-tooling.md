---
id: 1
group: "quality-tooling"
dependencies: []
status: "completed"
created: 2026-08-02
skills:
  - php
  - static-analysis
complexity_score: 3
---
# Add PHP_CodeSniffer and PHPStan in reporting mode

## Objective

Install and configure PHP_CodeSniffer against PSR-12 and PHPStan at level max,
both in reporting (non-blocking) mode, and expose them as Composer scripts so
local and CI invocations cannot drift apart.

## Skills Required

`php` for Composer dependency management and configuration file authoring;
`static-analysis` for correct PHPStan configuration at level max.

## Acceptance Criteria

- [ ] `squizlabs/php_codesniffer` and `phpstan/phpstan` are present in `require-dev` in `composer.json`.
- [ ] A `phpcs.xml.dist` exists configuring the PSR-12 standard over `src/`, `bin/`, and `tests/`.
- [ ] A `phpstan.neon.dist` exists configuring `level: max` over `src/` and `tests/`.
- [ ] No `phpstan-baseline.neon` file is created — `ls phpstan-baseline.neon` returns "No such file or directory".
- [ ] `composer lint` runs PHP_CodeSniffer and `composer analyse` runs PHPStan; both execute and produce a report.
- [ ] Running `vendor/bin/phpcs` and `vendor/bin/phpstan analyse --no-progress` completes and prints a violation/error count. A non-zero count is the expected outcome at this stage and does not fail this task.
- [ ] `vendor/bin/phpunit` still passes, confirming no regression from the dependency additions.
- [ ] Record the initial PSR-12 violation count and PHPStan error count in the task output; downstream remediation tasks use these as their starting baseline.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- PHP >= 8.2, Composer.
- `squizlabs/php_codesniffer` ^3.x, `phpstan/phpstan` ^2.x (or current stable).
- PSR-12 is the standard; `bin/upkeep` has no `.php` extension so the sniffer
  config must include it explicitly.
- PHPStan must analyse `tests/` as well as `src/`.

## Input Dependencies

None. This is a zero-dependency foundation task.

## Output Artifacts

- `phpcs.xml.dist`
- `phpstan.neon.dist`
- Updated `composer.json` (`require-dev` plus `scripts`)
- Baseline violation and error counts, reported for downstream tasks.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Add the dev dependencies:
   `composer require --dev squizlabs/php_codesniffer phpstan/phpstan`

2. Create `phpcs.xml.dist` at the repository root. It must define a ruleset
   referencing the `PSR12` standard, include the paths `src`, `bin`, and
   `tests`, and — because `bin/upkeep` is an extensionless PHP file — set the
   sniffer to also treat files without an extension in `bin/` as PHP. The
   standard way is an `<arg name="extensions" value="php"/>` plus an explicit
   `<file>bin/upkeep</file>` entry.

3. Create `phpstan.neon.dist` at the repository root with `parameters:`
   containing `level: max` and `paths:` listing `src` and `tests`. Do NOT
   generate a baseline — the plan explicitly rejected one. If PHPStan suggests
   creating a baseline, ignore the suggestion.

4. Add to the `scripts` section of `composer.json`:
   - `lint` → `phpcs`
   - `lint:fix` → `phpcbf`
   - `analyse` → `phpstan analyse --no-progress`

5. This task is **reporting mode only**. Do not fix any violation or error
   here, and do not wire anything into CI (task 3 does that) or make anything
   blocking (task 18 does that). Installing a hard gate now would block every
   intermediate commit of the remediation work that follows.

6. Report the two baseline numbers clearly. Tasks 4, 10, and 11 consume them
   to know when they are done.

</details>
