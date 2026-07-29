---
id: 15
group: "orchestrator-commands"
dependencies: [9]
status: "pending"
created: 2026-07-29
skills:
  - php
  - gitlab-api
complexity_score: 3
---
# Implement the release notes drafting command

## Objective
Implement `upkeep notes <module>`: draft release notes covering what merged since the module's last tag — removing changelog-assembly busywork while keeping tagging and release cutting strictly manual (the tool never tags).

## Skills Required
`php` for the notes assembly; `gitlab-api` for tags and merged-MR queries.

## Acceptance Criteria
- [ ] `upkeep notes <module>` for a real module with merges since its last tag prints: the last tag name and date, then a grouped list of merged MRs (title, MR number, author) since that tag — cross-checked against the module's GitLab UI merge history for the same range (no missing, no extra).
- [ ] Output is paste-ready Markdown for a Drupal.org release node (MR links included).
- [ ] A module with no merges since its last tag reports exactly that and exits 0; a module with no tags at all reports the full merged history with a clear "no previous tag" note.
- [ ] The command performs zero write operations against GitLab (read-only — verify no non-GET calls in the client usage; the command has no tag/release flags).
- [ ] Unit tests cover the grouping/formatting logic and both edge cases (no merges, no tags) with mocked client data; `vendor/bin/phpunit` passes.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Task 9's client: `tags(project)` (latest by date), `mergedSince(project, tagDate)`.
- Grouping: bot compatibility MRs grouped under one heading, everything else listed individually (the homogeneous class is the noise being compressed); reuse the bot pattern config from task 12 for author matching only — no gate logic here.
- Plain Markdown to stdout; no file writing.

## Input Dependencies
- Task 9's client and typed models. (Bot-pattern config arrives with task 12; if this task executes first, read the pattern from the same config location with the verified defaults.)

## Output Artifacts
- The `notes` command; consumed by task 18 (docs) and task 19's validation.

## Implementation Notes
Resist scope growth: no changelog file management, no template system, no semver suggestion — the deliverable is a pasteable draft, per the plan's "the tool drafts notes; the human tags."

<details>
<summary>Detailed steps</summary>

1. Resolve module → project; fetch tags; select latest by commit/creation date.
2. Fetch MRs merged after that date (client handles pagination); filter to target branch(es) the module releases from (registry-known branches).
3. Format: `## <module> — since <tag> (<date>)`, a `### Compatibility updates` group for bot-authored MRs, `### Changes` for the rest; each entry `- <title> (!<iid> by <author>)` with URL.
4. Edge cases per acceptance; unit tests with mocked data.
</details>
