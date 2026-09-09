# Upkeep — Design Document

*A contrib maintainer's workbench for Drupal.*

Package: `owenbush/upkeep` (Composer). Binary: `upkeep`.

A tooling design for reducing the per-issue, per-module chore of maintaining
multiple Drupal contrib projects: reviewing merge requests, testing them in
isolation across Drupal core versions, merging the safe ones, and preparing
releases.

Status: built. This document captures the architecture, the reasoning behind
each decision, the command surface, the runtime lifecycles, a build sequence,
and the questions that were open before code was written — each now closed
with what actually shipped. Sections marked **As built** record where the
implementation went further than, or differs from, the original sketch.

---

## 1. Problem

A maintainer of ~10 contrib modules spends disproportionate effort on a
repetitive loop, per issue, per module:

fetch MR → spin up an environment → install the module and its dependencies →
run tests / static analysis → eyeball the result → merge → tag → cut a release.

Doing this once is fine. Doing it across 10 modules — and, during a core
transition, across every module at once — is where it becomes a grind. The
automated Drupal-version compatibility MRs are the clearest example: high
volume, homogeneous, and individually low-stakes, but collectively a large
manual burden.

The tool must not be a single-core-version device. Future Drupal versions will
bring the same wave of compatibility work, so "which core version" is a
first-class dimension of the design, not a hard-coded assumption.

## 2. Goals and non-goals

**Goals**

- One place to see the state of every open **issue** across all maintained
  modules — not only the ones carrying a merge request or a patch. *(Added
  after v1: the original scan was Needs Review + RTBC, the two statuses a
  contribution sits in, which on a real module is under half the open queue and
  excludes Active entirely. Measured on pathauto: 42 of 93.)*
- A way to **start** work on an issue nobody has contributed to yet, and to
  **publish** it as a merge request. *(Added after v1. The tool began at a
  contribution, so the half of the job where a maintainer writes the fix
  happened outside it — a manual clone, branch and push. Everything downstream
  of a merge request was already good; the entry point was simply too late.)*
- One place to see the state of every open MR across all maintained modules.
- Fast, isolated testing of any MR — any issue, any module, any core version.
- Automatic handling up to *merge* for trusted, homogeneous compatibility MRs
  that pass all checks.
- A way to drop any MR onto a running site to click through manually.
- Draft release notes to remove the busywork of assembling a changelog.
- Distributable to the community, using each ecosystem's native mechanisms.

**Non-goals**

- No automatic release cutting. Releases stay manual — there may be further
  changes to batch in before tagging. The tool drafts notes; the human tags.
- Not a new test rig. The per-project testing environment already exists and is
  good; this design reuses it rather than reinventing it.
- Not a hosted service. This is local, maintainer-side tooling. A browser UI
  existed briefly and was removed: nothing forced it to stay in step with the
  core it rendered, and it fell behind twice before anyone noticed.
- **No issue *creation*, and no status changes.** drupal.org's api-d7 is
  read-only — a POST answers 403 — so anything that writes to an issue queue
  can only be a pre-filled browser form. `issue` and `needs-work` already hand
  off that way, and that is the ceiling until drupal.org ships a write API. The
  GitLab side is different and genuinely writable, which is why `publish` can
  open a merge request but nothing can move an issue to RTBC.

## 3. Guiding principle: isolate each axis with the cheapest mechanism that works

The core insight that shapes everything below: "isolation" is not one thing. A
module check needs isolation on three independent axes, and each has a different
cheapest-correct mechanism.

- **Database / site state** → swap via snapshot (fixtures). Instant.
- **Codebase + Composer dependency tree + PHP version** → separate on-disk
  project per (module × core-version). Isolated by construction.
- **Container services (DB, PHP, web)** → shared images and caches, disposable
  and recreated per project. Cheap.

Conflating these is what makes the two obvious approaches unsatisfying. A single
shared site (many modules in one codebase) is cheap on containers but destroys
Composer/PHP isolation — fatal for cross-version testing, where D11 and D12 pull
different dependency trees and want different PHP versions. A full separate
environment per module is perfectly isolated but feels heavyweight if you assume
the whole container set is the unit of cost.

The resolution: don't share the *codebase*, share the *expensive caches*. Keep
per-(module × core-version) projects for true isolation, and lean on the fact
that container images and the Composer package cache are already shared globally
across projects. You get correctness on every axis and pay far less overhead
than the naive "N full environments" framing implies.

## 4. Architecture overview

Four layers.

