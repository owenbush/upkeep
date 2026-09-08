# Running checks

The isolated check flow: what it runs, how it matches drupal.org CI, and what it honours from your module.

## Check what you are working on

```bash
upkeep check widget --working-copy --version=11
```

Runs the full suite against whatever the module working copy is currently on —
after `start`, after `patch:promote`, or after your own edits. It needs no
merge request and no GitLab token, names the branch it checked, and warns if
the tree has uncommitted changes (they are included in the run).

**It caches nothing.** Every other check result is keyed by a subject and a
revision — an MR and its head SHA, a patch and its source URL — so the
dashboard can tell a fresh pass from one about work that has since moved. A
working copy has neither, and an entry keyed on a guess would put
permanently-fresh-looking evidence in front of the fast-lane gate. So this mode
reports and exits, which is all "did I break it?" needs.

Pair it with `upkeep dev widget` when you want to click through the site.

## Check an MR

```bash
upkeep check field_visibility_conditions 2 --version=11
```

runs that MR through the full isolated flow: ensure the (module × core)
environment exists (built from the base artifact on first use), apply the MR
via a Composer path repository, run every check, print the per-check report,
and cache the results where the dashboard reads them.

Flags: `--version=N` selects the target core major (must be tracked by the
module's registry entry; defaults to the first one listed). `--fixture=NAME`
loads a named database fixture before checking (see Fixtures below). Note
that `--version` is *always* the core selector — `upkeep --version` does not
print an application version, by design.

See [Exit codes](../reference/exit-codes.md) for what `check` returns.

## Your module's own phpstan.neon and phpcs.xml.dist are honoured

Both tools discover their configuration from the working directory, and CI
runs them from **inside the module** — `.phpstan-base` opens with
`cd $DRUPAL_PROJECT_FOLDER`, `.phpcs-base` with `cd $CI_PROJECT_DIR` — so a
module that ships its own config gets it used, and the gitlab_templates
default is only a fallback.

upkeep now does the same — it used to run both with only the template it had
just downloaded in view, so your level, baseline, ignores and ruleset were
silently ignored and the check reported a verdict against rules your project
does not use.

It gets there by *naming* your config rather than by changing directory into
your module, and that difference matters: in CI the module repo root is where
`composer install` put `vendor/`, but under ddev-drupal-contrib your module is
a checkout symlinked into a site whose `vendor/` lives at the project root. Run
from inside the module, every vendor-relative path in a ruleset breaks —
`Referenced sniff "./vendor/drupal/coder/coder_sniffer/Drupal" does not exist`.
So the working directory stays where `vendor/` actually is.

The fallback config is kept at the project root rather than written into your
module: CI drops it beside the code because the container is thrown away, but
here that directory is your git checkout, and two untracked files in it would
make the next `patch:apply` or `start` refuse on a dirty working copy.

Your module's own `require-dev` is installed alongside the check toolchain,
because a ruleset that references `./vendor/phpcompatibility/…` needs the
package your module requires and the site does not. CI has it because
`composer install` runs in your module's repository; here your module is a
path repository of the site, and composer never installs a path dependency's
dev requirements. If any of them cannot be installed the run carries on with a
warning — a version conflict in a linting dependency should not take down your
tests.

## An MR is checked the way CI checks it

GitLab publishes two refs for every merge request:

- `refs/merge-requests/<iid>/head` — the contributor's branch.
- `refs/merge-requests/<iid>/merge` — that branch merged into the **current**
  tip of the target.

**CI analyses the second one**, and so does upkeep. The distinction is not
academic: an MR branch is a commit or two of work sitting on the target *as it
was when the branch was cut*, and fetching it gets you the newest version of
that — not the target's newer commits, which were never pushed to it. Measured
on pathauto, branches run 7 to 41 commits behind, and **23 of 25 open merge
requests have a merge tree that differs from their head tree**. Checking the
branch meant agreeing with CI by luck.

When GitLab publishes no merge ref, it could not merge the branch into its
target — normally a conflict. upkeep checks the branch alone and says so,
because a branch-only verdict is not the one CI would give.

Local check results are keyed on that merge ref's SHA, not the branch head's.
The merge tree changes when **either** side moves, so a result keyed on the
head would still read as current after the target gained a commit — the same
"evidence about a tree nobody checked" one level down. When a commit lands on
the target, your cached results for that branch go stale and say so.

## The base is brought up to date first

Every command that cuts a branch — `patch:apply`, `patch:check`,
`patch:promote`, `start` — fetches the base from origin first and cuts from
what came back. This is not a convenience; it is the difference between a
verdict that means something and one that does not.

**drupal.org's CI does not check your branch.** It checks
`refs/merge-requests/<iid>/merge`: your work merged into the *current* tip of
the target. A module working copy is cloned once, so its `2.0.x` sits still
while drupal.org's moves on — and a patch checked on top of a stale base is
checked against code CI will never run.

That failure is silent, because git has nothing to complain about. A real
case: a base sixteen months old, a target since rewritten for Drupal 12 (the
module file moved to OOP hooks and lost a `use` import), and a patch that only
added a function. The merge was clean. In the merged file the added block was
the only thing still naming the imported class, with no import left, so it
resolved to the global namespace. Local check green; CI red on one line, with
no hint that the two had looked at different code.

When the base has moved you are told how far:

```
2.0.x was 47 commits behind origin — updated. CI tests your work merged into
this, so a check against the old tip could have disagreed with it.
```

The local base is fast-forwarded when it can be, and **never reset**. A base
carrying your own commits is left exactly as it is, and upkeep says so and
cuts from origin anyway — because that is what CI merges into, and because
discarding unpushed work to tidy a check is not a trade it gets to make.

If the fetch fails, upkeep **refuses** (exit 2) rather than checking against
the stale base. Everywhere else it degrades and says so; here the degraded
result is a verdict that looks exactly like a good one and gets cached as
evidence. `--no-update` makes that a choice instead — for working offline, or
for reproducing a verdict against the tree as it was.

**Nothing is pushed.** `patch:promote` stops at the commit; publishing somebody
else's work under your account is a step a human types. It is also worth
running `patch:check` first — a patch that only applies with reduced context is
a weaker guarantee than a merge request implies.

**It assumes you can push to the project.** `publish` pushes to `origin` and
opens the MR on the canonical project, which is the maintainer's flow.
Contributors without push access need a drupal.org issue fork, which
drupal.org's own UI creates.
