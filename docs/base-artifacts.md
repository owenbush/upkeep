# Base artifacts: what they are, and their lifecycle

Every environment upkeep provisions starts from a **base artifact set** — one
per Drupal core major, built once and copied from thereafter. This document is
the lifecycle: how one is built, what happens when you rebuild it, what a
rebuild propagates to, and the sharp edges. They exist so a cold start is
fast: provisioning an environment copies a resolved tree rather than running
composer against the network. The user-facing quick start is the README.

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

### A core so new that nothing installs into it

Resolving the tree is only half of it, and the other half was broken.

`composer create-project drupal/recommended-project:^12@beta` applies that
stability to **the one install** and does not write it into the `composer.json`
it creates. So the tree is left declaring composer's default, `stable`, while
its lock holds a beta core — and every later `composer require` in it is a
partial update that composer refuses, over *core*, not over whatever you were
installing:

```
$ composer require psr/log:^3
Your requirements could not be resolved to an installable set of packages.
  - drupal/core is fixed to 12.0.0-beta1 (lock file version) by a partial
    update but that version is rejected by your minimum-stability.
```

That is a stable package with nothing to do with any of this. Wiring the
module, installing the module's dev requirements, provisioning the check
toolchain and installing drush are all `composer require`, so **no environment
seeded from a pre-release tree could do anything at all**.

So a pre-release tree now records its own stability, immediately after the
resolve and before anything copies it:

```bash
composer config minimum-stability dev
composer config prefer-stable true
```

`dev` rather than the stability that was asked for, because `beta` fixes the
above and still cannot install a dev toolchain (below). `prefer-stable` is what
makes `dev` safe and is not optional: without it, composer takes a dev version
of everything it can. Measured on a real resolve of core `12.0.0-beta1` plus
drush `^14@dev` — **no already-locked package moved**, and exactly three ended
up at dev: drush and the two of its dependencies that have no release. Nothing
is written for a released core; that tree is correct as composer left it.

### drush, on a core that new

Even with the tree fixed, drush needs naming. Core `12.0.0-beta1` pins
`guzzlehttp/guzzle ^8.0.1` and `symfony/* ^8.1`, and:

| drush | | |
|---|---|---|
| `13.8.0` (newest release) | guzzle `^7.0` | no |
| `13.x-dev` | symfony `^6 \|\| ^7` | no |
| `14.x-dev` | guzzle `^7.8.2 \|\| ^8.0`, symfony `^7 \|\| ^8` | **yes** |

None of drush's tagged releases supports guzzle 8, so composer's own
suggestion — `--with-all-dependencies` — cannot help: no guzzle and no symfony
satisfies both sides at once. Only `14.x-dev` does, and it has no release.

```bash
upkeep base-artifacts:build --version=12 --stability=beta --drush='^14@dev'
```

Not `^13@dev`. That was this page's advice once and it was wrong: `13.x-dev`
clears guzzle, which is the constraint composer names first, and then fails on
symfony. Reading the one dependency in the error message is how that happened.

**A flag, for the same reason `--stability` is one.** An unpinned development
branch can break overnight, and choosing one on somebody's behalf inside the
build that produces the tree every later verdict is measured against is not a
trade the tool makes. The failure names the flag and prints a runnable line
instead, suppressed on a released core and once a constraint was given — the
same rule as the resolve hint above.

## 3. Rebuilding

```bash
upkeep base-artifacts:build --version=12 --stability=alpha --force
```

`--force` is required over an existing set; without it the build refuses with
*Base artifacts for core 12 already exist at … Re-run with `--force` to rebuild
deliberately.* There is no in-place update: a rebuild is a fresh
`composer create-project`, so it resolves against packagist as it stands that
day and you get the newest release your constraint admits.

**The existing set survives until the new one is complete.** A rebuild
resolves into a staging directory beside the live one —
`<cockpit>/base-artifacts/.building-d12-<hex>/12/` — and only when the tree,
dump, meta and marker are all written does the finished set move into place:
the outgoing one steps aside, the incoming one takes the name, the staging
directory goes. Both moves are renames within one directory, so the swap is
bounded by two atomic operations rather than by the minutes a resolve and a
site install take.

So a rebuild that fails — a network blip, a constraint that no longer resolves
— costs you the attempt and nothing else. The run says so explicitly (*The
existing base artifacts for core 12 are untouched at …*), because the other
reading of a failed rebuild is that everything is gone.

The staging directory is named with a leading dot, which keeps it out of
`ArtifactLayout::versionsOnDisk()` — the whole-number match behind
`base-artifacts:status`, prune, and the core inference that gives an
unregistered module its versions. A half-built tree must never read as a core
somebody can be offered. A staging directory left behind by a build that was
killed outright is removed by the next build for that core, which is the next
moment anyone is demonstrably not relying on it.

### A Drupal core security release

This is the commonest reason you will need to rebuild, and the symptom does not
look like a core problem at all.