**Engine — the per-project rig (reused).**
Each (module × core-version) combination is a project using the existing
`ddev-drupal-contrib` add-on. It provides the correct core/PHP setup, the
module-as-center-of-universe layout, and CI-aligned check commands
(`ddev phpunit`, `ddev phpstan`, `ddev phpcs`) that mirror the Drupal
Association's GitLab CI. This is deliberately not rebuilt: reusing it gives
better CI fidelity than any hand-rolled rig, and it is mature and actively
maintained.

**Driver — the orchestrator (new).**
A PHP command-line application, distributed as a Composer package. It is *not* a
ddev add-on, because its job is inherently cross-project and external: it talks
to the Drupal.org GitLab API and shells out to `ddev`. It enumerates modules and
their MRs, drives the engine to test them, aggregates results into a dashboard,
performs fast-lane merges, and drafts release notes.

**Supporting — fixtures and maintenance (new).**
Environment-internal commands that run *inside* a project: load/create DB
fixtures, and prune a project's disposable state. Because these operate on the
container/DB, they are `ddev` commands, shipped initially as a small companion
ddev add-on (with the option to upstream into `ddev-drupal-contrib` later).

**Cockpit — the control project (new).**
A designated directory that houses the orchestrator's configuration (the module
registry), the per-core-version base artifacts used for fast cold starts, and
serves as the home you run the tool from.

### The adapter boundary

The single most important structural decision. The orchestrator does not call
`ddev-drupal-contrib`'s specifics directly. It talks to a thin internal
interface — roughly:

- `ensure_env(module, core_version)`
- `apply_mr(ref)`
- `load_fixture(name)`
- `run_checks()`
- `serve()`
- `teardown()`

`ddev-drupal-contrib` is *one implementation* behind that interface. This means
the decision to reuse it is not load-bearing: if it changes, or a different rig
is preferable later, only the adapter is swapped, not the orchestrator. It also
keeps the fixture commands separable — the orchestrator calls `load_fixture`,
not a hard-coded ddev command name.

**As built: the boundary is a composition root, not a convention.** The first
implementation left five command classes constructing `DdevContribAdapter`
themselves. The textual guard (`grep -r "ddev" src/ --exclude-dir=Adapter`)
passed anyway — only because the class name capitalises the D — so the
invariant was nominally satisfied and substantively breached: a swap of engine
would have meant editing five commands. The shipped structure closes that:

- Commands depend on `Adapter\EngineAdapterFactory`, injected through the
  constructor. The adapter cannot simply be built once at startup because it
  needs the cockpit, the projects root and the log sinks the *invocation*
  resolved; the factory is that seam.
- `bin/upkeep` is the composition root and the only file outside
  `src/Adapter/` permitted to name a concrete engine. It builds one
  `DdevContribAdapterFactory` and hands it to the commands that need it.
- The guard is now case-insensitive (`grep -ri`), which is what makes it
  meaningful rather than merely satisfiable.

The secondary benefit is testability: a command is constructible with a fake
factory returning a fake engine, so the whole orchestrator is exercised
without docker.

### Support layers around the boundary

Three concerns turned out to belong in one place each rather than at every
call site, and are namespaced accordingly:

- **`src/Filesystem/`** — one write path. `FileWriter` writes to a temp file in
  the target's own directory and `rename()`s it into place, so a reader sees
  either the complete old file or the complete new one and the target never
  stops existing (which matters for files that double as completion markers,
  such as `.upkeep-env.yml`). Every step is checked, so a write that does not
  land raises instead of returning `false` into a discarded value while the
  caller prints success. `PathGuard` canonicalises paths and decides
  containment on the resolved path, so a `..` segment or a symlink pointing
  out of the root is caught rather than pattern-matched around.
- **`src/Security/`** — `SecretRedactor` filters credential material out of
  child-process output before it reaches a log, an exception message, or a
  cached result; `CredentialEnvironment` removes the PAT from every child
  environment, since the process layer otherwise copies the entire parent
  environment into every subprocess this tool spawns.
- **File modes** — caches carrying token-scoped remote data or raw check
  output (`<cockpit>/results/`, `<cockpit>/cache/dashboard/`) are `0600` files
  in `0700` directories. `file_put_contents` cannot take a mode and the umask
  default is wrong for them, which is a second reason the write path is
  centralised.

### The API failure taxonomy

`GitlabClient` methods do not throw for HTTP-level outcomes; they return a
`Gitlab\ApiFailure`. The hierarchy is sealed — every subtype is final and
enumerated — so a `match` over it needs no `default` arm, and the conditions
are exhaustive and mutually exclusive: `Unauthorized` (401), `EndpointClosed`
(403), `NotFound` (404), `RateLimited` (429), `RequestRejected` (any other
status >= 400), `MalformedResponse` (a non-error status whose body is not
usable JSON), `TransportError` (no usable response at all), `ResourceMissing`
(the call succeeded but the asked-for item is not in the result).

