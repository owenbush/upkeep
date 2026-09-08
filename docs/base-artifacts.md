# Base artifacts: what they are, and their lifecycle

Every environment upkeep provisions starts from a **base artifact set** — one
per Drupal core major, built once and copied from thereafter. This document is
the lifecycle: how one is built, what happens when you rebuild it, what a
rebuild propagates to, and the sharp edges. The design rationale for having
them at all is `contrib-maintainer-design.md` §6 (fast cold starts); the
user-facing quick start is the README.

## 1. What one is

```
<cockpit>/base-artifacts/<core-major>/
    tree/                  resolved drupal/recommended-project base tree
    clean-install.sql.gz   gzipped module-free clean-install DB dump
    meta.yml               exact core version, PHP, DB engine, build timestamp
    canonical              marker: never auto-pruned
```

Two artefacts, because environment setup has two expensive halves and both are
identical across every module tested on that core: resolving a Drupal codebase,
and installing Drupal into a database. The tree is copied (`cp -a`) rather than
re-resolved — verified byte-identical to a from-scratch resolve — and the dump
is loaded rather than re-installed.

The set is **canonical**: `Maintenance\Category` marks the whole
`base-artifacts/` directory unprunable, and `PruneSelector` reports
`canonical base artifact` for anything under it. Environments are disposable
and get pruned; these are not, and are rebuilt only when you say so.

The tree is **module-free**. Drush is required only in the throwaway install
project used to produce the dump, which is torn down afterwards, so nothing
module- or task-specific leaks into what every later environment copies.

## 2. Building

```bash
upkeep base-artifacts:build --version=11
```

A build spins up a throwaway ddev project, installs Drupal, dumps the database
and tears itself down — a few minutes, mostly composer. It rides the shared
global composer cache, so the second major you build downloads far less than
the first.

`--scratch-dir` defaults under `$HOME` and must stay there. That is not
hardening: the throwaway project is bind-mounted into the Docker VM, and macOS
providers only share the home directory by default, so a scratch dir in `/tmp`
fails to mount and the install dies.

### A core major with no stable release yet

`drupal/recommended-project:^12` resolves to nothing while 12 is in alpha —
packagist carried exactly one 12.x release, `12.0.0-alpha1`, when this was
written — and composer reports only that it could not find a matching version.

```bash
upkeep base-artifacts:build --version=12 --stability=alpha
```

This is not a corner case for this tool. A Drupal major spends **months** in
alpha and beta, and that is precisely when compatibility work happens: the
Project Update Bot merge requests upkeep's fast lane exists to merge are about
the *unreleased* core. Not being able to build for it means not being able to
answer the question upkeep is most often asked.

**It is a flag, never an automatic fallback.** Substituting a pre-release when
a stable constraint finds nothing would quietly build something other than what
was asked for, and a base artifact set is the thing every later verdict is
measured against — the one place a silent substitution is least acceptable. So
you ask for it, and the exact resolved version is recorded either way:
`meta.yml` takes `core_version` from the lock, so `base-artifacts:status` shows
`12.0.0-alpha1` under **Exact core** and cannot be mistaken for a release.

A failed *stable* resolve gets a hint appended to composer's own words, naming
the flag and the command. Once a stability **was** given the hint is
suppressed: the constraint is then not the obvious suspect, and repeating
advice already taken would bury whatever composer actually said.

Accepted values are composer's own, loosest first: `dev`, `alpha`, `beta`,
`RC`, `stable`. A misspelling is refused here, naming the real ones, rather
than handed to composer — which answers a bad `@` suffix with a parse error
about the whole constraint.

## 3. Rebuilding

```bash
upkeep base-artifacts:build --version=12 --stability=alpha --force
```

`--force` is required over an existing set; without it the build refuses with
*Base artifacts for core 12 already exist at … Re-run with `--force` to rebuild
deliberately.* There is no in-place update: a rebuild is a fresh
`composer create-project`, so it resolves against packagist as it stands that
day and you get the newest release your constraint admits.

### You do not need to raise the stability as the major matures

A composer stability is a **minimum**, not a pin, and pre-release versions sort
below the release they precede:

```
12.0.0-alpha1 < 12.0.0-alpha2 < 12.0.0-beta1 < 12.0.0-rc1 < 12.0.0 < 12.1.0
```

`^12@alpha` therefore admits everything alpha-or-better, and composer takes the
highest version available. The same command walks itself forward through beta,
RC and the stable release with no intervention. Pass a tighter stability only
when you want to *stop* accepting the looser ones — `--stability=beta` once
betas exist and you no longer want alphas.

