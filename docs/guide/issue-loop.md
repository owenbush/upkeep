# Work on an issue

The loop that starts at an issue rather than at somebody else's merge request: issues, start, publish.

The other three commands start from a *contribution* — a merge request, or a
patch somebody already posted. These start from the issue itself, which is
where a maintainer's own work begins.

```bash
upkeep issues pathauto                  # every open issue, and what is on it
upkeep issues pathauto --unclaimed      # only what nobody has started
upkeep issues pathauto --status=active
```

```
ISSUE       STATUS      PRIORITY  CONTRIBUTION  TITLE

#3608478    RTBC        Major     !183          Memcache Transaction-Aware Issue After…
#3616056    review      Normal    1 patch       Deprecated function: Using null as an…
#3223746    review      Normal    unclaimed     JSON API response has empty path when…
#3611658    active      Normal    unclaimed     Generator pattern cache grows unbounded…

93 open issues · 42 awaiting you · 29 unclaimed
```

`unclaimed` is the point of the view: an issue nobody has contributed to is the
most actionable row on the list. The old scan covered Needs Review and RTBC
only — 42 of pathauto's 93 open issues — so everything Active, Needs work or
Postponed was invisible, which is precisely where work begins.

Then:

```bash
cd $(upkeep start pathauto 3223746 --version=11)
# ... write the fix, commit it ...
upkeep publish pathauto 3223746
```

`start` provisions the environment and opens a branch named to drupal.org's
issue-fork convention (`<nid>-<slug>`), so drupal.org, GitLab and upkeep all
recognise it as belonging to that issue. Re-running **resumes** — an existing
branch is checked out as it stands, and nothing here ever resets, forces or
discards. That is the difference from the `mr-` and `patch-` branches, which
*are* reset on every apply so a contribution is tested alone.

`publish` pushes the branch and opens the merge request. What it pushes
re-enters the pipeline that already existed: an ordinary MR that `dashboard`,
`check` and `merge` have always handled. Re-running after more commits reports
the MR that exists rather than duplicating it.

**It opens merge requests; it never merges them.** Those are opposite acts, and
merging stays behind `merge --fast-lane`'s per-MR prompt.

**It cannot create issues or change their status.** drupal.org's API is
read-only (a POST answers 403), so that will always be a browser action —
`upkeep issue` gets you there in one step.

## Projects whose issue queue has moved to GitLab

drupal.org is moving issue queues into GitLab, one project at a time. Where a
project keeps its issues is a property of the project, not a setting you
configure, so upkeep asks drupal.org and reads whichever queue is live:

```bash
upkeep issues helper
```

```
helper keeps its issues in GitLab, not on drupal.org — read from GitLab.
```

That line is printed only when a project has moved. Everything downstream is
unchanged: the same `ISSUE`/`STATUS`/`PRIORITY` columns, the same `start` and
`publish`, the same dashboard rows. An issue is an issue wherever it is filed.

**Merge requests never moved.** They have always lived in GitLab, so `check`,
`review`, `merge`, `checkout` and `publish` behave identically on a migrated
project. The one difference you will notice is that merge requests tend to come
from the canonical project rather than an issue fork, because a maintainer with
write access needs no fork — [taking over an
MR](./taking-over-an-mr.md) covers both shapes.

### What changes, and what does not

| | drupal.org queue | GitLab queue |
|---|---|---|
| issue numbers | node id | issue iid — the same number for a migrated issue |
| statuses | Needs review, RTBC, … | `state::needsReview`, `state::rtbc`, … labels |
| patch files on an issue | yes | **no** — contributions arrive as merge requests |
| `upkeep patches` | lists them | finds nothing, because there is nothing |

The patch surface finding nothing on a migrated project is the truth rather
than a gap. A project that has moved takes contributions as merge requests, so
`upkeep dashboard` and `upkeep issues` still show all the work; only the
patch-shaped half of it is absent, because nobody posts patches there.

### Why upkeep reads GitLab at all

api-d7 is *not* stale for a migrated project — and it is worth saying so,
because the obvious assumption is wrong. Measured on `helper`: api-d7 lists 73
open issues against GitLab's 74, and the statuses agree exactly. Every migrated
issue kept its drupal.org node, which still answers, and
`drupal.org/node/<nid>` redirects to the GitLab work item.

What justifies reading GitLab is the one issue in that difference. An issue
opened *after* the migration has no drupal.org node at all, so no api-d7 query
can return it however it filters — and today that is 1 issue of 74, while it is
**every future issue** on that project.

There is a sharper edge underneath. GitLab continues its issue numbering from
the migrated node ids, so a post-migration issue's number is also some
unrelated drupal.org node's id — on `helper`, issue 3619172 is "Drush command
to run a module hook" while drupal.org node 3619172 is an unrelated change
notice. Reading the issue from the queue that owns the number is what keeps the
two attached.

### When drupal.org will not say

If the project node cannot be read, upkeep reads drupal.org's queue — the
behaviour every release so far has had — and the failed read is reported with
the run's other warnings. It does not guess that a queue has moved: guessing
wrong means scanning an empty queue and reporting a module as having no open
issues at all, which is the worst failure this surface has.
