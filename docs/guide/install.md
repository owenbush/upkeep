# Install upkeep

What upkeep needs, how to install it from a clone, and how to get tab completion.

## Requirements

- PHP >= 8.2 and Composer — or Go >= 1.24 for [the Go build](#the-go-build)
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

```bash
composer global require owenbush/upkeep
```

That is the whole thing. Composer puts the `upkeep` binary in its global bin
directory, which is `~/.composer/vendor/bin` or `~/.config/composer/vendor/bin`
depending on your setup — `composer global config bin-dir --absolute` prints
yours.

If `upkeep` is not found afterwards, that directory is not on your `PATH`. Add
it once and every globally installed PHP tool works the same way:

```bash
export PATH="$(composer global config bin-dir --absolute):$PATH"
```

To update: `composer global update owenbush/upkeep`.

### What you get, and what you do not

A global install resolves upkeep's dependencies fresh against your PHP, rather
than using the `composer.lock` in this repository. So the tree you run is not
byte-for-byte the one CI ran. That is normal for a CLI tool, and it is checked
rather than assumed: CI resolves the same way on the lowest and highest
supported PHP — 8.2 gets Symfony 7.x, 8.4 gets Symfony 8.x — and runs every
gate against both.

Global installs share one dependency tree, so a tool with a narrow constraint
can conflict with another. upkeep's are deliberately wide
(`symfony/* ^7.2 || ^8.0`), which makes it the one that bends rather than the
one that breaks.

### Or from a clone

For working on upkeep itself:

```bash
git clone https://github.com/owenbush/upkeep.git
cd upkeep && composer install
```

**Re-run `composer install` after every `git pull`.** A pull can bring a new
dependency with it, and a `vendor/` older than the code it sits beside is a
broken install. Upkeep checks this on startup and refuses with exit 2 rather
than dying partway through a command with a class-not-found trace — naming the
missing packages, and the right recovery for how you installed it.

A clone *does* use the committed `composer.lock`, so it gives you the exact
versions CI tested. Resolution is pinned to PHP 8.2 via `config.platform`, so
that tree is the same whichever PHP you run it on. Both of those apply to
installing upkeep; neither reaches someone who requires it as a dependency.

Sanity check, however you installed:

```bash
upkeep list --raw
```

### The Go build

A full rewrite in Go lives in `go/`, on the `go-port` branch, with every
command ported. It needs no PHP and no Composer — one static binary — but it
wants the same ddev and the same token, and it reads and writes the same
cockpit, registry and base artifacts.

> Until the port is merged and a version is tagged, the only route below that
> works is building from source. The rest describe what a release publishes.

#### macOS

```bash
brew install owenbush/tap/upkeep
```

Signed with a Developer ID and notarized, so Gatekeeper runs it without
argument.

#### Linux

Download the archive for your architecture from the
[releases page](https://github.com/owenbush/upkeep/releases) — `amd64` and
`arm64` are both built — and put the binary on your `PATH`:

```bash
tar xzf upkeep_*_linux_amd64.tar.gz
sudo install -m 0755 upkeep /usr/local/bin/upkeep
```

Not brew: Homebrew on Linux does not install casks, and the archive is one
command.

#### Windows

Through [WSL2](https://learn.microsoft.com/windows/wsl/install), using the
Linux binary above.

That is not a workaround — it is where ddev itself puts you. All three of
ddev's supported Windows configurations require WSL2, and the one it
recommends for "most users, best performance" is Docker CE *inside* WSL2. Your
projects live in the WSL filesystem for the same reason: crossing the
Windows/WSL boundary costs ddev dearly. upkeep provisions and drives those
projects, so it belongs on the same side of that boundary.

There is no native Windows binary, and adding one would be a poor trade: it
does not compile today (the process-group kill that makes a timeout actually
stop `ddev start` is Unix-only), `$HOME` is not where Windows keeps a home
directory, and the result would serve a platform whose recommended setup is
the Linux one anyway.

#### From source

```bash
cd go
go build -o upkeep ./cmd/upkeep
./upkeep --help
```

Requires Go >= 1.24. `go install` does not work yet — the module lives in a
subdirectory and declares the root path, which [resolves when the port
replaces the PHP](../../go/README.md).

Everything else in this documentation applies to the Go build unchanged; where
it deliberately differs, [its own README](../../go/README.md) says so and why.

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