Two distinctions earn their keep on this instance. **401 is not 403**: on a
block-by-default API, 403 means "that endpoint is not open to PATs, use the
browser" — advice that is actively misleading for an expired token, where the
only useful action is to re-check the credential. And **`ResourceMissing` is
not `NotFound`**: asking for the MRs merged since a tag that a HTTP 200 tag
list simply does not contain is a domain miss, so the message never claims a
404 that never happened. Each subtype also answers `isTransient()`, which is what
lets the client memoize stable outcomes for the rest of a run without
poisoning a resource because of one network blip. No subtype ever carries
credential material.

## 5. Why not the alternatives

Recording the roads not taken, so the choices are legible.

- **`ddev-drupal-suite` (one site, many modules).** Its model shares one
  codebase across modules, which breaks Composer and PHP-version isolation. It
  was a useful design foil but is the wrong fit for cross-version testing.
- **One project, many docroots via nginx.** Serves multiple roots from one
  project, but shares one PHP version and one Composer root — isolation only of
  the served directory, not the dependency/runtime environment. Fine for
  same-core-version work; leaks exactly where cross-version testing needs it.
- **Re-pointing one container set at swappable trees.** ddev's `composer_root`
  is config-plus-restart, not a fast per-check switch, and driving Composer
  against arbitrary subtrees is not cleanly supported. Fighting the grain of the
  tool for a fragile result.
- **A proxy in front of separate projects.** This is actually already true —
  ddev runs a shared router in front of every project, so each (module ×
  version) project is reachable at its own `*.ddev.site` URL with no extra
  work. "Switching" is a URL lookup, not a reconfiguration. An extra proxy
  container in the control project could add a single unified hostname on top,
  but is optional.

The conclusion the research pushed toward: separate projects are ddev's intended
unit of isolation. Accept N projects, and claw back the cost through shared
caches and base artifacts rather than through shared codebases.

## 6. Fast cold starts

> Lifecycle detail — building, rebuilding, pre-release core majors and what a
> rebuild propagates to — is [base-artifacts.md](base-artifacts.md). This
> section is the rationale for having base artifacts at all.

Running N projects is accepted. The lever for making a *new* (module ×
core-version) project spin up quickly is caching the stateful layers, since the
containers themselves are already cheap (shared images, create-from-image in
seconds).

**Already shared/cached for free across projects:**

- Docker images — pulled once, shared by every project.
- Composer package cache — global; the first resolve of a core version's
  dependency graph downloads packages, every later tree of that version reuses
  them. This is the big win for the version matrix.
- Docker layer cache — any real image building is layer-cached.

**Built once per core version (the engineered part):**

- **Base vendor tree** — a resolved `drupal/recommended-project` for each core
  version. Identical across all modules until the module itself is required. A
  new project is seeded by copying this base tree (fast local copy), then
  `composer require`-ing just the one module on top — resolving the ~5% that is
  the module, not the ~95% that is Drupal.
- **Clean-install DB snapshot** — a freshly installed, module-free site per core
  version, stored as a fixture/snapshot. New projects restore it in seconds
  instead of running a full site install.

**Cold-start recipe for a fresh (module × core-version) project:**

1. Create project, start containers → seconds (shared images).
2. Seed codebase from the cached base vendor tree for that core version → fast
   local copy.
3. `composer require` the one module → small incremental resolve, packages
   already cached.
4. Restore the clean-base DB snapshot for that core version → seconds.
5. Apply the MR, enable the module, run checks.

Every step rides an existing cache or a per-core-version base artifact built
once. The only irreducible per-check cost is the module's incremental require
and the MR checkout — which is the actual thing under test.

Note: containers cannot be meaningfully "snapshotted" in a VM sense in ddev's
model — they are disposable and recreated from images plus volumes. All caching
effort correctly targets the two stateful layers: DB (snapshots) and
vendor/codebase (base-tree copy).

## 7. Fixtures

Fixtures are the mechanism for fast, realistic, isolated DB state. Two formats,
each for a different job.

- **Snapshots** — the fast local working mechanism. Restore a known state in
  seconds; discard after a check. Tied to the exact DB engine/version, so they
  are a local cache, not a portable artifact.
- **Dumps (gzipped SQL)** — the portable, version-controllable source of truth.
  Engine-agnostic, used to *build* a snapshot on any machine or DB version.
  Slower to restore, but shareable and committable.

