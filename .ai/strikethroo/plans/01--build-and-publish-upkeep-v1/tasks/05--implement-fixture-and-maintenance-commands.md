---
id: 5
group: "ddev-addon"
dependencies: [4]
status: "completed"
created: 2026-07-29
skills:
  - ddev-addon
  - bash
complexity_score: 5
complexity_notes: "The add-on's dominant deliverable: three fixture commands plus project-local maintenance, kept as one task because they share the fixture model and storage layout."
---
# Implement fixture and project-local maintenance commands

## Objective
Implement the add-on's real commands per the plan's fixture model: `ddev fixture-create <name>` (DB → portable gzipped SQL dump, sanitized by default when destined for a module repo, with size warning), `ddev fixture-load <name>` (dump → materialized snapshot on first use → fast snapshot restore thereafter), `ddev fixture-list`, plus a project-local command to drop the project's disposable materialized snapshots.

## Skills Required
`ddev-addon` for host/web command conventions and install.yaml wiring; `bash` for the command implementations.

## Acceptance Criteria
- [ ] In a `ddev-drupal-contrib` project with an installed site: `ddev fixture-create smoke` produces a gzipped SQL dump at the conventional path and prints where; running it with a module-repo destination runs `drush sql:sanitize` first (observable in output) unless `--no-sanitize` is passed.
- [ ] `ddev fixture-create` warns on stdout when the dump exceeds a size threshold, and the threshold is documented in the command help.
- [ ] After mutating the DB, `ddev fixture-load smoke` restores the pre-mutation state (verify: `ddev drush sql:query "SELECT ..."` shows the original value); a second `fixture-load smoke` is observably faster because it restores the materialized snapshot instead of re-importing the dump (both paths print which mechanism ran).
- [ ] Resolution order is per-module-first: a fixture in the module's `tests/fixtures/` shadows a same-named fixture in the shared library location; `ddev fixture-list` shows both scopes with their origin labeled.
- [ ] The maintenance command deletes only materialized snapshots for the current project (never dumps), and prints what it removed; committed/`tests/fixtures/` dumps are untouched (verify by listing the directory before/after).
- [ ] All commands exit non-zero with a clear message on missing fixture name or absent site.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- ddev custom commands (repo `commands/` layout installed via `install.yaml`).
- DB export/import via the project's DB container (`ddev export-db` / `ddev import-db` or direct `mysqldump`/`mysql` in-container), snapshots via `ddev snapshot` where suitable.
- `drush sql:sanitize` for sanitization; must degrade with a clear error if drush is absent.
- Shared-library fixture location: a well-known path the cockpit will own (make it configurable via an env var with a sane default, e.g. `UPKEEP_FIXTURE_LIBRARY`); per-module path is `tests/fixtures/` in the module repo.

## Input Dependencies
- Task 4's scaffolded repo (commands are added to it).
- Task 3's snapshot/reclamation findings (soft — informs which snapshot mechanism to use).

## Output Artifacts
- Working fixture + maintenance commands in `owenbush/ddev-upkeep`, consumed by task 6 (tests), task 11 (adapter `load_fixture`), and task 18 (docs).
- The concrete on-disk fixture layout convention (paths, naming) that the docs task will document.

## Implementation Notes
Dumps are the portable source of truth; snapshots are disposable local caches tied to the DB engine — never treat a snapshot as authoritative, and always rebuild it from the dump when the engine/version changes (store the engine identity next to the materialized snapshot and compare on load). Keep the commands plain bash following the template's conventions; no new languages or frameworks in the add-on.

<details>
<summary>Detailed steps</summary>

1. `fixture-create <name> [--dest=module|library] [--no-sanitize]`: resolve destination path (module `tests/fixtures/` when inside a module checkout and `--dest=module` or default-detected; else shared library). If destination is a module repo: run `ddev drush sql:sanitize -y` unless `--no-sanitize`. Export DB, gzip to `<name>.sql.gz`. Warn if > ~5 MB (pick and document the threshold).
2. `fixture-load <name>`: resolve per-module-first then library. If a materialized snapshot for `<name>` matching the current DB engine exists, restore it (`ddev snapshot restore` or import of a cached fast-format artifact) and print "restored from snapshot"; else import the dump, then materialize the snapshot for next time and print "imported dump, snapshot materialized".
3. `fixture-list`: enumerate both scopes, print name, scope (module/library), dump size, and whether a materialized snapshot exists.
4. Maintenance command (e.g. `fixture-prune`): delete this project's materialized snapshots only; print each deletion; never touch `.sql.gz` dumps.
5. Wire all commands into `install.yaml` so `ddev add-on get` installs them; re-run the local-path install in a scratch project to confirm they appear in `ddev` help output.
</details>
