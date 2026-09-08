# Disk housekeeping

Where the space goes, and how to reclaim the disposable half of it safely.

```bash
upkeep status
```

summarizes the cockpit, environments, and total tracked disk usage;
`upkeep status --disk` itemizes it per module, core version, and category
(project trees, docker volumes, materialized snapshots, base artifacts,
fixture dumps) with totals. The header line reports how many modules are
registered:

```
Cockpit: /home/you/upkeep-cockpit (2 registered module(s))
```

To count them, `status` — like `base-artifacts:status` and
`base-artifacts:build` — parses `registry.yml` rather than just checking that
it exists, so a malformed registry now fails these commands with exit 2 too,
instead of being discovered later by a command that reads it.

```bash
upkeep prune --all
```

is a **dry run** — it lists deletion candidates (environment trees, docker
volumes, materialized fixture snapshots) with reclaimable sizes and deletes
nothing until you add `--yes`. Narrow it with `--trees`, `--snapshots`, or
`--projects`, age-gate with `--older-than=30d`, and keep the newest N
snapshots per project with `--keep-latest=N`. Everything pruned regenerates
on demand.

`--older-than` measures an environment's age from when it was **last used**,
not when it was created: each environment's `.upkeep-env.yml` carries a
`last_used_at` timestamp, stamped at provision and re-stamped every time the
environment is reused. An environment created months ago but checked against
this morning is not a deletion candidate. Environments provisioned before this
field existed parse fine — the key is optional — and fall back to their
creation date until the next time they are used.

Protected regardless of flags: base artifacts, committed fixture dumps
(`.sql.gz`), and keep-marked items — `touch <project>/.keep` keeps a whole
environment, and an `<artifact>.keep` sibling (e.g.
`materialized/<name>.sql.keep`) keeps a single snapshot. Environments are
always disposed through the engine adapter, so containers and named volumes
are released together with the tree.
