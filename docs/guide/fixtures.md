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

A named gzipped SQL dump, `<name>.sql.gz`, and a generated manifest beside it,
`<name>.yml`. **The dump is the portable source of truth** — it is what you
commit, share and keep — and the manifest is what makes it mean anything
anywhere else. It is managed by the companion
[ddev-upkeep](https://github.com/owenbush/ddev-upkeep) add-on, which upkeep
installs into every environment automatically; you never have to invoke it
yourself, but you can:

| Command | What it does |
| --- | --- |
| `ddev upkeep-fixture-create <name>` | Dump the current database to a portable fixture and write its manifest. Runs `drush sql:sanitize` by default when the destination is the module repository; `--no-sanitize` opts out |
| `ddev upkeep-fixture-load <name>` | Load one. First use imports the dump; later loads restore a snapshot |
| `ddev upkeep-fixture-list` | Both scopes, with size and snapshot state |
| `ddev upkeep-fixture-prune` | Delete this project's materialized snapshots — never the dumps |

## A dump alone is not enough

A database encodes references to *code*: which extensions are enabled, plugin
IDs inside config entities, field types, schema versions. It captures none of
that code. Load a dump into an environment that does not provide it and Drupal
cannot build a container — and because that happens during your checks, the
failure reads as though the merge request under test is broken.

That matters here more than on an ordinary site, because an upkeep environment
is deliberately spare. It contains core, the module under test, that module's
own dependencies, and the check toolchain. **Nothing else.** So a fixture
captured on your everyday development site, with whatever contrib you happen to
have installed, will not load into one.

The dividing line is worth knowing:

| In the fixture's database | In an upkeep environment? |
| --- | --- |
| Core modules and themes — `node`, `views`, `olivero`, `claro` | Yes, they ship inside `drupal/core` |
| The module under test, and its submodules | Yes |
| Anything that module `require`s in its `composer.json` | Yes — composer resolves it |
| Any other contrib — `admin_toolbar`, `webform`, `paragraphs` | **No** |

So `olivero` is fine and `admin_toolbar` is not, unless the module under test
depends on it.

This is what the manifest is for. `upkeep-fixture-create` records the core
version, the enabled extensions and the composer requirements it saw:

```yaml
core: '11'
core_version: '11.4.6'
require:
  drupal/admin_toolbar: '^3.4'
extensions: [admin_toolbar, node, pathauto, views]
```

and `upkeep-fixture-load` makes the environment able to hold the dump, or
refuses before touching the database:

1. **Core major must match.** A dump captured on Drupal 11 will not be loaded
   into a Drupal 10 environment.
2. **Declared packages are installed** if the environment does not have them,
   and what it installed is printed. This is why the requirement has to live
   in the fixture rather than in your shell history: an environment can be
   re-provisioned at any time — a base-artifact rebuild does it through seed
   skew — and anything you added by hand goes with it.
3. **Every declared extension must then be present**, or the load is refused
   and the missing ones are named. A custom module in a fixture is the case
   that lands here; nothing can install it for you.

You never write the manifest. It is generated from the database and the
project's `composer.json` at capture time, so it cannot drift from the dump it
sits beside.

A dump with no manifest — one captured before they existed — loads exactly as
it always did, with nothing checked.

## Dumps and snapshots

On first load the dump is imported and *materialized* into a fast-format
snapshot, which later loads restore instead of re-importing. A metadata file
records the database engine identity and the dump's checksum at materialization
time; if either changes — you upgraded the engine, or the dump was updated —
the snapshot is stale and is rebuilt from the dump on the next load.

Snapshots are **disposable, engine-tied local caches and never authoritative**.
`upkeep prune` and `ddev upkeep-fixture-prune` delete them; the dump is untouched, and
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
  `tests/fixtures/<name>.sql.gz`, plus its generated manifest,
  `tests/fixtures/<name>.yml`. Commit both — a dump whose requirements nobody
  recorded is a dump that only works on the machine it was made on. Lowercase,
  filesystem-safe, and named for the state it provides: `smoke.sql.gz`,
  `two-vocabularies.sql.gz`, `upgrade-from-2x.sql.gz`.
- **Sanitisation is mandatory.** A committed fixture is public data. Never dump
  a database holding real accounts, e-mail addresses, personal data or secrets:
  sanitise first, or build the fixture from a scratch install that never held
  any. `ddev upkeep-fixture-create` runs `drush sql:sanitize` for you by default when
  the destination is the module repository.
- **Keep them lean.** The *minimum* state that makes the fixture useful — a
  minimal-profile install plus the entities your tests need, not a production
  copy. Gzipped SQL of a minimal Drupal install is well under 5 MB; treat
  anything larger as a smell (the add-on warns at that threshold), and
  megabytes of it are usually cache and log tables. Truncate them before
  dumping.

## The add-on itself

upkeep installs [ddev-upkeep](https://github.com/owenbush/ddev-upkeep) into
every environment it provisions, at a **pinned release** — the same treatment
ddev-drupal-contrib gets, and for the same reason: an environment that tracked
whatever the latest release happened to be would take a breaking change to the
add-on without warning. An environment holding an older release re-installs on
next use.

To develop the add-on against upkeep, point it somewhere else:

```bash
export UPKEEP_ADDON_SOURCE=/path/to/ddev-upkeep
```

That takes a local checkout or anything `ddev add-on get` accepts, and no
version is pinned to it — a checkout has no release to pin to. Moving between
a checkout and the published release re-installs, so an environment cannot be
left holding whichever arrived first.
