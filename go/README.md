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

`internal/drupal` — the version semantics upkeep gets from `composer/semver`
today, which is the dependency a Go port cannot simply take with it.

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

## Scope, honestly

`internal/drupal` is one of 163 classes in `src/`. The PHP side is ~22,000
lines of source and ~33,000 lines of tests, and the tests are where most of the
hard-won behaviour is written down.
