---
id: 4
group: "ddev-addon"
dependencies: []
status: "in-progress"
created: 2026-07-29
skills:
  - ddev-addon
  - git
complexity_score: 2
---
# Scaffold the ddev-upkeep add-on repo from the official template

## Objective
Create the sibling repository for the companion add-on from the official `ddev/ddev-addon-template` GitHub template, renamed and rebranded to `ddev-upkeep`, with the template's install manifest, bats test harness, and release CI intact and passing in their scaffold state.

## Skills Required
`ddev-addon` for template conventions (install.yaml, commands layout, tests); `git` for repository creation.

## Acceptance Criteria
- [ ] A repo exists at `/Users/owen/contrib/ddev-upkeep` (sibling of this repo) generated from `ddev/ddev-addon-template` (via `gh repo create owenbush/ddev-upkeep --template ddev/ddev-addon-template --private --clone` or equivalent), with all template placeholder names (`addon-template`, etc.) replaced by `ddev-upkeep` in `install.yaml`, README title, and test files.
- [ ] `ddev add-on get /Users/owen/contrib/ddev-upkeep` succeeds in a scratch ddev project (local-path install of the scaffold).
- [ ] The template's bats test suite passes locally in its scaffold state: `bats tests` (or the template's documented test invocation) exits 0.
- [ ] The repo stays private until task 19 (publication).

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- `gh` CLI authenticated as `owenbush`; GitHub template instantiation.
- ddev installed locally; `bats` available for the test harness.
- Follow the template's current structure exactly — do not restructure it.

## Input Dependencies
None (task 1's name verification is a soft input; if task 1 found a collision, the user has already decided a new name before this runs).

## Output Artifacts
- The scaffolded `owenbush/ddev-upkeep` repo (local clone at `/Users/owen/contrib/ddev-upkeep`), ready for task 5 to add real commands.

## Implementation Notes
Create the GitHub repo now (private) rather than local-only, so the template's GitHub Actions test workflow is exercised early; task 19 flips it public.

<details>
<summary>Detailed steps</summary>

1. `gh repo create owenbush/ddev-upkeep --template ddev/ddev-addon-template --private --clone` into `/Users/owen/contrib/ddev-upkeep`.
2. Grep for every occurrence of the template's placeholder name and replace with `ddev-upkeep`; update `install.yaml` name field and README heading. Keep the template's license and CI workflows.
3. Create a scratch ddev project (any minimal type), run `ddev add-on get /Users/owen/contrib/ddev-upkeep`, confirm the scaffold's sample command appears; then remove the scratch project.
4. Run the template's test suite per its README (typically `bats tests`); it must pass before this task is done.
5. Commit and push the rename as the initial commit series.
</details>
