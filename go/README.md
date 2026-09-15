# upkeep, in Go — a port in progress

An exploration, on a branch. Nothing here is wired to a command yet.

## How the port is being done

**The PHP implementation is the specification.** It is the one that has been
run against real drupal.org and GitLab data, and most of what it knows was
learned by being wrong in the field first: checking a merge request's merge ref
rather than its branch head, `ddev exec` re-joining its arguments before a
shell sees them, composer never installing a path dependency's `require-dev`,
patches cut against release tarballs that can never apply to a checkout. A port
that re-derives those from a reading of the code will re-learn them the same
way, from users.

So each piece is ported against a corpus the PHP generates:

1. `testdata_gen.php` runs the PHP implementation over a set of inputs and
   writes its answers to `corpus.json`.
2. The Go implementation is held to those answers exactly, in a test.
3. A randomised corpus is generated for the same question and compared again,
   because real-world inputs are not adversarial and the interesting bugs live
   where they are.

That third step has already paid: 2,703 random constraints found a semantic
difference that 161 real ones did not.

## What is here

| Package | What it covers |
| --- | --- |
| `internal/filesystem` | The single write path — atomic temp-then-rename — and path canonicalisation and containment |
| `internal/security` | Credential scrubbing and output redaction |
| `internal/proc` | The shell-out seam every engine interaction goes through |
| `internal/drupal` | drupal.org: version semantics, issue models, and the api-d7 client |
| `internal/gitlab` | git.drupalcode.org: the REST client, the failure taxonomy, merge-request and project models, token resolution |
| `internal/check` | What a check suite run amounts to: type, status, output excerpt, aggregate verdict |
| `internal/workflow` | The exit-code contract — 0 did what was asked, 1 the work failed, 2 upkeep could not do the job |
| `internal/config` | The Project Update Bot pattern, defined once |
| `internal/results` | The file-backed store of local check results, and what a row knows across its cores |
| `internal/gate` | The fast-lane classifier: READY-AUTO, REVIEW, BLOCKED |
| `internal/baseartifact` | The per-core base tree and dump: layout, the meta.yml sidecar, the status scan, and the core constraint |
| `internal/dashboard` | The row model, the snapshot, and the phrase-plus-command every row carries |
| `internal/patches` | The patch surface: which patch was meant, fetching it safely, what identifies it, and how an issue's work was delivered |
| `internal/cockpit` | The control directory, the module watchlist, and resolving a module whether or not it is watched |
| `internal/adapter` | Engine specifics: branch naming, the git remote, push diagnosis, shell quoting (the ddev engine itself is still to come) |
| `internal/maintenance` | The disk inventory and the prune selector — what is disposable, and what may never be |
| `internal/naming` | The module-name and core-version rules shared by everything that builds a path segment |
| `internal/invariant` | Checks over the source itself, for properties no single code path shows |

### registry.yml is shared, so its parsing is held to PHP's

Both implementations read and write the same registry, so a disagreement about
which files are valid is a disagreement about the user's own config — and YAML
parsers differ at exactly the edges this file lives on: an unquoted number, a
null mapping, a scalar where a list belongs.

`registry_expect.php` loads 28 fixtures through the real PHP loader and commits
the answers. The Go test compares accept/reject and the parsed content, not the
wording: the two word their refusals differently, and what has to agree is
*which* registries are accepted.

`levenshtein` is held to PHP's the same way — 3,012 cases in `corpus.json` —
because the "did you mean" threshold is an edit distance, and an implementation
that disagrees about one suggests a different module name than the tool it
replaces.

`meta.yml` gets the same treatment (`meta_expect.php`, 16 fixtures), and both
directions are covered: the PHP rendering is in the answers file and read back
here, and this implementation's own rendering is committed as fixtures that
`meta_expect.php` loads — so each side is proved to read what the other writes.
A base artifact set outlives whichever binary built it.

Issue payloads get the same treatment (`issue_expect.php`, 22 fixtures), and
for a sharper reason than the others: the dashboard snapshot stores raw api-d7
payloads verbatim, so a snapshot written by one implementation is read back by
the other. That forced a restructure — the client now rewrites each resolved
attachment *into the payload* the way PHP does, rather than keeping the
resolved files beside it, so live fetches and cache reads go through one path
and a snapshot is portable.

### The version semantics

This is the dependency a Go port cannot simply take with it.

| Question | Where |
| --- | --- |
| Does a branch's `core_version_requirement` support core N? | `Applies` |
| Is a resolved core version stable, alpha, beta, RC or dev? | `Stability` |

199 lines, plus tests, and no third-party dependency — the interval arithmetic
replaced the semver library it started with. Verified against **18,273 cases**
generated from `composer/semver`, with no divergence.

### Why it is not three lines of Masterminds

`Applies` asks whether a constraint *overlaps a whole major* —
`[N.0.0, N+1.0.0)` — which is constraint-against-constraint. Go's semver
libraries answer version-against-constraint, so the obvious approach is to
probe sample versions inside the major. That was tried, and it fails on any
constraint bounded inside the major: `>=10.50 <10.51`, `~10.4.0`, `10.6.*`.

Nine of twenty such cases were wrong, and **every one was wrong in the same
direction** — the constraint really did support the core and the probe said it
did not. That is a module vanishing from the dashboard for a core it supports,
which the design notes call the worst failure this tool has. So the
approximation was abandoned rather than tuned, and the intervals are computed
properly.

## Running it

