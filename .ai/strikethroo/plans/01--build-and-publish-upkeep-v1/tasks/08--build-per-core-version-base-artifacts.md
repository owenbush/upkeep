---
id: 8
group: "orchestrator-core"
dependencies: [3, 7]
status: "in-progress"
created: 2026-07-29
skills:
  - composer
  - drush
complexity_score: 4
---
# Implement per-core-version base artifact building

## Objective
Implement the cockpit's base-artifact layer: an `upkeep` command that builds, per core version, the two canonical artifacts — a resolved `drupal/recommended-project` base vendor tree and a clean-install, module-free DB snapshot (stored as a portable dump) — stored under the cockpit's `base-artifacts/` and never auto-pruned.

## Skills Required
`composer` for the base tree resolve; `drush` for the clean site install and dump.

## Acceptance Criteria
- [ ] `upkeep base-artifacts build --version=11` (naming per the scaffold's conventions) produces under the cockpit: a resolved base tree for D11 (vendor/ present, `composer validate` passes inside it) and a gzipped clean-install SQL dump; re-running for `--version=12` produces the second pair.
- [ ] The build uses the seeding mechanism task 3 verified (tree copy if verified clean, else full resolve riding the shared Composer cache) — the choice is stated in code comments referencing the verification outcome.
- [ ] `upkeep base-artifacts status` lists which core versions have artifacts, their build dates, and sizes.
- [ ] Artifacts are marked canonical: the storage layout places them where task 16's prune explicitly excludes them (a `canonical` marker or dedicated directory).
- [ ] A stale rebuild path exists: `build --version=N --force` rebuilds over an existing artifact set deliberately.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Composer programmatic invocation via `Symfony\Component\Process` (shell out to `composer`), never a bundled Composer.
- Clean install via ddev (a temporary throwaway project) or via drush against a temporary DB — use ddev so the DB engine matches what per-module projects will run; export with the same dump format the fixture model uses (gzipped SQL).
- Artifact layout: `<cockpit>/base-artifacts/<core-version>/{tree/, clean-install.sql.gz, meta.yml}`.

## Input Dependencies
- Task 3's seeding and cache verdicts (mechanism choice).
- Task 7's cockpit services and command skeleton.

## Output Artifacts
- Base artifacts on disk plus the build/status commands; consumed by task 10 (`ensure_env` seeds from them) and protected by task 16 (prune exclusion).

## Implementation Notes
The base tree must remain module-free — it is identical across all modules until a module is required on top. Store `meta.yml` per version (exact core version resolved, PHP version, DB engine identity, build timestamp) so `ensure_env` and snapshot-materialization can detect skew.

<details>
<summary>Detailed steps</summary>

1. `BaseArtifactBuilder` service: given core major (e.g. 11), run `composer create-project drupal/recommended-project` pinned to that major into `<cockpit>/base-artifacts/11/tree/` (or copy from a pristine cached resolve if task 3 verified copying clean).
2. Spin a throwaway ddev project against that tree (matching the engine's PHP/DB defaults for that core), `drush site:install minimal -y` (module-free), export DB → `clean-install.sql.gz`, tear the throwaway down using the reclamation commands task 3 verified.
3. Write `meta.yml`. Implement `status` by reading the artifact directories + meta files.
4. Unit-test only the pure logic (layout resolution, meta parsing, staleness compare) — the build itself is exercised by running the command for real once per version, which is this task's own verification.
</details>
