---
id: 3
group: "verification"
dependencies: []
status: "in-progress"
created: 2026-07-29
skills:
  - ddev
  - composer
complexity_score: 4
complexity_notes: "Three related empirical experiments merged into one task because they share a single ddev/Composer sandbox; splitting them would fragment trivially small units."
---
# Verify ddev reclamation and Composer seeding mechanics

## Objective
Answer three mechanical open questions by experiment: (a) exactly what `ddev delete`, `ddev stop --remove-data`, and plain tree removal each reclaim; (b) whether copying a resolved base vendor tree and incrementally requiring one module behaves identically to a from-scratch resolve; (c) whether the shared global Composer cache behaves correctly when two core versions pull different versions of the same package.

## Skills Required
`ddev` for project lifecycle/volume semantics; `composer` for tree seeding and cache behavior.

## Acceptance Criteria
- [ ] A reclamation table exists recording, for each of `ddev delete`, `ddev stop --remove-data`, and `rm -rf <tree>`: what disk is freed (volumes, images, codebase), what survives, and the observed `docker volume ls` / `du` deltas from an actual run.
- [ ] Seeding experiment: a project seeded by copying a resolved `drupal/recommended-project` tree then running `composer require drupal/<module>` passes `composer validate` and `composer install --dry-run` reports nothing to change; `vendor/composer/installed.json` and autoloader behavior match a from-scratch control build (diff of installed package sets is empty apart from expected paths).
- [ ] Cache experiment: two projects on different core majors (e.g. D11 and D12) resolve successfully back-to-back with the shared `~/.cache/composer`, and the second project's resolve shows cache hits (no re-download of shared packages) with no corruption — `composer install` exits 0 in both.
- [ ] `docs/contrib-maintainer-design.md` section 13 is updated with all three verified answers.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Local Docker + ddev installed and working.
- `drupal/recommended-project` for two supported core versions.
- Disk inspection: `du -sh`, `docker volume ls`, `docker system df`.

## Input Dependencies
None — first-wave verification task.

## Output Artifacts
- Verified answers recorded in `docs/contrib-maintainer-design.md`.
- The reclamation table feeds task 16 (prune targets); the seeding verdict feeds task 8 (base-artifact build) and task 10 (ensure_env); the cache verdict validates the cold-start design.

## Implementation Notes
Run all experiments in throwaway directories under a scratch location, never inside this repo. If the seeding experiment shows divergence (stale autoloader, wrong installed-paths, missing scaffold), record the exact failure mode — task 8 will then use a full-resolve fallback that still rides the shared cache, per the plan's mitigation.

<details>
<summary>Detailed steps</summary>

1. **Reclamation**: create a disposable ddev project with a DB, note `docker system df` and volume list; run each teardown variant on separate identical projects; record what each removed. Include whether `ddev delete` removes the project's DB volume, and whether plain `rm -rf` leaves orphaned volumes/registrations (`ddev list`).
2. **Seeding**: build project A from scratch (`composer create-project drupal/recommended-project`), then `composer require drupal/<some-module>`. Build project B by `cp -a` of a pristine resolved base tree, then the same require. Compare: `composer validate`, `composer install --dry-run`, sorted package lists from `composer show`, presence of scaffold files, and a smoke `php -r "require 'vendor/autoload.php';"`.
3. **Cache**: with the global Composer cache in place, resolve a D11 tree then a D12 tree; verify both succeed and observe cache reuse (Composer's output shows "Loading from cache" / no network fetch for previously-seen versions).
4. Update the three corresponding bullets in `docs/contrib-maintainer-design.md` section 13, then delete the scratch projects (using the reclamation commands just verified).
</details>