**Model:** a fixture is a named SQL dump; the tool materializes it into a fast
snapshot on first use, then restores the snapshot for subsequent checks.
Portability and speed both, and the cross-version DB-engine skew problem is
handled because the portable dump can always rebuild a snapshot against whatever
engine the current core version uses.

**Scope — both shared and per-module:**

- **Shared library** — generic states (`minimal`, `with-content`,
  `multilingual`) living with the workbench, reused across modules.
- **Per-module fixtures** — committed to the module repo under a conventional
  `tests/fixtures/` path, travelling to everyone who clones it. Resolution is
  per-module-first, falling back to the shared library.

Committing fixtures to module repos turns them into reusable test infrastructure
that co-maintainers and contributors inherit — a genuine contribution to the
module, not just a personal convenience.

**Cautions for committed fixtures (public, licensed artifacts):**

- **Sanitization.** A DB dump can carry PII, emails, hashed passwords, session
  data, real content. Anything committed must be synthetic or sanitized. The
  create flow should default to running `drush sql:sanitize` when the
  destination is a module repo.
- **Size / repo hygiene.** Gzipped dumps are binary blobs; Git keeps every
  version forever. Keep fixtures deliberately lean — enough to exercise the code
  path, not a production copy — and warn on large ones.

Establishing the `tests/fixtures/` convention as a documented standard is itself
a contribution: any maintainer with the tooling can check out any
convention-following module and immediately load its fixtures.

## 8. The general case vs. the fast lane

Two tiers of behavior, so the tool is a year-round workbench rather than a
one-off migration device.

**General case (any issue, any MR, any core version).** For any MR pointed at,
the tool fetches the ref, drops it onto the rig, runs the fast checks, pulls
GitLab CI status, and can put it on a live site to click through. It assumes
nothing about *why* the MR exists — bugfix, feature, security, compatibility all
work the same. The core version is read from the project, never hard-coded.

**Fast lane (automatable classes of MR).** On top sits an optional auto-merge
gate that fires only for MRs matching a recognizable, safe pattern: currently,
trusted automated compatibility MRs from the project update bot, with green
GitLab CI and green local checks. The gate is deliberately conservative;
anything not matching routes to manual review. The pattern is parameterized by
the current target core version, so it carries forward to future versions
unchanged, and other trusted classes could be added later.

Checks that feed the gate: GitLab CI status (authoritative, via API) plus local
PHPStan, deprecation/upgrade-status report, module install/enable, and a basic
functional smoke — the fast, high-signal local checks, trusting CI for the full
test matrix.

### Critical constraint: the Drupal Association PAT/automation policy

This is the single biggest external constraint on the design and it reshapes
the fast lane. The DA's policy for Personal Access Tokens on git.drupalcode.org
draws a hard line:

- A PAT *may* be used to "perform any individual action that a user could
  already perform with a regular authenticated session." Interactive,
  human-triggered, one-action-at-a-time use is within this envelope.
- A PAT *may not* be used "to create automation/bots without prior approval from
  the Drupal Association engineering team."
- The API is block-by-default. Specific endpoints are opened for PAT use on
  request, by opening an issue in the Infrastructure project tagged `gitlab api`.
- A 403 on this instance often reflects a closed endpoint or project config, not
  just token scope. Build for graceful degradation, not assumed full access.

Consequence: **the fast lane is human-triggered (decided).**

The default and only v1 behavior is human-triggered, one-action-at-a-time
merging:

- **Human-triggered mode (v1).** The dashboard surfaces READY-AUTO rows; the
  maintainer reviews and presses a key; the tool performs *that merge, as a
  single action the user could have done in the browser*. This keeps the
  policy's "individual action a user could perform" framing while removing the
  per-MR UI clicking. It captures nearly all the time savings, requires no DA
  sign-off, and aligns with the maintainer's preference that merging be
  acknowledged and releases stay manual. Beyond compliance, a moment of human
  acknowledgment before merging into projects the maintainer is responsible for
  is the right default: cheap insurance against the compat MR that passes every
  check but is subtly wrong.
- **Unattended mode (future, out of scope for v1).** True batch
  auto-merge-on-a-schedule is "automation/bots" under the policy and would
  require explicit DA engineering approval (and possibly endpoint-opening
  requests). Noted as a possible future extension, not designed around now. v1
  does not build a gated second tier.

Auth uses the same PATs maintainers already generate for HTTPS Git access
(Drupal.org account → "Git access"), sent via the `PRIVATE-TOKEN` header. The
tool must be rate-limit-friendly and well-behaved regardless of tier.

## 9. Command surface

Illustrative, not final. `upkeep` is the orchestrator (Composer package
`owenbush/upkeep`); `ddev <verb>` are the companion add-on's in-project commands.

