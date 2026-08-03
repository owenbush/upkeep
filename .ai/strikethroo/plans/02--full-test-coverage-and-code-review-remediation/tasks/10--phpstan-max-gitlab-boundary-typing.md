---
id: 10
group: "standards-remediation"
dependencies: [4, 8]
status: "pending"
created: 2026-08-02
skills:
  - php
  - static-analysis
complexity_score: 6
complexity_notes: "Level-max typing against symfony/http-client and symfony/yaml dynamic returns requires establishing a boundary-narrowing pattern, not just annotation edits."
---
# Reach PHPStan level max across the GitLab and configuration boundary

## Objective

Eliminate every PHPStan level-max error in `src/Gitlab/`, `src/Config/`, and
`src/Notes/` by narrowing external input at the boundary into typed value
objects, rather than scattering assertions through the interior.

## Skills Required

`php` for the type work; `static-analysis` for PHPStan generics, array shapes,
and narrowing idioms.

## Acceptance Criteria

- [ ] `vendor/bin/phpstan analyse --no-progress` reports zero errors for `src/Gitlab/`, `src/Config/`, and `src/Notes/`.
- [ ] No `phpstan-baseline.neon` exists.
- [ ] `grep -rn "phpstan-ignore" src/Gitlab/ src/Config/ src/Notes/` returns either nothing, or only hits with an adjacent comment justifying why the error cannot be fixed at the source.
- [ ] External input — HTTP response bodies and YAML documents — is decoded and validated into typed structures once, at the edge, rather than being passed around as `mixed` or untyped arrays.
- [ ] `vendor/bin/phpunit` passes.
- [ ] `vendor/bin/phpcs` still reports zero violations — the type work did not reintroduce style errors.
- [ ] The error count for these namespaces has gone from the task 1 baseline to zero.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- `symfony/http-client` response methods (`toArray()`, `getContent()`) and
  `symfony/yaml` `Yaml::parse()` return loosely typed values that level max
  flags extensively.
- Collections need generic annotations (`list<T>`, `array<string, T>`) for
  level max to pass.
- The `Gitlab` models (`MergeRequest`, `MergeRequestList`, `Pipeline`,
  `Project`, `Tag`) are the natural typed targets for decoded API responses.

## Input Dependencies

- Task 4: a PSR-12-clean codebase, so these diffs are not tangled with
  formatting churn.
- Task 8: the settled `Gitlab` error taxonomy and class structure — typing code
  that is about to be restructured would be wasted work.

## Output Artifacts

- A level-max-clean `Gitlab`, `Config`, and `Notes`.
- A boundary-narrowing pattern that task 11 follows for the remaining
  namespaces.
- Any justified `@phpstan-ignore` annotations, which task 17 audits.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Run `composer analyse` and filter to these three namespaces to get the
   working list.

2. The governing strategy, from the plan: **narrow at the boundary**. Decode
   and validate external input into typed value objects once, where it enters
   the system, so the interior is statically clean without assertions
   scattered through it. Concretely, `GitlabClient` should turn a response into
   a `MergeRequest` (or the relevant model) at the point of decoding, validating
   shape as it goes — after which every downstream consumer works with a typed
   object and needs no narrowing at all.

3. The same applies to YAML: `registry.yml` parsing should produce typed
   `Cockpit\Module` objects at the edge rather than arrays travelling inward.
   (`src/Cockpit/` itself belongs to task 11 — coordinate if the boundary
   crosses.)

4. Expect to add generic annotations to collections. `MergeRequestList` in
   particular will need `list<MergeRequest>` or similar for level max to be
   satisfied.

5. Fix at the source. `@phpstan-ignore` is a last resort and requires an
   adjacent justifying comment — the plan explicitly rejected a baseline for
   the same reason, and task 17 audits every suppression.

6. Re-run `composer lint` afterwards. Type annotations and restructured
   constructors can reintroduce PSR-12 violations, and task 4's clean state
   must hold.

7. Task 11 runs in parallel on the other namespaces. Whatever narrowing pattern
   you establish here should be the one it follows, so the codebase ends up
   with one approach rather than two. Note the pattern clearly in your output.

</details>