```bash
cd go
go test ./...

php testdata_gen.php                  # corpus.json, committed — real and awkward cases
php testdata_gen.php --fuzz=20000     # corpus-fuzz.json, not committed — random cases
```

The random corpus is skipped when it has not been generated. It is seeded, so a
failure reproduces exactly.

## What the port has found so far

Every one of these is a Go runtime difference, not a domain mistake, and every
one was caught by a test rather than by reading the code.

- **`filepath.Join` calls `Clean`**, which folds `..` into the segment before
  it — the exact escape `PathGuard` exists to refuse. It hid a test first (the
  test passed while asserting nothing), then turned out to be in the
  implementation too: the walk up to the deepest existing ancestor rebuilt each
  candidate parent with `Join`, so the function performed the escape it exists
  to refuse.
- **`exec.CommandContext` kills only the direct child.** Every command upkeep
  runs is a launcher, and the grandchildren hold the output pipes open, so
  `cmd.Wait` blocks however long the timeout said. The suite took sixty seconds
  because two timeout tests waited out `sleep 30`s they had already timed out;
  a `ddev start` timeout would have waited for ddev regardless. Fixed with
  process groups and `WaitDelay`.
- **Chunked output splits lines.** The PHP splits each chunk on a newline, so a
  line arriving in two writes becomes two lines. Harmless for a transcript,
  wrong for a status line that overwrites itself.
- **A bare version is exact, not a range.** composer reads `7.8` as `=7.8.0`,
  found by the random corpus and not by the real one.
- **The legacy contrib branch convention is not recognised as a base branch.**
  `WorkingCopyStatus::isOnCustomBranch()` matches `1.0.x` and `2.x` and
  nothing else, so `8.x-1.x` — the convention a great many contrib modules
  still use, pathauto among them — reads as a developer branch. That makes
  `hasLocalWork()` true for every working copy on one, so every guard keyed on
  it refuses: stale teardown, prune, and the dirty-copy check before applying.
  Verified against the PHP directly. The Go port matches it rather than
  quietly fixing it, because the change loosens a guard on destructive
  operations and that is a decision to take deliberately.

- **A `.` path segment defeats containment for a path that does not exist.**
  `PathGuard::canonicalize` drops empty segments and keeps `.` ones, so
  `/root/./child` does not compare as inside `/root` — while `/root/../root/child`
  does, because the ancestor walk resolves it. Only reachable for a path that
  does not exist yet (an existing one is normalised by `realpath`), so nothing
  in upkeep reaches it today; the prune selector's protected-root check is the
  place it would matter. The Go version drops `.` as well as `""`, which is
  strictly safer — unlike `..`, a `.` cannot move a path anywhere.

- **JSON narrowing helpers that only accept `float64` break on their own
  output.** `encoding/json` decodes every number as `float64`, so readers
  written against a decoded payload work — until a model is rendered *back*
  through `ToAPIMap` and read again without a JSON round trip, where the ints
  are real ints. PHP's `is_int` covers both without anyone thinking about it.
  Found by the row factory's tests, which build snapshots in-process; the
  differential corpora all read from disk and so could not have caught it.

- **The issue category is an id in the payload and a label on the page.**
  `field_issue_category` is `1`; the issue page says "Bug report". The port
  passed the id straight through, so it would have printed `1` where upkeep
  prints `Bug report`. Caught by building the issue-payload corpus, not by
  reading the code — the field looked like every other string field.

- **`preg_replace` works on bytes; Go's `regexp` works on runes.** The
  filename an untrusted API supplies is reduced to a safe path segment by
  replacing everything outside `[A-Za-z0-9._-]` with an underscore. PHP runs
  that without the `/u` modifier, so a two-byte `é` becomes **two**
  underscores; the obvious Go translation makes one. Both are safe, but both
  implementations cache the downloaded patch under the name this produces, so
  the difference would put the same patch at two paths. The Go version is
  byte-wise, held to `safenames.json`.

- **The base-artifact size measure does not stay inside the tree.** Its own
  doc comment says it does, and for symlinked *directories* that is true —
  `FOLLOW_SYMLINKS` is unset. But a symlinked *file* is a leaf, and
  `SplFileInfo::getSize()` follows the link: measured on a tree holding 1000
  bytes plus one link to a 5000-byte file outside it, PHP reports 7000. Every
  `vendor/bin/*` entry is also counted a second time on top of the real file it
  points at. It is only a size report, so nothing downstream breaks — but the
  Go version follows no symlink at all, and the divergence is deliberate.

- **The gate trusts that its two arguments describe the same cores.** It takes
  the cores a row applies to and the evidence separately, and checks only that
  the evidence is non-empty — never that it covers them. The PHP row factory
  builds both from one list, so they agree by construction and nothing is
  broken today. But what the gap would produce is exactly the failure the row
  model exists to prevent: a merge request green on 11, never asked about 10,
  reading as fully green. The Go gate checks (`LocalEvidence.Covers`), which
  makes the invariant structural rather than conventional. Worth doing on the
  PHP side too.

- **`url.PathEscape` already escapes a slash.** I wrote a helper to add that,
  with a comment asserting the opposite, and the mutation test that should have
  caught a bare interpolation reported the helper itself as dead weight instead.
  The helper is gone; the escaping is still pinned by a test.

- **Map iteration order is random in Go**, so a rendered list of statuses had
  to become a declared slice rather than a map walk.

## Scope, honestly

`internal/drupal` is one of 163 classes in `src/`. The PHP side is ~22,000
lines of source and ~33,000 lines of tests, and the tests are where most of the
hard-won behaviour is written down.
