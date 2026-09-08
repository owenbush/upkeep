# Exit codes

0 did what was asked, 1 the supervised work failed, 2 upkeep could not do the job.

Every Upkeep command answers with one of exactly three codes. This is a
contract you can script against, and it is the same contract for all of them —
not just `check` and `review`:

| Code | Meaning |
| ---- | ------- |
| 0 | the command did what was asked |
| 1 | the work it supervised failed — a red check, a merge GitLab refused, a non-zero command run through `exec` |
| 2 | Upkeep could not do the job, so there is no verdict — no cockpit or registry, no token, an unregistered module, an untracked core version, an argument Upkeep rejected, an unresolvable MR, an engine or API failure |

The distinction that matters to a script: **1 means look at the subject, 2
means look at your setup.**

Consequences worth knowing:

- **No GitLab token exits 2** — from every command that needs one
  (`api:probe`, `check`, `review`, `dashboard`, `merge`, `notes`, `issue`,
  `needs-work`, `modules:add`), because no credential means no verdict was
  produced. The one deliberate exception is `patches`, whose token-less mode
  is documented and degraded rather than broken: it warns once, scans without
  cross-referencing merge requests, and still exits 0.
- **`merge` exits 1 when any merge failed**, and 2 if GitLab rejected the
  credential (HTTP 401).
- **`upkeep exec` does not pass the child's exit code through** — see
  [Working in an environment](../guide/environments.md).
- Anything Upkeep itself could not do exits 2, including a malformed
  `registry.yml`, a module missing from it, a core version the module's entry
  does not track, a merge-request argument that is not a positive integer, and
  a per-MR directory under `<cockpit>/results/` that exists but cannot be
  read. That last one used to be reported as "never checked" — indistinguishable
  from a genuinely unchecked MR, and enough to make the fast-lane gate withhold
  a merge for a reason invisible to you.
- A command that could not write what it was asked to write **fails** rather
  than printing success — `init` and `modules:add` in particular. Every file
  Upkeep produces is written to a temporary file and renamed into place, so a
  reader never sees a half-written registry and a failed write is never
  reported as a completed one.

The error messages are deliberately uniform, so the same situation reads the
same way whichever command you hit it from. An unregistered module lists the
modules that *are* registered; an untracked core version names the ones the
entry does track and tells you to add it to `core_versions` in `registry.yml`;
a missing cockpit always says to run `upkeep init` or point `--cockpit` /
`UPKEEP_COCKPIT` at an existing one.

One caveat: a command line the Symfony console cannot even parse — an unknown
command, an unknown option, a missing required argument — is rejected by the
console before Upkeep's contract applies, and exits **1**. The three codes
above describe every invocation Upkeep actually runs.