**Orchestrator (run from the control project):**

```
upkeep dashboard              # all MRs, all modules, all core versions
upkeep check <module> <mr>    # test one MR: checks + results
upkeep check <module> <mr> --version=12
upkeep check <module> <mr> --fixture=with-content
upkeep review <module> <mr>   # drop MR onto a live site to click through
upkeep merge --fast-lane      # merge the ready class, one approved action at a time
upkeep notes <module>         # draft release notes since last tag
```

Dashboard sketch:

```
MODULE            MR   CORE   TITLE                     CI    LOCAL  STATUS
my_module         12   12     Automated D12 compat      ok    ok     READY-AUTO
another_module    8    12     Automated D12 compat      ok    fail   REVIEW (phpstan)
third_module      15   11     Fix random test failure   fail  -      BLOCKED (CI)
gadget            3    12     Automated D12 compat       ok    ok     READY-AUTO
```

**Fixtures (in-project ddev commands):**

```
ddev upkeep-fixture-create <name>   # export current DB -> portable dump (sanitized if module repo)
ddev upkeep-fixture-load <name>     # materialize dump -> snapshot -> restore
ddev upkeep-fixture-list
```

**Maintenance / prune (disk control):**

```
upkeep status --disk               # disk per module/version/category
upkeep prune --trees --older-than=30d    # drop disposable vendor/codebase trees
upkeep prune --snapshots           # keep-latest-N per module
upkeep prune --projects            # tear down stale project volumes
upkeep prune --all --older-than=30d      # orchestrated sweep
```

Maintenance principles: **dry-run by default** on anything destructive (show the
reclaim, require confirmation), and **protect canonical artifacts** — committed
`tests/fixtures/`, "keep" snapshots, and the per-core-version base artifacts are
never auto-pruned. Only disposable derived state (per-module vendor trees,
materialized snapshots, stopped project volumes) is aggressively reclaimable,
which is safe precisely because it is cheap to regenerate.

## 10. Disk and the maintenance lifecycle

Disk grows with (modules × core-versions tested). Ten modules across two core
versions is twenty vendor trees plus their volumes and snapshots. This is the
accepted cost of honest cross-version isolation — a green check meaning green
*for that exact core version*, with no cross-contamination, is the product.

The trade is favorable because the disposable layers are cheap to regenerate:
vendor trees rebuild from the shared Composer cache and the base tree; snapshots
rebuild from dumps. So pruning can be aggressive on disposable state while base
artifacts and committed fixtures stay protected. `status --disk` surfaces where
space is going before anything is reclaimed.

Lifecycle in one line: build (module × version) trees lazily on first check,
reuse them warm via shared caches, snapshot-swap DB state per check, prune the
disposable layers on a threshold — canonical and base artifacts always
preserved.

**As built: age means last use, not creation.** Each environment carries a
`.upkeep-env.yml` recording what it was provisioned for, what it was seeded
from, and both `created_at` and `last_used_at`. `prune --older-than` filters on
`last_used_at`, which is stamped at provision and re-stamped on every reuse. An
age measured from creation would make a warm, daily-driven environment a
deletion candidate purely for being old, which is exactly backwards for a cache
whose value is that it is warm. The key is optional, so environments written
before it existed still parse and fall back to `created_at` until next reused.
Because the same file doubles as the provisioning completion marker, every
re-stamp goes through the atomic write path — a target that momentarily stopped
existing would read as an interrupted provision and force a multi-gigabyte
rebuild.

## 11. Packaging and distribution

- **Orchestrator** → `upkeep`, a PHP CLI (Symfony Console), distributed as the
  Composer package `owenbush/upkeep` (`composer global require owenbush/upkeep`),
  optionally also a phar later. Chosen because the audience is Drupal contrib
  maintainers who already live in PHP/Composer; this is how Drush, PHPStan,
  PHP_CodeSniffer, and Rector ship. It keeps the whole project in one ecosystem
  and asks the user to adopt no foreign toolchain.
- **Fixtures + maintenance ddev commands** → a companion ddev add-on
  (`ddev add-on get ...`), the native mechanism for in-project commands. Start
  as a companion for fast iteration; propose upstream into `ddev-drupal-contrib`
  once proven.
- **Engine** → the existing `ddev-drupal-contrib`, unchanged.

The result is a stack where every piece feels native to a Drupal maintainer: the
orchestrator ships like Drush, the extension ships like a ddev add-on, and
nothing requires a Node or Python toolchain.

