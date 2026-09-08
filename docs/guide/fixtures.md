# Fixtures

Check against the database state your module actually lives in, not a bare install.

Checks run against a clean install by default. When a check (or your manual
review) needs real content — configured entities, test users, sample nodes —
load a named fixture with `check --fixture=NAME`. Fixtures are gzipped SQL
dumps managed by the companion **ddev-upkeep** add-on, which Upkeep installs
into every environment automatically; module-local fixtures live in the
module's `tests/fixtures/`, shared ones in the cockpit's `fixtures/`
directory. See the
[ddev-upkeep README](https://github.com/owenbush/ddev-upkeep) for the full
fixture model and the `tests/fixtures/` convention.

> **Until ddev-upkeep is published:** `ddev add-on get owenbush/ddev-upkeep`
> 404s while the repo is private, so point Upkeep at your add-on source
> explicitly — `export UPKEEP_ADDON_SOURCE=/path/to/ddev-upkeep` (a local
> checkout path, or any source `ddev add-on get` accepts). This is temporary
> and disappears at publication.