### The stability is not persisted

It is a property of the invocation, not of the artifact set. A `--force`
rebuild that omits `--stability` goes back to plain `^12`. While 12 is still
pre-release that fails — with the hint naming the flag, so it tells you rather
than doing something surprising — and once 12 is released, plain `^12` is what
you wanted anyway.

If you need to know what a set actually holds, read **Exact core** in
`base-artifacts:status`; that is the resolved version from the lock, which is
the honest answer regardless of what constraint produced it.

## 4. What a rebuild propagates to

### Existing environments detect it themselves

Each environment's `.upkeep-env.yml` records `seed_core_version` — the base
artifact core version it was seeded from. `EnvironmentMeta::staleReasons()`
compares it against the canonical artifact's on every `ensureEnv()`, so a
rebuild trips:

> Seed skew: environment was seeded from base artifact core 12.0.0-alpha1, the
> canonical artifact is now core 12.0.0-beta1.

The next `check` or `review` tears that environment down and re-provisions it
from the new tree. Nothing to run by hand, and no way to keep silently testing
against a tree that no longer exists.

### The check toolchain follows the seed, without a second flag

`drupal/core-dev:^12` resolves to nothing for exactly the same reason
`drupal/recommended-project:^12` does. So the flag alone would have built a
base artifact set fine, provisioned an environment fine, and then failed at the
first check — after all the expensive work.

`DdevContribAdapter::ensureCheckToolchain()` therefore derives the stability
from the artifact meta's resolved `core_version`, via composer's own
`VersionParser::parseStability()` (`BaseArtifact\CoreConstraint::stabilityOf()`):

| seeded core     | toolchain constraint         |
| --------------- | ---------------------------- |
| `12.0.0-alpha1` | `drupal/core-dev:^12@alpha`  |
| `13.0.0-beta2`  | `drupal/core-dev:^13@beta`   |
| `11.4.6`        | `drupal/core-dev:^11`        |

**Derived, not asked for again.** A second flag could disagree with the tree it
is installing into. And because the answer comes from composer's parser rather
than a table of majors and suffixes, 13 and everything after it need no change
here — that is the property that makes this maintenance-free.

The suffix goes on **core's constraint only**. `drupal/coder@alpha` carries no
version constraint at all, so it would tell composer that an alpha of a package
with nothing to do with the seeded core is acceptable. Only templates that
interpolate the core major get the suffix; a test holds that.

## 5. Sharp edges

**A forced rebuild removes the old set before building the new one.** `--force`
`rm -rf`s the version directory first, and a failed build removes the partial
set too (an existing version directory must always mean the last build
completed). The consequence: if a forced rebuild fails — a network blip, a
resolve that no longer works — you are left with **no** artifact set for that
core, and every command needing an environment for it refuses until you build
one successfully. Rebuild when you can afford to retry, and note that the
previous artifacts are not recoverable from the cockpit. *(Building to a
sibling directory and swapping on success would remove this; not done.)*

**Re-provisioning refuses over uncommitted work.** A stale environment whose
module working copy has local changes is not torn down:

> Environment upkeep-paragraphs-d12 is stale and needs re-provisioning, but the
> module working copy has local work: … Push or stash your work, then re-run.

Discarding somebody's unpushed work to refresh a cache is not a trade upkeep
makes. So rebuild base artifacts when you are not mid-contribution, or expect
to be asked to deal with the working copy first.

**Rebuilding is disk churn, not disk savings.** The new tree is a full resolve,
and every environment for that core re-provisions on next use. `status --disk`
before and `prune` after is the honest sequence if space is tight.

## 6. Where this is enforced

| Concern | Code |
| --- | --- |
| Constraint, stability validation, hint, derived toolchain stability | `BaseArtifact\CoreConstraint` |
| Build orchestration, `--force` semantics, partial-set cleanup | `BaseArtifact\BaseArtifactBuilder` |
| On-disk layout, canonical marker | `BaseArtifact\ArtifactLayout` |
| Completeness scan behind `base-artifacts:status` | `BaseArtifact\ArtifactScanner` |
| Seed-skew detection | `Adapter\EnvironmentMeta::staleReasons()` |
| Toolchain provisioning | `Adapter\DdevContribAdapter::ensureCheckToolchain()` |
| Prune protection | `Maintenance\Category`, `Maintenance\PruneSelector` |
