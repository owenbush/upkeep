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
| `internal/invariant` | Checks over the source itself, for properties no single code path shows |

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
- **Map iteration order is random in Go**, so a rendered list of statuses had
  to become a declared slice rather than a map walk.

## Scope, honestly

`internal/drupal` is one of 163 classes in `src/`. The PHP side is ~22,000
lines of source and ~33,000 lines of tests, and the tests are where most of the
hard-won behaviour is written down.