**On the name.** "Upkeep" says exactly what the tool does — keeping a set of
modules maintained — is short and comfortable to type as a command, and is
gender-neutral. It was chosen after checking the Drupal namespace, which is
dense with brands: `druid`, `steward`, and `forge` were each ruled out for
colliding with existing Drupal agencies or projects, and `bench` was set aside
for its proximity to the established Workbench module suite. "Upkeep" appears in
the Drupal space only as the ordinary English word, with no module, project,
suite, or agency collision. *Verified (2026-07-29):* `owenbush/upkeep` and
`owenbush/ddev-upkeep` both return HTTP 404 on Packagist (names free). A
Packagist search for "upkeep" returned only two unrelated packages (`aguva/ussd`,
`areia-lab/laravel-maintainer`), neither of which ships any vendor binary, so no
existing package claims an `upkeep` bin. A GitHub repo search for `ddev-upkeep`
returned zero results, and the official ddev add-on registry (`ddev add-on
list`) contains no add-on named "upkeep". All three names — `owenbush/upkeep`,
the `upkeep` binary, and `owenbush/ddev-upkeep` — are clear to use.

## 11a. Prior art and references

Related projects in the same arena. None is a dependency of this design; they
are reference implementations, foils, and signals of who is working nearby.

- **`ddev-drupal-contrib`** — the engine. Reused wholesale (see sections 4 and
  5). Confirmed healthy and officially blessed: ~136 stars, actively maintained,
  and the subject of a DDEV issue to "make the add-on official... align local
  development with what the DA uses in its CI." It already treats core version as
  a first-class dimension (`TEST_DRUPAL_CORE` env var; nightly tests against all
  supported cores), which validates the per-(module × core-version) model. Its
  `symlink-project` + `poser` commands are its answer to getting a module's
  working copy into the site without Composer clobbering it (a temporary
  `composer.contrib.json` keeps the module's own `composer.json` untouched).

- **`joachim-n/drupal-project-contrib-development`** — prior art, not a
  dependency. It is a Composer *plugin* aimed at the site-builder workflow
  (patch a contrib dependency of a production site against its installed
  version), which is a different workflow from ours (evaluate incoming MRs
  against clean core). It cannot be reused as a library — its commands are
  human-typed Composer subcommands operating on the host project's
  `composer.json`, not a programmatic API. Its value is two borrowable insights:
    1. **Path repositories over `--prefer-source`.** When bringing a module's
       git clone into a Composer project, use a Composer *path repository* so
       Composer treats the checked-out code as an external working copy it will
       not delete or reset. `--prefer-source` makes Composer consider itself the
       owner and it may remove branches or the whole checkout. When building the
       adapter's `apply_mr`, use the path-repository approach (or the engine's
       equivalent `symlink-project`/`poser` mechanism, which is what we will use
       by default) — never `--prefer-source`.
    2. **Native-base vs. backport boundary.** An MR's diff is cut against the
       module's development branch. Our model tests each MR against its *native
       base branch* on clean core, so MR application is a straightforward branch
       checkout and the diff always applies. Testing an MR against a *different*
       branch than it was cut against (backport / against-stable scenarios) is a
       distinct, harder case that would need a diff-from-installed-version
       approach like Joachim's `apply-patch-from-branch`. This design scopes to
       native-base testing; backport testing is explicitly out of scope for the
       first version.
  Joachim is also active in the `ddev-drupal-contrib` queue, so he is a natural
  person to solicit feedback from when proposing fixtures upstream or floating
  the orchestrator.

- **`ddev-drupal-suite`** — design foil only. Its one-site-many-modules model
  demonstrated the shared-codebase approach this design rejects (breaks
  Composer/PHP-version isolation). Early and small; referenced for contrast, not
  used.

## 12. Build sequence

Smallest independent value first.

1. **Fixture + maintenance ddev commands** (companion add-on). Self-contained,
   independently useful, testable against one real module immediately.
2. **Base-artifact caching** — per-core-version base vendor tree and clean
   install snapshot, plus the seed-a-new-project logic. Prerequisite for fast
   cold starts; the orchestrator depends on it.
3. **Orchestrator core** — GitLab API client + environment driver behind the
   adapter interface + the dashboard. The bulk of the novel work.
4. **Fast-lane merge + release notes** — layered on once dashboard and driving
   work.
5. **Control project + packaging** — tie together; finalize the invocation and
   distribution.

## 13. Open questions to verify before building

