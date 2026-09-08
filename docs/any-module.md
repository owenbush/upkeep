# Plan — the registry as a watchlist, not a gate

*Status: steps 1 and 2 built. Written to be disagreed with before 1,508 tests
moved; §6 records what has landed and what has not.*

Drupal core is **out of scope here** and gets its own design: it needs a second
engine adapter, not a registry change. See §7.

---

## 1. What the registry is actually doing

`registry.yml` has two jobs, and they have been the same job since the tool
started:

> **Identity and configuration** — how to find a module on GitLab, and which
> cores to test it on.
>
> **Scope** — which modules the dashboard, `issues`, `patches` and `status`
> survey.

The second is the one a maintainer means when they curate a registry. The first
is a tax, and this week made most of it unnecessary.

| Field | Still irreplaceable? |
|---|---|
| `name` | No. It is the argument you type anyway. |
| `project` | No. `project/<name>` is drupal.org's convention, and **upkeep already assumes it**: `PruneExecutor` builds `new Module($item->module, 'project/' . $item->module, …)` for anything it finds outside the registry. |
| `core_versions` | Not for identity. `Drupal\CoreCompatibility` now reads `core_version_requirement` off the branch, and `ArtifactLayout::versionsOnDisk()` says which cores can actually be built for. |

So the gate `MrContextResolver::requireModule()` enforces —

```
Module "pathauto" is not registered in the cockpit. Registered modules: token.
```

— is protecting configuration the tool can now derive. What it is *really* good
at is catching a typo, and that is worth keeping (§5).

---

## 2. The model

> **The registry is a watchlist. Any module is workable.**

Two kinds of command, and the split is already latent in the code:

**Subject commands** take a module and act on it. They stop requiring
registration: `check`, `review`, `dev`, `exec`, `env:path`, `issue`,
`needs-work`, `patch:apply`, `patch:check`, `patch:promote`, `start`,
`publish`.

**Survey commands** iterate the watchlist and are unchanged: `dashboard`,
`issues`, `patches`, `notes`, `modules`, `status`, `prune`, the browser UI.

What this buys is not a feature so much as the removal of an obstacle. Today a
fresh cockpit cannot do anything until a registry entry exists. After:

```bash
upkeep init ~/cockpit
upkeep check pathauto 12          # works
upkeep modules:add                # only when you want to *watch* it
```

Upkeep stops being the tool for your twelve modules and becomes the tool for
Drupal contribution, which is what somebody reviewing one patch on somebody
else's module actually needs.

---

## 3. Resolving a module nobody registered

```
project       project/<name>
core version  --version, else the newest core with base artifacts on disk
```

### 3.1 Why on-disk artifacts and not the branch

The obvious answer is "ask the branch what it declares" — `CoreCompatibility`
is right there. It is the wrong default, for a sequencing reason and a
practical one.

Sequencing: `MrContextResolver::resolve()` picks the core *before* it fetches
the merge request, because the core decides which environment to talk to.
Deriving the core from the MR's target branch means fetching first and
reordering the resolver, which is a real change to the one path that must not
misclassify anything.

Practical: a branch declaring `^10.2 || ^11 || ^12` is no use if you have only
built base artifacts for 11. The set of cores you can actually test on is a
fact about your disk, and `ArtifactLayout::versionsOnDisk()` already answers
it.

So: **newest core with artifacts** — which means reversing what
`ArtifactLayout::versionsOnDisk()` returns, since it sorts ascending and
`selectCoreVersion()` takes `core_versions[0]`. Missing that on the first pass
made `env:path paragraphs` answer for Drupal 10 on a machine with 11 built.

When there are no artifacts at all, refuse with the command that fixes it:

```
No base artifacts to check against. Build one first:
  upkeep base-artifacts:build --version=11
```

### 3.2 But still check the branch, once it is known

Defaulting from disk means we can pick a core the branch does not support —
exactly the failure `docs/dashboard-row-model.md` §2.3 removed from the
dashboard. The answer is not to guess better but to say so: once the branch is
resolved, if it declares cores and the chosen one is not among them, **refuse
and name both sets**.

```
pathauto 8.x-1.x declares ^10.2 || ^11 || ^12, which does not include core 9.
Checking it there would produce a failure that says nothing about the module.
Pass --version=10, 11 or 12, or build base artifacts for one of them.
```

A registered module keeps its current behaviour — `core_versions[0]`, and the
"does not track core version" refusal — because that is a maintainer's
deliberate statement about what they support and outranks a guess.

---

## 4. What happens to each piece

| Piece | Fate |
|---|---|
| `MrContextResolver::requireModule()` | Becomes `resolveModule()`: registry entry when there is one, derived `Module` otherwise. The "not registered" refusal is replaced by a **typo** refusal (§5). |
| `MrContextResolver::selectCoreVersion()` | Two paths: registered (unchanged), derived (newest on disk, then the branch check of §3.2). |
| `UpkeepCommand::modules()` | Unchanged — it is the watchlist, and survey commands keep using it. |
| `PruneExecutor` | Stops reverse-mapping project names through the registry. `ProjectName::for()` is losslessly reversible (`upkeep-<name with _ as ->-d<core>`, and Drupal machine names contain no hyphens), so an ad-hoc environment can be identified from its own name. |
| Completion | Unchanged. Suggesting the watchlist is exactly right; a module you have never mentioned is not completable from anything local. |
| `ModuleRegistry` validation | Unchanged, and now guards only the watchlist. |
| Dashboard, `issues`, `patches`, UI | Unchanged. |
| `registry.yml` | Not renamed. It still means what it means; it just stops being a precondition. |

