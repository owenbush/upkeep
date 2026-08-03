---
id: 5
group: "review-and-remediation"
dependencies: [1]
status: "completed"
created: 2026-08-02
skills:
  - security-review
  - php
complexity_score: 7
complexity_notes: "Wide read surface (99 source files) but read-only and bounded by three named lenses with pre-identified focus areas. Kept as one task because splitting would force two agents to read the whole codebase to produce one findings record."
---
# Conduct the security and best-practice review and produce the findings record

## Objective

Produce an enumerable, written set of findings across the security and
best-practice lenses, so that "remediate all issues" has a verifiable end state.
This task changes no production code — it produces the work list that tasks 6
through 9 consume.

## Skills Required

`security-review` for credential, subprocess, and filesystem threat analysis;
`php` for reading and reasoning about the Symfony Console/Process/HttpClient
codebase.

## Acceptance Criteria

- [ ] A findings record exists at `.ai/strikethroo/plans/02--full-test-coverage-and-code-review-remediation/review-findings.md`.
- [ ] Every finding has: a unique ID, the file and line it concerns, the lens (security or best-practice), a severity, and a proposed resolution.
- [ ] Every finding is assigned to exactly one of tasks 6, 7, 8, or 9, or is explicitly marked out of scope with a stated reason.
- [ ] The credential-handling analysis explicitly traces every path from `Gitlab\TokenResolver::resolve()` to any sink that logs, prints, persists, or embeds the value — including error and exception paths — and states for each whether the token can reach it.
- [ ] The subprocess analysis states whether any caller can place token material into a `Process` argument vector, a child environment variable, or child output that `Adapter\ProcessRunner` then interpolates into an `AdapterException` message via `getCommandLine()` or the combined output.
- [ ] The filesystem analysis covers cockpit and projects-root resolution (including the "must stay under `$HOME`" constraint), traversal and symlink handling, and the permissions and write-atomicity of files under `<cockpit>/results/` and the base-artifact trees.
- [ ] The best-practice analysis covers the `Gitlab` exception hierarchy's consistency, duplication across the 21 command classes, and any breach of the adapter boundary.
- [ ] The PHPStan and PHP_CodeSniffer output from task 1 is reviewed for findings that indicate real defects rather than style, and any such finding is recorded here rather than left to the mechanical remediation tasks.
- [ ] No production code under `src/` or `bin/` is modified by this task — `git diff --stat src/ bin/` shows no changes.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- 99 files under `src/` across 13 namespaces, plus `bin/upkeep` and `tests/`.
- Pre-identified focus areas, from the plan:
  - `src/Gitlab/TokenResolver.php` — reads a PAT from `UPKEEP_GITLAB_TOKEN` or
    `~/.config/upkeep/drupal-pat`; documented as never printing or persisting
    it; performs no permission check on the credential file.
  - `src/Adapter/ProcessRunner.php` — builds `Process` from list-form argument
    vectors (so no shell interpolation by construction), but its `run()`
    failure path interpolates `getCommandLine()` and the full combined child
    output into the exception message, and its log closure streams every child
    output line.
  - Cockpit resolution precedence (`--cockpit` > `UPKEEP_COCKPIT` > cwd) and
    projects-root precedence (`--projects-root` > `UPKEEP_PROJECTS_ROOT` >
    `~/.upkeep/projects`).
- The adapter boundary is a hard invariant:
  `grep -r "ddev" src/ --exclude-dir=Adapter` must return nothing.

## Input Dependencies

- Task 1: PHPStan and PHP_CodeSniffer reports, which surface candidate findings
  and the baseline counts.

## Output Artifacts

- `review-findings.md` — the enumerated findings record with severities,
  proposed resolutions, and task assignments. Tasks 6, 7, 8, 9, and 17 all
  consume it.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. This is a **read-only analysis task**. Resist fixing anything as you find
   it — the fixes are tasks 6 through 9, and mixing analysis with remediation
   makes the findings record incomplete.

2. Work the three lenses in order.

   **Security — credential handling.** Start at
   `Gitlab\TokenResolver::resolve()` and follow the returned string. Every
   consumer is a potential sink. For each, ask: does it log, print, persist,
   put the value in an exception message, or pass it to a subprocess? Check
   error paths specifically — they are where redaction is normally forgotten.
   Also assess whether the tool should warn or refuse when
   `~/.config/upkeep/drupal-pat` is group- or world-readable, and record the
   decision either way rather than leaving it implicit.

   **Security — subprocess.** `ProcessRunner` uses list-form argument vectors,
   which means no shell interpolation and therefore no classic command
   injection. That is a good property; confirm it holds at every construction
   site (`new Process(...)`) and that nothing builds a command string. Then
   examine the failure path: `run()` throws an `AdapterException` containing
   `$process->getCommandLine()` and the trimmed combined stderr+stdout. If any
   caller can put a token into the argv or the child can echo it, that message
   leaks it. Trace whether that is reachable.

   **Security — filesystem.** Cover path traversal and symlink handling in
   cockpit and projects-root resolution, the `$HOME` containment requirement
   (Docker providers on macOS only mount the home directory), and the
   permissions and atomicity of writes under `<cockpit>/results/` and the
   base-artifact trees.

   **Best practice.** Look for misplaced responsibilities, inconsistency in the
   `Gitlab` exception hierarchy (`ApiFailure`, `EndpointClosed`, `NotFound`,
   `RateLimited`, `TransportError`), duplication across the 21 command classes
   and `AbstractMrCommand`, and anything that leaks engine knowledge outside
   `src/Adapter/`.

3. Write `review-findings.md` in the plan directory. Use a table or one section
   per finding, but every finding needs: ID, file:line, lens, severity,
   proposed resolution, and the owning task (6, 7, 8, or 9).

4. Remember the scope rule from PRE_PLAN: a change qualifies only if it
   resolves a recorded finding. If you notice an improvement that is not a
   defect, record it as out-of-scope with a reason rather than assigning it to
   a remediation task. That is how this plan avoids the open-ended redesign
   that no-BC freedom would otherwise invite.

5. If a finding's correct resolution is genuinely ambiguous, record the
   question in the findings record rather than guessing, and flag it to the
   user. Do not invent an answer.

</details>
