---
id: 2
group: "verification"
dependencies: []
status: "completed"
created: 2026-07-29
skills:
  - gitlab-api
complexity_score: 3
---
# Verify git.drupalcode.org API access and bot-MR pattern

## Objective
Empirically determine which GitLab REST endpoints on git.drupalcode.org respond to the maintainer's existing Personal Access Token — MR listing, pipeline status, and specifically the merge endpoint — and pin down the exact author/branch pattern of the automated compatibility MRs. These answers gate the GitLab client and the fast-lane design.

## Skills Required
`gitlab-api` — REST calls against a GitLab instance with `PRIVATE-TOKEN` auth, interpreting 403s on a block-by-default instance.

## Acceptance Criteria
- [ ] `curl -s -H "PRIVATE-TOKEN: $TOKEN" "https://git.drupalcode.org/api/v4/projects/project%2F<module>/merge_requests?state=opened&scope=all"` against a real maintained module returns HTTP 200 with a JSON array (or the failure code is recorded).
- [ ] The pipelines endpoint (`.../merge_requests/<iid>/pipelines` or equivalent) is tested the same way and its status code recorded.
- [ ] The merge endpoint's accessibility is determined WITHOUT merging anything unintended: either via a dry probe on a sacrificial/test MR the maintainer explicitly designates, or — if no safe target exists — recorded as "untested pending a safe target" with the fallback plan noted. A 403/405 vs 2xx outcome (or the untested status) is recorded explicitly.
- [ ] At least three real automated compatibility MRs are examined and the recorded pattern includes: exact author username(s), source-branch naming pattern, and title pattern.
- [ ] `docs/contrib-maintainer-design.md` section 13 is updated with the endpoint matrix (endpoint → status code → open/closed) and the bot-MR pattern.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- The maintainer's existing Drupal.org Git-access PAT (from Drupal.org account → "Git access"), supplied by the user at execution time — never committed or logged.
- GitLab REST API v4 on `https://git.drupalcode.org`, `PRIVATE-TOKEN` header auth.
- At least one real contrib module the user maintains, named by the user.

## Input Dependencies
None — first-wave verification task. Requires the user to supply a PAT and name at least one maintained module before the calls can run; ask if not provided.

## Output Artifacts
- Endpoint access matrix and bot-MR identification pattern recorded in `docs/contrib-maintainer-design.md`.
- Consumed by task 9 (GitLab client), task 12 (gate classification), and task 14 (fast-lane merge vs degraded browser-link path).

## Implementation Notes
The DA policy allows interactive individual actions with a PAT. Every probe in this task is a single human-triggered read call except the merge probe — treat the merge endpoint with care: do NOT merge a real MR as a side effect of verification. If the user cannot designate a safe merge target, record the merge endpoint as unverified and let task 14 build the degraded path as default until proven otherwise.

<details>
<summary>Detailed steps</summary>

1. Ask the user for: the PAT (via environment variable, e.g. `export DRUPAL_PAT=...`, never echoed), and 1–2 module machine names they maintain.
2. Resolve the project ID: `curl -s -H "PRIVATE-TOKEN: $DRUPAL_PAT" "https://git.drupalcode.org/api/v4/projects/project%2F<module>"` — record status and the numeric `id`.
3. Probe read endpoints, recording HTTP status for each: project lookup, `merge_requests?state=opened&scope=all`, single MR by IID, MR pipelines/head pipeline, project tags (needed later by release notes).
4. For the merge endpoint (`PUT .../merge_requests/<iid>/merge`): only exercise against an MR the user explicitly designates as safe to merge (a real ready compat MR they want merged anyway is ideal — the action is then genuinely useful). Otherwise record as unverified.
5. List recent MRs authored by the project update bot across the user's modules; record author username, branch pattern (e.g. `project-update-bot-only` branches), and title convention from at least three samples.
6. Update `docs/contrib-maintainer-design.md` section 13's GitLab bullet and bot-MR bullet with the findings.
</details>
