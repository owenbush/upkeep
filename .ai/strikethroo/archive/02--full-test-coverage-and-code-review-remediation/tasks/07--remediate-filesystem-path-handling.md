---
id: 7
group: "review-and-remediation"
dependencies: [5]
status: "completed"
created: 2026-08-02
skills:
  - php
  - security
complexity_score: 5
---
# Remediate filesystem path and result-file handling

## Objective

Resolve the filesystem findings: path traversal and symlink handling in cockpit
and projects-root resolution, enforcement of the `$HOME` containment
requirement, and the permissions and write-atomicity of generated files.

## Skills Required

`php` for filesystem and path handling; `security` for traversal, symlink, and
file-permission reasoning.

## Acceptance Criteria

- [ ] Every filesystem finding assigned to this task in `review-findings.md` is resolved, and the record is updated with each resolution.
- [ ] Cockpit resolution (`--cockpit` > `UPKEEP_COCKPIT` > cwd) and projects-root resolution (`--projects-root` > `UPKEEP_PROJECTS_ROOT` > `~/.upkeep/projects`) reject or safely normalise traversal sequences, and this is covered by tests.
- [ ] The projects root is verified to stay under `$HOME`, with a clear error when it does not, covered by a test.
- [ ] Files written under `<cockpit>/results/` and the base-artifact trees are created with appropriate permissions, and partial writes cannot leave a corrupt file readable as valid — covered by a test.
- [ ] `vendor/bin/phpunit` passes.
- [ ] `grep -r "ddev" src/ --exclude-dir=Adapter` returns nothing.
- [ ] The suite still runs with no network and no docker; all filesystem tests use temporary directories cleaned up afterwards.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- Affected namespaces: `src/Cockpit/`, `src/Results/`, `src/BaseArtifact/`, and
  the path-resolution helpers in `src/Adapter/` (`ProjectsRoot`,
  `SnapshotLayout`).
- The `$HOME` containment rule exists for a concrete reason: Docker providers
  on macOS only mount the home directory. It is a functional requirement, not
  only a security one.
- No backwards-compatibility constraint applies — on-disk formats and layouts
  may change, but see the note below about the maintainer's working cockpit.

## Input Dependencies

- Task 5: the findings record, specifically the filesystem findings.

## Output Artifacts

- Hardened path resolution and file writing.
- Tests covering traversal rejection, `$HOME` containment, and write
  behaviour, which task 16 builds on for coverage.
- Updated `review-findings.md` with resolutions recorded.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Read `review-findings.md` and work only the findings assigned to this task.

2. For traversal, prefer canonicalising with `realpath()` and then verifying
   the result is inside the expected root, over pattern-matching for `..`.
   Pattern-matching misses symlink-based escapes; canonicalisation does not.
   Note that `realpath()` returns `false` for paths that do not yet exist, so
   directory-creation paths need the parent canonicalised instead.

3. The `$HOME` containment check should produce a clear, actionable error that
   explains *why* the constraint exists (the macOS Docker mount limitation),
   not just that it was violated. That reason is documented in `CLAUDE.md`.

4. For write atomicity, the standard approach is to write to a temporary file
   in the same directory and `rename()` into place — `rename()` is atomic
   within a filesystem, so a reader never observes a partial file. Apply this
   to the cached results under `<cockpit>/results/` in particular, since a
   truncated JSON or YAML file there would be read back as corrupt.

5. Use `sys_get_temp_dir()`-based fixtures for the tests and clean up in
   `tearDown()`. Do not touch the real `~/.upkeep/` or any real cockpit.

6. **If you change an on-disk format or layout**, surface it explicitly in the
   task output along with the manual rebuild it implies for an existing
   cockpit. The plan accepts this cost but requires it to be a stated decision
   rather than a silent surprise.

</details>
