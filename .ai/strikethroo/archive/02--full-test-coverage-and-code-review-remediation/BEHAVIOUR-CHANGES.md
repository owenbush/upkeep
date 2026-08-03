# Operator-Visible Behaviour Changes — Plan 02

Every change below alters what a user or a script observes. Task 19 must
document all of them; task 12 must assert the new behaviour, not the old.

Sources: tasks 6, 7, 8, 9. No backwards-compatibility constraint applies
(decision recorded during planning: the repository is unreleased), so these are
intentional.

## CLI surface

1. **`upkeep exec` no longer passes the child's exit code through.**
   0 → 0; any non-zero child code → **1**; child that never started → **2**.
   `README.md:266` currently states "The exit code is passed through, so you can
   script against it" — that sentence is now false and must be rewritten.
   (Maintainer decision D2 explicitly declined an exemption for `exec`.)

2. **`base-artifacts:build --core=N` renamed to `--version=N`**, aligning with
   the documented rule that `--version` is the target-core selector.
   `README.md:151` and `README.md:365` must change. The "no artifacts yet" hint
   in `base-artifacts:status` now says `--version=N`.

3. **`upkeep issue <module> <mr>` now validates the IID.** `abc`, `0`, and `-3`
   are rejected with exit 2 instead of silently becoming `!0`.

4. **Diagnostics moved from stdout to stderr** for `exec` and `env:path`, so
   `cd $(upkeep env:path x)` no longer captures error text into the path.

5. **`upkeep status` header now reports the registered-module count.**
   `status` and `base-artifacts:*` now parse the registry rather than only
   stat-ing it, so a malformed registry fails them too.

## Exit codes

The 0/1/2 contract now covers all 19 commands (decision D2), expressed once in
`Command\UpkeepCommand::execute()`. `ExitCode::CHECKS_FAILED` was renamed
`ExitCode::FAILED` because it now also covers merges and `exec`.

Contract wording: **0** the command did what was asked; **1** the work it
supervised failed; **2** upkeep could not do the job.

6. **"No token" now exits 2, not 1**, from `api:probe`, `dashboard`, `merge`,
   `notes`, `issue`, `needs-work`, and `modules:add`. Rationale: no credential
   means no verdict was produced, which is what 2 means. `README.md:209`'s
   exit-code table must be generalised from check/review to the whole CLI.
   **Deliberate exception**: `patches` without a token is a documented degraded
   mode — it warns once and still exits **0**.

7. **Everything previously exiting 1 for an upkeep-side problem now exits 2** —
   missing or malformed cockpit or registry, unregistered module, untracked core
   version, no provisioned environment, API failure.

   **Correction (recorded during task 19, verified):** this item originally also
   listed "bad usage" under exit 2. That is wrong, and the docs were written to
   the true behaviour rather than to this claim. Symfony's `Application` handles
   argument and option parse failures *before* `UpkeepCommand::execute()` runs,
   so console-level usage errors exit **1**: `upkeep nosuchcommand`,
   `upkeep issue` with missing arguments, and `upkeep base-artifacts:build
   --core=11` were each confirmed to exit 1. Errors upkeep itself validates —
   a malformed MR IID, a missing `--version` on `base-artifacts:build` —
   correctly exit 2. Closing that gap would mean overriding the application's
   exception handling, which is a code change and was out of scope for a
   documentation task.

8. **`merge` exits 1 when any merge failed** (previously exited 0
   unconditionally), and **2** when GitLab rejected the credential
   (`Unauthorized`).

## Messages

9. **Canonical wordings replaced bespoke per-command text.** "Not registered"
   now lists the registered modules; untracked core now says "does not track
   core version … Add it to core_versions in registry.yml"; five commands lost
   their own "No cockpit found at …" in favour of the registry's
   "Run `upkeep init` …".

## On-disk formats

10. **`.upkeep-env.yml` gains `last_used_at`**, stamped at provision and
    re-stamped on every reuse. This fixes `prune --older-than`, which previously
    read a field nothing ever wrote and silently fell back to `created_at` —
    meaning it could delete actively-used environments. Existing environments
    parse unchanged (the key is optional) and fall back until next reused.
    **No rebuild required.**

11. **`<cockpit>/results/` and `<cockpit>/cache/dashboard/` are now `0700`
    directories with `0600` files.** Files already on disk keep their old mode
    until re-created, so an existing cockpit wants a one-off:
    ```
    chmod -R go-rwx <cockpit>/results <cockpit>/cache
    ```
    A version-controlled cockpit should also exclude `results/` and `cache/`.

## Refusals that were previously silent acceptances

12. **`--projects-root` / `$UPKEEP_PROJECTS_ROOT` outside `$HOME` is now
    refused.** The error explains why: the path is bind-mounted into the Docker
    VM and macOS providers only share the home directory, so an environment
    outside it can never start. **No escape hatch was added** — if one is
    wanted, it needs a decision.

13. **An unreadable `<cockpit>/results/<...>` directory now raises** instead of
    reporting "never checked".

14. **`init` and `modules:add` now return failure** where they previously
    printed success over a write that never landed.

## Security-relevant

15. **`upkeep exec` no longer forwards `UPKEEP_GITLAB_TOKEN` to the child
    process.** The credential is scrubbed from every child environment. Any
    workflow relying on the child inheriting that variable will break, and
    should read the token from its own source instead.
