# Fixtures

Check against the database state your module actually lives in, not a bare install.

Checks run against a clean minimal install by default. That is the right
default — it is reproducible and it is what CI does — but plenty of modules
cannot be meaningfully exercised against an empty site. If your module manages
vocabulary terms you need terms; if it configures form displays you need
content types with fields. A **fixture** captures that setup once instead of
rebuilding it every time.

```bash
upkeep check pathauto 12 --fixture=smoke
upkeep patch:check pathauto 3597857 --fixture=smoke
```

An unknown fixture name aborts **before any check runs**, rather than quietly
testing against the wrong state.

## What a fixture is

A named gzipped SQL dump, `<name>.sql.gz`. **The dump is the portable source of
truth** — it is what you commit, share and keep. It is managed by the companion
[ddev-upkeep](https://github.com/owenbush/ddev-upkeep) add-on, which upkeep
installs into every environment automatically; you never have to invoke it
yourself, but you can:

| Command | What it does |
| --- | --- |
| `ddev fixture-create <name>` | Dump the current database to a portable fixture. Runs `drush sql:sanitize` by default when the destination is the module repository; `--no-sanitize` opts out |
| `ddev fixture-load <name>` | Load one. First use imports the dump; later loads restore a snapshot |
| `ddev fixture-list` | Both scopes, with size and snapshot state |
| `ddev fixture-prune` | Delete this project's materialized snapshots — never the dumps |

## Dumps and snapshots

On first load the dump is imported and *materialized* into a fast-format
snapshot, which later loads restore instead of re-importing. A metadata file
records the database engine identity and the dump's checksum at materialization
time; if either changes — you upgraded the engine, or the dump was updated —
the snapshot is stale and is rebuilt from the dump on the next load.

Snapshots are **disposable, engine-tied local caches and never authoritative**.
`upkeep prune` and `ddev fixture-prune` delete them; the dump is untouched, and
the next load rebuilds. Do not commit them.

## Where a fixture is found

Module scope first, then the shared library:

1. `tests/fixtures/<name>.sql.gz` in the module checkout — when the project root
   is a module checkout, which under ddev-drupal-contrib it is.
2. The shared library: `$UPKEEP_FIXTURE_LIBRARY`, defaulting to
   `<cockpit>/fixtures`, defaulting to `~/.upkeep/fixtures`.

A module fixture always shadows a same-named library one. So a fixture
committed to the module wins for everybody who checks that module out, and the
library is for state you want across modules.

## The `tests/fixtures/` convention

This stands alone. It applies to any Drupal contrib module, whether or not you
or your co-maintainers use upkeep — anyone without the add-on can
`gunzip -c tests/fixtures/<name>.sql.gz` and import it with whatever they like.

- **Path.** `tests/fixtures/` in the module repository, beside the module's
  other test resources.
- **Format and naming.** One gzipped SQL dump per fixture,
  `tests/fixtures/<name>.sql.gz`. Lowercase, filesystem-safe, and named for the
  state it provides: `smoke.sql.gz`, `two-vocabularies.sql.gz`,
  `upgrade-from-2x.sql.gz`.
- **Sanitisation is mandatory.** A committed fixture is public data. Never dump
  a database holding real accounts, e-mail addresses, personal data or secrets:
  sanitise first, or build the fixture from a scratch install that never held
  any. `ddev fixture-create` runs `drush sql:sanitize` for you by default when
  the destination is the module repository.
- **Keep them lean.** The *minimum* state that makes the fixture useful — a
  minimal-profile install plus the entities your tests need, not a production
  copy. Gzipped SQL of a minimal Drupal install is well under 5 MB; treat
  anything larger as a smell (the add-on warns at that threshold), and
  megabytes of it are usually cache and log tables. Truncate them before
  dumping.

> **Until ddev-upkeep is published:** `ddev add-on get owenbush/ddev-upkeep`
> 404s while the repository is private, so point upkeep at your add-on source
> explicitly — `export UPKEEP_ADDON_SOURCE=/path/to/ddev-upkeep` (a local
> checkout, or any source `ddev add-on get` accepts). Temporary; it disappears
> at publication.
