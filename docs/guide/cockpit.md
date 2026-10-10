# Set up a cockpit

The directory that holds your registry, environments, base artifacts, fixtures and cached results.

## Cockpit setup

**You do not have to register a module to work on it.** `check`, `review`,
`start`, `publish`, `issue`, `issues`, `patch:*`, `dev`, `exec` and `env:path`
take any module machine name: the project is `project/<name>` by drupal.org
convention, and the core version comes from the base artifacts you have built.
So reviewing one patch on somebody else's module needs no setup:

```bash
upkeep check paragraphs 42
```

The registry is a **watchlist**. It decides what `dashboard`, `patches`,
`issues` and `status` survey, and a module you register can say which cores it
supports — which then wins over anything upkeep would infer. Register the ones
you maintain; work on anything.

The cockpit is a plain directory holding your module registry, the per-core
base artifacts, your shared fixture library, and cached check results. Create
one:

```bash
upkeep init ~/upkeep-cockpit
```

That scaffolds `registry.yml` plus empty `base-artifacts/`, `fixtures/` and
`projects/` directories. Every command finds the cockpit via `--cockpit`, else
the `UPKEEP_COCKPIT` environment variable, else the current directory —
exporting the variable once is the comfortable setup:

```bash
export UPKEEP_COCKPIT=~/upkeep-cockpit
```

Environments live under the projects root, resolved in this order:

1. `--projects-root`;
2. the `UPKEEP_PROJECTS_ROOT` environment variable;
3. `<cockpit>/projects/`, when that directory exists — which is why `init`
   creates it: a cockpit is self-contained by default;
4. `~/.upkeep/projects`.

Whichever wins must resolve to a path under `$HOME` (see the Colima note
above).

Two more directories appear inside the cockpit as you use it: `results/`
(cached check results) and `cache/` (cached GitLab and drupal.org data). Both
hold token-scoped remote data and raw check output, so Upkeep creates them
`0700` with `0600` files. If you keep your cockpit in version control, exclude
both — they are caches, they regenerate, and they are not meant to be shared.

## Register your modules

Edit `registry.yml`. Each entry is a module machine name with its
git.drupalcode.org project path and the list of core versions you maintain it
for:

```yaml
modules:
  conditions_helper:
    project: project/conditions_helper
    core_versions: ["10", "11"]
  field_visibility_conditions:
    project: project/field_visibility_conditions
    core_versions: ["11"]
```

Or skip the hand-editing: `modules:add` lists every `project/` namespace
project your token is a member of on git.drupalcode.org — for a maintainer,
that's your modules — and registers the ones you opt into:

```bash
upkeep modules:add                          # interactive: pick from your memberships
upkeep modules:add token_or field_helper    # non-interactive: register by name
upkeep modules:add --core-versions=10,11    # cores the new entries track (default: 11)
```

Already-registered modules are never offered twice, existing entries are never
overwritten, and the command is read-only against GitLab. Note: writing
regenerates `registry.yml`, so hand-written comments in it do not survive.

## Change which cores a module tracks

`core_versions` is the one registry field that moves in the ordinary course of
maintenance — a new core major reaches alpha, an old one goes end of life — so
it is a command rather than a file edit:

```bash
upkeep modules:track jumplinks              # what it tracks today
upkeep modules:track jumplinks 12           # track core 12 as well
upkeep modules:track jumplinks --remove=10
upkeep modules:track jumplinks --set=12,11  # replace the list, in this order
```

Cores named as arguments are **appended**, which matters: the first entry is
the core a command targets when you omit `--version`, so adding next year's
core does not quietly retarget every bare `upkeep check`. Moving that default
is `--set`'s job, and nothing else's.

`modules:add --core-versions` does not do this — it applies to new entries
only, and deliberately never overwrites one you already have.

If a core you start tracking has no base artifacts yet, the command says so
and names the build; see [Base artifacts](../base-artifacts.md).

## Stop watching a module

```bash
upkeep modules:untrack jumplinks
```

The entry goes, so `dashboard`, `patches`, `modules`, `status` and `prune` stop
covering it. **Nothing on disk is removed and nothing stops working**: the
registry is a watchlist rather than a gate, so `check`, `review` and `dev`
still take the module by name. To reclaim its environments and cached results,
run [`prune`](../reference/disk.md).

The report prints the cores it removed, as the `modules:add` that puts them
back — that list is a judgement you made, and the registry was the only place
it lived.

Check what is registered:

```bash
upkeep modules
```
