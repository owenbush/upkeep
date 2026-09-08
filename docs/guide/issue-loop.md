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
