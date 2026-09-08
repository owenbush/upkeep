# Install upkeep

What upkeep needs, how to install it from a clone, and how to get tab completion.

## Requirements

- PHP >= 8.2 and Composer
- ddev >= 1.24.10 with a working Docker provider
- A [git.drupalcode.org](https://git.drupalcode.org) personal access token
  (next section)

> **Note for Colima / Docker Desktop users:** environments are bind-mounted
> into the Docker VM, and macOS Docker providers only share your home
> directory by default. The projects root (where environments live) must
> therefore be under `$HOME`, and Upkeep **refuses** a
> `--projects-root`/`UPKEEP_PROJECTS_ROOT` (or a
> `base-artifacts:build --scratch-dir`) that resolves outside it, exiting 2
> with an explanation. That is cheaper than an opaque mount failure minutes
> into a provision. The check is done on the resolved path, so a symlink or a
> `..` pointing out of `$HOME` is refused too, and there is no override — a
> path outside your home directory cannot work. The defaults
> (`<cockpit>/projects`, `~/.upkeep/projects`, `~/.upkeep/scratch`) are all
> inside `$HOME` already, so this only matters if you move them.

## Install

Until the first release is published to Packagist, install from a clone:

```bash
composer install
```

then put `bin/upkeep` on your `PATH` (or call it by path). Once published,
the intended install is `composer global require owenbush/upkeep`.

**Re-run `composer install` after every `git pull`.** A pull can bring a new
dependency with it, and a `vendor/` older than the code it sits beside is a
broken install. Upkeep checks this on startup and refuses with exit 2, naming
the missing packages and the directory to run `composer install` in — rather
than dying partway through a command with a class-not-found trace.

`composer.lock` is committed, so `composer install` gives you the same
dependency versions CI tested against. Resolution is pinned to PHP 8.2 (the
lowest version upkeep supports) via `config.platform`, so the tree is the same
whichever PHP you run it on.

Sanity check:

```bash
upkeep list --raw
```

## Shell completion

```bash
upkeep completion bash | sudo tee /etc/bash_completion.d/upkeep   # bash
upkeep completion zsh  > ~/.zsh/completions/_upkeep               # zsh
upkeep completion fish > ~/.config/fish/completions/upkeep.fish   # fish
```

Open a new shell and TAB completes command names, options, **your registered
module machine names**, and the core versions each module actually tracks:

```
upkeep patch:pro<TAB>              -> upkeep patch:promote
upkeep patch:promote fi<TAB>       -> upkeep patch:promote field_inheritance
upkeep check pathauto 12 --version=<TAB>   -> 11
```

The module names come from your registry, so they are exactly the modules you
can act on, and `--version=` offers only the cores that module tracks rather
than every core any module uses. Completion reads the cockpit from
`UPKEEP_COCKPIT` or the current directory — set the env var if you drive upkeep
from outside the cockpit.

Values nothing local can enumerate — an MR IID, an issue node id — are not
completed, because guessing them would mean a network round trip on every press
of TAB.
