# Review, merge and release notes

Looking at the thing in a browser, the one-approval-per-merge fast lane, and drafting notes afterwards.

## Review an MR in the browser

```bash
upkeep review field_visibility_conditions 2 --version=11
```

applies the MR to the running site in that environment, makes sure the module
is installed, and prints the site URL plus a one-time login URL.

## Fast-lane merge

```bash
upkeep merge --fast-lane
```

walks the current `READY-AUTO` rows **one at a time** and asks for an
explicit per-MR approval; only an approved row is merged, via a single API
call. Rows that are not `READY-AUTO` are shown in a non-actionable summary
and are never offered. Immediately before each merge the MR is re-fetched:
head-SHA drift, a CI regression, a state change, or a new draft marker
demotes the row (with reasons) instead of merging, and the merge call itself
carries the expected head SHA so GitLab rejects races the re-check cannot
see.

**Policy stance.** The Drupal Association permits automation to *prepare*
work but expects merges to be individual human actions. Upkeep enforces that
structurally: one prompt, one approval, one API call per MR. There is no
batch mode, no flag that merges without prompting, and no unattended mode in
v1 — an unattended mode would need explicit DA approval first. `--fast-lane`
is required precisely because the command performs merges; the flag names the
workflow, it never skips approval.

**Degraded path.** If the GitLab instance refuses API merges (the merge
endpoint closed to PATs — HTTP 403), an approved row is not lost: Upkeep
prints the exact browser merge URL for that MR and records it as handled
manually. You perform the same single human action, just in the browser. A
rejected *credential* is HTTP 401 and is reported differently, because "use
the browser" would be the wrong advice for a bad token.

**Exit codes.** `merge` exits 0 when everything you approved went through (or
you approved nothing), 1 when any merge failed, and 2 when GitLab rejected the
credential or no token was configured.

## Issue status

Most merge requests link to a drupal.org issue (via the `Issue #NNN:` title
convention or the branch name). To view the linked issue's details and open
it in the browser — where you can change the status to RTBC, Needs work,
etc.:

```bash
upkeep issue entity_type_access_conditions 2
```

This extracts the issue number from the MR, fetches the issue from
drupal.org (title, status, priority, version), and opens the drupal.org
issue page in your browser. Use `--no-open` to just print the details
without launching the browser.

The `<mr>` argument must be a merge request IID — a positive integer.
Anything else (`abc`, `0`, `-3`) is refused with exit 2 rather than being
turned into a request for `!0` and a confusing 404. The same rule applies
everywhere an MR number is taken: `check`, `review`, `issue`, `needs-work`.

The dashboard also shows issue status for each MR in the ISSUE column (e.g.
`#3467675 (review)`), pulled live from the drupal.org API.

> **Note:** the drupal.org API is read-only, so status changes go through
> the browser. The `issue` command gets you there in one step.

## Release notes

```bash
upkeep notes conditions_helper
```

drafts paste-ready Markdown release notes: every MR merged since the module's
last tag, grouped and linked. Takes a registered machine name or a full
project path (e.g. `project/conditions_helper`).
