---
id: 19
group: "release"
dependencies: [1, 17, 18]
status: "pending"
created: 2026-07-29
skills:
  - packaging
  - git
complexity_score: 3
---
# Publish both artifacts and verify the installation paths

> **⛔ HOLD (2026-07-29, user directive): Do NOT execute this task — in this or any
> future session — until the user (Owen) has tested the full toolchain locally and
> given explicit, fresh approval to publish. This supersedes the plan's "publish"
> end-state until lifted. Blueprint execution stops after Phase 7 and hands over
> for local testing.**

## Objective
Take both repos public and make the tool really installable: create/publish the public GitHub repo for `owenbush/upkeep`, register it on Packagist, tag initial versions of both repos, flip `owenbush/ddev-upkeep` public, and verify both ecosystem install paths end-to-end from clean state — `composer global require owenbush/upkeep` and `ddev add-on get owenbush/ddev-upkeep`.

## Skills Required
`packaging` for Packagist registration and Composer distribution; `git` for repo publication and tagging.

## Acceptance Criteria
- [ ] Task 1's name verification is re-confirmed immediately before registration (`curl` the Packagist package URL again — still 404) — publication halts and surfaces to the user if anything changed.
- [ ] `owenbush/upkeep` exists as a public GitHub repo with the full history pushed and a tagged initial release; `owenbush/ddev-upkeep` is public with a tagged release produced by the template's release flow, and its CI is green on that tag (`gh run list` for the tag's workflow shows success).
- [ ] `https://packagist.org/packages/owenbush/upkeep.json` returns HTTP 200 listing the tagged version, with the GitHub webhook/auto-update hooked up (push a trivial change and confirm Packagist reflects it, or confirm the webhook ping succeeds).
- [ ] Clean-state orchestrator install: in a container or pristine shell with only PHP+Composer, `composer global require owenbush/upkeep` succeeds and `upkeep --version` + `upkeep list` show the expected command surface.
- [ ] Clean add-on install: in a fresh ddev project, `ddev add-on get owenbush/ddev-upkeep` (public path, not local) succeeds and the fixture commands appear in `ddev` help.
- [ ] Both repos' default branches are protected only as far as the user wants — ask before adding any branch-protection or release automation not already provided by the template.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- `gh` CLI for repo creation/visibility; Packagist account submission (`https://packagist.org/packages/submit`) — requires the user's Packagist credentials/session; ask the user to perform the browser-side submission step if credentials aren't available to the agent.
- Initial version tags: propose `v0.1.0` for both (pre-1.0 signals v1-of-the-tool but early API); confirm tag names with the user before pushing tags.
- Docker (or a scratch shell with isolated `COMPOSER_HOME`) for the clean-install verification.

## Input Dependencies
- Task 1 (names verified), task 17 (green suite), task 18 (docs in place). Transitively: everything.

## Output Artifacts
- The published, installable v1 of both artifacts — the plan's end state. The plan's Self Validation section runs after this task completes.

## Implementation Notes
Publication is outward-facing and effectively irreversible (Packagist package names persist even after deletion) — the re-verification step and the user confirmation on tag names are mandatory, not ceremony. Nothing in this task adds features: if a verification step fails, the fix happens in the owning task's domain, not with a workaround here.

<details>
<summary>Detailed steps</summary>

1. Re-run the Packagist 404 check for `owenbush/upkeep`.
2. Create the public GitHub repo for this project (`gh repo create owenbush/upkeep --public`), push history; flip `ddev-upkeep` to public (`gh repo edit owenbush/ddev-upkeep --visibility public`).
3. Confirm tag names with the user; tag and push both repos; for the add-on, follow the template's release process so its release workflow produces the versioned artifact.
4. Register on Packagist (user performs the browser submission if needed); set up the GitHub hook for auto-updates.
5. Run both clean-state install verifications; capture transcripts.
6. Confirm add-on CI green on the tag.
</details>