- **Fixtures: upstream vs. companion.** *Resolved (2026-07-29): companion-first
  confirmed.* The five open enhancement issues were each read in full (body and
  comments); none overlaps the planned fixture or MR-orchestration work. #164 is
  a `ddev phpstan` bug (wrong `phpstan.neon` symlink path when run before
  `symlink-project`). #163 asks for `-h`/`--help` support on `symlink-project`.
  #157 asks to pin `gitlab_templates` script versions instead of always pulling
  latest (toolchain churn — reinforces the next bullet, no fixture overlap).
  #172 is `ddev poser` failing on Composer security advisories for older cores;
  maintainers lean against disabling security blocking by default (relevant only
  as environment-build friction the adapter may need to absorb). #170 asks for a
  less opinionated multi-module variant; the maintainer explicitly advised
  building such functionality "independent of this one, for now" with a possible
  later merger — a direct endorsement of the companion-add-on-first approach.
- **Toolchain churn underneath the engine.** *Resolved (2026-07-30): shipped
  as designed.* The queue shows the engine and wider toolchain move (Composer
  2.9 broke tests; a `gitlab_templates` upstream change broke
  `symlink-project` until the add-on was upgraded). The shipped answer is the
  adapter boundary: every engine specific lives in `src/Adapter/` behind
  `EngineAdapterInterface`, reached through an injected `EngineAdapterFactory`
  and chosen once in the `bin/upkeep` composition root (guarded —
  `grep -ri "ddev" src/ --exclude-dir=Adapter` returns nothing; the `-i`
  matters, see section 4), so such churn is absorbed in one adapter, not
  across the orchestrator. The engine add-on is pinned at
  ddev-drupal-contrib **1.1.5**
  (`Adapter\EngineAddOn::VERSION`); upgrading the pin is a deliberate
  adapter-maintenance event, never an ambient `latest`.
- **MR application mechanics.** *Resolved (2026-07-30): shipped as a Composer
  path repository.* `Adapter\ModuleWiring` prepends a path repository for the
  module working copy to the environment's `composer.json` and syncs the
  composer pin to the MR branch — Composer therefore never clobbers the
  checked-out MR branch, and `--prefer-source` is never used. Environments are
  seeded from the native base artifact tree only (the base-vendor-copy bullet
  below); the engine's `symlink-project`/`poser` route was not needed for this
  path. See section 11a.
- **ddev reclamation semantics.** *Verified empirically (2026-07-29, ddev
  v1.25.1, Docker 29.5.2/colima, macOS arm64)* on three identical throwaway
  projects, each with a 139.3MB mariadb volume, a 0B snapshots volume, a 123kB
  mutagen volume, per-project `-built` webserver/dbserver images, and a 252K
  codebase. Findings: `ddev delete --omit-snapshot` removes the project's
  containers, all three named volumes (`<name>-mariadb`,
  `ddev-<name>-snapshots`, `<name>_project_mutagen`), the per-project `-built`
  images, and the `ddev list` registration — the codebase and `.ddev/` config
  are untouched, and shared base images (`ddev/ddev-webserver`,
  `ddev/ddev-dbserver-*`) remain (reclaim those separately via
  `ddev delete images` / `docker rmi`). `ddev stop --remove-data
  --omit-snapshot` is *identical* to `ddev delete` in v1.25.1 — it printed
  "Project ... was deleted", removed the same volumes/images/containers, and
  removed the registration too. Plain `rm -rf <tree>` reclaims only the 252K
  codebase and leaves everything else orphaned: containers *kept running*,
  all three volumes retained, `-built` images retained, and a stale `ddev list`
  row ("project directory missing"). The orphan is fully recoverable:
  `ddev delete --omit-snapshot --yes <name>` works after the tree is gone and
  reclaimed all residue (docker `system df` returned exactly to baseline:
  volumes 2.117GB → 1.699GB). Prune commands should therefore always
  `ddev delete` before (or instead of) removing trees, and never rely on
  `rm -rf` alone.
- **Shared Composer cache across the version matrix.** *Verified empirically
  (2026-07-29, Composer 2.8.12).* D12 is not yet released, so the two most
  recent installable majors — D10 (`drupal/core` 10.6.14) and D11 (11.4.4) —
  were used; the point (two majors with differing dependency trees sharing one
  cache) holds. With a single shared cache dir, a cold D11
  `create-project` made 68 downloads (69 packages); a second D11 resolve made
  **0** downloads (every dist served from cache, confirmed at `-vvv`:
  "Loading drupal/core (11.4.4) from cache"); a back-to-back D10 resolve
  installed 60 packages with only 35 downloads — 25 dists at versions shared
  with D11 were reused. Different versions of the same package coexist safely:
  the cache keys dists by content hash
  (`files/drupal/core/4f00ac92….zip` and `…a93d428….zip` side by side), 104
  zips / 77M total, no corruption, all installs exit 0 and pass
  `composer install --dry-run` clean. Note: on macOS the global cache is
  `~/Library/Caches/composer` (not `~/.cache/composer`); use
  `composer config -g cache-dir` / `COMPOSER_CACHE_DIR` rather than a
  hard-coded path.
