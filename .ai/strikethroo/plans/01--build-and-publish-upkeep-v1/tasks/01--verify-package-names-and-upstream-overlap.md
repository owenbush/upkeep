---
id: 1
group: "verification"
dependencies: []
status: "in-progress"
created: 2026-07-29
skills:
  - web-research
complexity_score: 2
---
# Verify package names and upstream issue overlap

## Objective
Confirm the chosen public names are actually free at the registry level, and confirm no open `ddev-drupal-contrib` enhancement issue overlaps the planned fixture work — recording verified answers in the design doc.

## Skills Required
`web-research` — registry lookups and issue-queue reading; no code is written.

## Acceptance Criteria
- [ ] `curl -s -o /dev/null -w "%{http_code}" https://packagist.org/packages/owenbush/upkeep.json` returns `404` (name free) — or, if taken, the collision is reported to the user and no further naming assumption is made.
- [ ] A Packagist search for any existing package installing an `upkeep` vendor binary is performed and the result recorded.
- [ ] A GitHub search for existing `ddev-upkeep` repos and a check of the ddev add-on registry (`ddev add-on list --all` or the ddev.com add-on listing) confirm no collision for `ddev-upkeep`, with the result recorded.
- [ ] The open `ddev-drupal-contrib` issues #164, #163, #157, #172, #170 have each been read in full, and a one-paragraph overlap verdict per issue is recorded.
- [ ] `docs/contrib-maintainer-design.md` section 13 is updated in place with the verified answers for the naming and upstream-overlap questions.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Packagist HTTP API (`https://packagist.org/packages/{vendor}/{name}.json`, search API).
- GitHub search / `gh` CLI for repo and issue lookups (`gh issue view <n> --repo ddev/ddev-drupal-contrib`).
- No authentication beyond anonymous/public access is required.

## Input Dependencies
None — first-wave verification task. The design doc `docs/contrib-maintainer-design.md` (sections 11, 13) defines what to check.

## Output Artifacts
- Updated `docs/contrib-maintainer-design.md` with verified naming and overlap answers.
- A clear go/no-go on the names `owenbush/upkeep`, binary `upkeep`, and `owenbush/ddev-upkeep`, consumed by task 19 (publication) and implicitly by all scaffolding tasks.

## Implementation Notes
If any name is taken, STOP and surface the collision to the user for a naming decision — do not pick a substitute name yourself.

<details>
<summary>Detailed steps</summary>

1. Check `https://packagist.org/packages/owenbush/upkeep.json` — expect HTTP 404. Also query `https://packagist.org/search.json?q=upkeep` and scan results for any package whose `bin` is `upkeep` (spot-check the top hits' composer.json `bin` entries).
2. Search GitHub for `ddev-upkeep` in repo names (`gh search repos ddev-upkeep`). Check the official ddev add-on registry listing for anything named `upkeep`.
3. For each of the five `ddev-drupal-contrib` issues (#164, #163, #157, #172, #170), read the full issue text and comments (`gh issue view N --repo ddev/ddev-drupal-contrib --comments`). Record: issue title, one-paragraph summary, and verdict — "no overlap with fixture/MR-orchestration work" or a description of the overlap. If any issue overlaps the fixture design, surface it to the user before task 5 proceeds.
4. Edit `docs/contrib-maintainer-design.md` section 13: mark the naming bullet and the "Fixtures: upstream vs. companion" bullet as resolved, with the findings inline.
</details>