---

## 5. Risks

- **A typo becomes a 404.** `upkeep check pathuato 4` currently gets a list of
  what is registered; derived, it would clone `project/pathuato` and fail
  obscurely. The refusal has to survive in a new form: when the project cannot
  be resolved *and* a registered module is a near match, say so. Losing this is
  the single most likely way the change makes the tool worse.
- **Unbounded environments.** Nothing stops a maintainer provisioning
  environments for thirty modules they looked at once. `status --disk` and
  `prune` stop being housekeeping and become necessary; prune's ad-hoc
  discovery (§4) is part of this change, not a follow-up.
- ~~**A derived module has no `core_versions` to disagree with.**~~ Written
  down here, then not implemented — so the first real `--version=12` on an
  unregistered module answered *"Its registry entry tracks: 11, 10. Add it to
  core_versions in registry.yml"*, naming an entry that does not exist and
  listing a directory in reverse. Fixed: `Module::$watched` carries the
  provenance and the refusal is "no base artifacts for core 12, build one".
  Anything scripted against the old wording still breaks, which was the real
  content of this risk.
- **Verified against fixtures is not verified.** Every bug this week survived a
  green suite. This lands with a run against a module that is *not* in the
  registry, on a machine where it has never been provisioned, and the
  `full-check` workflow gains that case.

---

## 6. Sequence

Each step independently green and shippable.

1. ~~**Derived modules.**~~ **Done.** `Cockpit\ModuleResolution` plus
   `UpkeepCommand::resolveModule()`, wired into every subject command. A
   registry entry still wins; an unregistered name is derived; a name that
   cannot be a machine name is refused outright; and the near-miss suggestion
   moved to the *project failure*, which is the only point it is knowable.

   Two things came out differently from the plan. `issues` turned out to be a
   subject command, not a survey one — it takes a required module argument and
   reads that module's queue from drupal.org, so there was never a reason to
   require registration. And the two places that report an unresolvable
   project worded it differently; they now share one message, which was worth
   doing while the hint needed a home.

   Step 2's core selection came with it, because a derived module has no
   `core_versions` and is unusable without one. The disk is the source (§3.1);
   the branch-disagreement refusal of §3.2 is **not** built and is still to
   come.

   **Landed incomplete the first time.** `check <module> <mr>` and `review`
   resolve through `MrContextResolver::resolve()`, which kept its own
   `requireModule()` call — so the merge-request path, the one the change was
   most about, still refused unregistered modules while every other subject
   command accepted them. Nothing failed, because no test asked for an
   unregistered module on that path. The resolver now takes the on-disk
   versions like every other caller, and the missing test is the one that
   would have caught it.
2. ~~**The branch-disagreement refusal**~~ **Done**, on the merge-request
   path: `MrContextResolver` reads the target branch's info.yml and refuses a
   core it does not declare, naming the constraint and a core that is *both*
   declared and built here — suggesting one with no base artifacts would
   answer a refusal with another. Unreadable or unparseable stays silent.

   Not yet on the patch path, where the base branch comes from the issue's
   version field and the GitLab client is optional by design.
3. **Prune without the registry.** Reverse `ProjectName`, so ad-hoc
   environments are discoverable and collectable.
4. **Docs and the survey/subject split**, stated once in README and CLAUDE.md
   so the distinction is a rule rather than a pattern someone infers.
5. **Live verification.** An unregistered module, end to end, plus the
   `full-check` case.

### What is deliberately not in this

Renaming or restructuring `registry.yml`; changing the dashboard; touching the
fast-lane gate; and Drupal core.

---

## 7. Why core is not this change

The drupal.org half already works: same GitLab, same issue forks, same merge
requests, `project/drupal`. The environment half does not, and not by accident.

`ddev-drupal-contrib` builds a Drupal site **around** a module — a path
repository with `symlink: true` puts the checkout at
`web/<projects path>/<name>` and composer installs a site round it. Core is not
a module in that arrangement. Core *is* the site.

So core needs a second `EngineAdapterInterface` implementation, which is
precisely what the adapter boundary exists for — *"if a change needs engine
knowledge outside `src/Adapter/`, grow the adapter interface instead"* — plus:

- base artifacts meaning something else entirely (there is no site to install
  around the checkout);
- core's own test configuration, its own ruleset, and runtimes in a different
  league from a contrib module's nine seconds;
- a fast-lane gate whose premise does not transfer, since "Project Update Bot
  compatibility merge request" is a contrib concept.

There is a foundation to build on — `justafish/ddev-drupal-core-dev` exists and
was last pushed in August 2026 — so it is a second adapter over an existing
add-on rather than from nothing. It is a project, not a step, and bolting it
onto the contrib adapter is how the boundary gets breached.