Composer refuses to *load* a package affected by a published security advisory.
That is its `policy.advisories.block` setting, and it is on by default.
Everything upkeep puts into a seeded tree goes in with `composer require`: the
module itself, the module's own `require-dev`, the check toolchain, drush. Each
of those is a **partial** update, so `drupal/core-recommended` stays locked at
whatever version the base artifacts resolved to, and composer is not permitted
to move it.

So on the day an advisory lands against the core your artifacts were built on,
those installs stop resolving:

```
Problem 1
  - drupal/core-recommended is locked to version 11.4.6 and an update of this
    package was not requested.
  - drupal/core-recommended 11.4.6 requires drupal/core 11.4.6 -> found
    drupal/core[11.4.6] but these were not loaded, because they are affected by
    security advisories ("SA-CORE-2026-016", "SA-CORE-2026-013", ...).
```

Which install you see it on depends on the environment. A reused one already
has the module linked and the toolchain present, so the first composer call of
the run is the module's dev requirements and the failure reaches you as a check
that cannot find a class:

```
Could not install the module's own dev dependencies (drupal/paragraphs). Its
phpcs or phpstan configuration may reference a sniff or extension that is now
missing; the check will name it if so.
...
  120    Class Drupal\paragraphs\ParagraphInterface not found.
```

A fresh provision has more composer calls ahead of that one, so it fails
sooner, while wiring the module or installing the check toolchain.

**The fix is to rebuild that core's artifacts.**

```bash
upkeep base-artifacts:build --version=11 --force
```

A rebuild is a fresh `composer create-project`, so it resolves against
packagist as it stands today and takes the patched release. Every environment
seeded from the old set then trips the seed skew check in section 4 and
re-provisions itself on the next `check` or `review`. There is nothing else to
run.

Three things are worth knowing when you meet this.

The install failure is reported as a **warning rather than a refusal**, on
purpose: a package needed only for linting must not take down phpunit, the
install check and the smoke test. The consequence is that composer's own
output, which is the only thing that names the real cause, is behind `-v`.

The warning's suggested cause is a missing sniff or extension. That is the
common case and it is not this one, so a dev dependency that will not install
and that you cannot otherwise explain is worth checking against the current
core release before anything else. `upkeep base-artifacts:status` prints the
version each set resolved to and when it was built.

Composer will also offer you `policy.advisories.ignore-id` and
`policy.advisories.block: false`. Both work, and neither is the right trade
here. The advisory is accurate, a patched core exists, and a rebuild gets you
it; suppressing the check leaves every environment you own on a core with a
known vulnerability in order to avoid a command that takes a few minutes.

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

### The toolchain constraint *is* persisted, and the stability is not

An asymmetry worth the words, because they look like the same kind of thing.

`--drush` is recorded in `meta.yml` as `tool_require`, and every environment
seeded from the set installs drush that way without being told again. It has
to be: `check`, `review` and `dev` do not take the flag and should not, so a
set built with a constraint that provisioning then ignored would be a working
artifact set that no command could use. The key is omitted when empty and
optional on read, so every set built before it existed still reads — as
"whatever the engine defaults to", which is what those sets used.

`--stability` needs no such record in `meta.yml`, because it is *derivable*:
the file already holds the resolved `core_version`, and `StabilityOf` reads the
stability straight back out of `12.0.0-beta1`. Nothing about drush's constraint
can be derived from anything, so it is stored.

### `--stability` is not persisted, but the tree's own stability is

Two different things, and the distinction is easy to lose.

**The flag is a property of the invocation.** A `--force` rebuild that omits
`--stability` goes back to plain `^12`. While 12 is still pre-release that
fails — with the hint naming the flag, so it tells you rather than doing
something surprising — and once 12 is released, plain `^12` is what you wanted
anyway.

**The resolved tree's stability is a property of the tree**, and is written
into its `composer.json` as `minimum-stability` plus `prefer-stable` (§2). That
is not the flag being remembered; it is the tree declaring what it is, so that
`composer require` inside it and inside every environment copied from it works
at all. Without it none of them did.

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

**A rebuild is not free of risk, only of the big one.** The build stages beside
the live set and swaps at the end (§3), so a failed resolve costs the attempt
and nothing else. What remains is a window of two renames: a process killed
between them leaves the core with no version directory and both sets inside
the staging directory. Nothing is deleted, and the failure message names the
paths for exactly that reason — recovery is moving one directory by hand.

**A failed build never leaves a partial set.** An existing version directory
must always mean the last build completed, since that is what every downstream
environment seeds from. Both the failure path and the staleness collector
enforce it.

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
| Build orchestration, `--force` semantics, staged build and swap, partial-set cleanup | `BaseArtifact\BaseArtifactBuilder` |
| On-disk layout, canonical marker | `BaseArtifact\ArtifactLayout` |
| Completeness scan behind `base-artifacts:status` | `BaseArtifact\ArtifactScanner` |
| Seed-skew detection | `Adapter\EnvironmentMeta::staleReasons()` |
| Toolchain provisioning | `Adapter\DdevContribAdapter::ensureCheckToolchain()` |
| Prune protection | `Maintenance\Category`, `Maintenance\PruneSelector` |
