---
id: 13
group: "orchestrator-commands"
dependencies: [9, 11]
status: "pending"
created: 2026-07-29
skills:
  - symfony-console
  - ddev
complexity_score: 4
---
# Implement the check and review commands

## Objective
Implement the single-MR workflow commands: `upkeep check <module> <mr>` runs one MR through the full isolated flow (ensure environment → apply MR → optional fixture → run checks → report + cache results), with `--version=<N>` and `--fixture=<name>` flags; `upkeep review <module> <mr>` puts the MR onto a running site and prints its browsable `*.ddev.site` URL.

## Skills Required
`symfony-console` for the commands and progress/reporting output; `ddev` awareness for interpreting adapter behavior (all engine specifics stay behind the adapter).

## Acceptance Criteria
- [ ] `upkeep check <module> <mr>` on a real open MR: provisions (or reuses) the correct (module × core-version) environment via the adapter, applies the MR, runs the checks, prints a per-check pass/fail summary with durations, exits 0 on all-green and non-zero if any check failed — demonstrated live with captured output.
- [ ] `--version=12` runs the same MR against a second core version in a separate environment; both environments coexist afterwards (`ddev list` shows both projects).
- [ ] `--fixture=<name>` loads the named fixture before checks; a missing fixture aborts with a clear error before any check runs.
- [ ] Results are written to the shared results cache keyed by (module, MR iid, core version, MR head SHA), and a subsequent `upkeep dashboard` shows the fresh LOCAL status for that row.
- [ ] `upkeep review <module> <mr>` prints the project URL and `curl -sI` of that URL returns HTTP 200 with the MR branch checked out in the environment.
- [ ] The commands reference only the adapter interface and the GitLab client — no direct `ddev` shell-outs in command code (same grep guard as task 10).

Use your internal Todo tool to track these and keep on track.

## Technical Requirements
- Target core version default: each tracked core version from the registry entry when `--version` is omitted? No — default to the MR's native target's core context; explicit `--version` selects among the module's tracked versions. Resolve the MR via the GitLab client first (existence, target branch) before touching environments.
- Progress output while long steps run (environment seeding, checks) so the command doesn't sit silent.
- Results cache writer shared with task 12's reader (same layout).

## Input Dependencies
- Task 11's complete adapter (all six operations).
- Task 9's client (MR resolution, head SHA for cache keying).

## Output Artifacts
- `check` and `review` commands; the populated results cache that feeds the dashboard's LOCAL column and the fast-lane gate's local-checks input.

## Implementation Notes
`check` is the workhorse the whole tool exists for — keep its output readable at a glance (one line per check, clear failure excerpts at the end). On adapter errors (environment failed to provision), exit distinctly from check failures so scripting can tell infrastructure problems from red checks.

<details>
<summary>Detailed steps</summary>

1. Shared `MrContext` resolver: registry lookup → client MR fetch → validate open + native-base (surface the adapter's backport rejection early) → determine core version (flag or default per registry).
2. `check`: adapter `ensureEnv` → `applyMr` → optional `loadFixture` → `runChecks` → render summary table → persist `CheckResult`s to cache with head SHA → exit code mapping (0 green, 1 check-failed, 2 infrastructure-error).
3. `review`: same context resolution → `ensureEnv` → `applyMr` → `serve` → print URL prominently.
4. Live demonstration per acceptance criteria; keep the captured transcript as evidence.
</details>