- **Base-vendor-copy seeding.** *Verified empirically (2026-07-29): identical.*
  A project built by `cp -a` of a pristine resolved
  `drupal/recommended-project:^11` tree followed by
  `composer require drupal/token` matches a from-scratch
  `create-project` + same require control in every checked dimension:
  `composer validate` passes; `composer install --dry-run` reports "Nothing to
  install, update or remove"; sorted `composer show` package sets identical
  (69 packages); `vendor/composer/installed.json` **byte-identical** between
  the two trees; `composer.json` identical and `composer.lock` package sets
  and `content-hash` equal; scaffold files (`web/index.php`, `web/.htaccess`,
  `web/robots.txt`, `web/autoload.php`, `default.settings.php`) present in
  both; module lands at `web/modules/contrib/token` in both; and
  `require 'vendor/autoload.php'` resolves core classes in both. Task 8 can
  seed environments by copying the base artifact — no full-resolve fallback
  needed for this path.
- **GitLab API specifics on git.drupalcode.org.** *Verified empirically
  (2026-07-29)* with the maintainer's Git-access PAT (`PRIVATE-TOKEN` header)
  against `project/conditions_helper` (id 181714), `project/field_visibility_conditions`
  (id 176919), and `project/token`. Endpoint access matrix (all GET, REST v4):

  | Endpoint | HTTP | Open? |
  | --- | --- | --- |
  | `GET /projects/project%2F<module>` | 200 | open |
  | `GET /projects/<p>/merge_requests?state=opened&scope=all` (also `state=all`, `author_username=`) | 200 | open |
  | `GET /projects/<p>/merge_requests/<iid>` (includes `head_pipeline`, `detailed_merge_status`) | 200 | open |
  | `GET /projects/<p>/merge_requests/<iid>/pipelines` | 200 | open |
  | `GET /projects/<p>/pipelines` | 200 | open |
  | `GET /projects/<p>/repository/tags` | 200 | open |
  | `PUT /projects/<p>/merge_requests/<iid>/merge` | — | **untested pending a safe target** |

  No 403s were hit on any read path — the reads the orchestrator needs are all
  open to a plain PAT. The merge endpoint was deliberately not exercised: no
  sacrificial MR was designated, and a 2xx would merge something real. Fallback
  per the plan: task 14 ships the degraded browser-link path as default until a
  maintainer designates a safe ready-to-merge compat MR and the endpoint is
  probed interactively (DA policy permits interactive single-actions). *That
  degraded path shipped: `merge --fast-lane` treats a closed merge endpoint
  (`EndpointClosed`) as the documented fallback — it prints the exact browser
  merge URL for the approved MR and records the row as handled manually.*
- **Bot-MR identification.** *Resolved (2026-07-29)* from three real bot MRs:
  `conditions_helper` !1 (merged), `field_visibility_conditions` !2 (open,
  draft), `token` !130 (open). The pattern is exact and consistent:
  - Author username: `Project-Update-Bot` (user id `66574`, display name
    "project update bot") — identical across all samples.
  - Source branch: always literally `project-update-bot-only`.
  - Title: `Automated Project Update Bot fixes`, optionally prefixed
    `Draft: ` while the bot considers it unready.
  - Description: `Relates to #<issue-nid>. This merge request was automatically
    created by the Project Update Bot. It contains the changes from run
    <run-id>.`

  Gate classification should key on author username (or id 66574) AND source
  branch `project-update-bot-only`; treat a `Draft: `-prefixed title (or
  `detailed_merge_status: draft_status`) as not fast-lane eligible.

## 14. Summary

Reuse `ddev-drupal-contrib` as the engine. Build a PHP/Composer-packaged
orchestrator as the driver, talking to the Drupal.org GitLab API and driving the
engine through a thin, swappable adapter interface. Add fixtures and maintenance
as a companion ddev add-on. Isolate each axis with its cheapest-correct
mechanism — snapshots for DB state, separate projects for codebase/Composer/PHP,
shared caches for containers — and make cold starts fast with per-core-version
base artifacts. Keep disk in check with a dry-run-by-default prune surface that
protects canonical artifacts. Automate up to merge for trusted compatibility
MRs; keep releases manual with drafted notes. The fast lane is human-triggered —
the tool tests and surfaces ready MRs; the maintainer approves each merge as a
single action — which respects the DA automation policy and is the right default
for merging into projects one is responsible for. Design for the version matrix
from the start, so the tool stays useful for every future Drupal transition, not
just the current one.
