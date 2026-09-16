# Install upkeep

What upkeep needs, how to install it from a clone, and how to get tab completion.

## Requirements

- ddev >= 1.24.10 with a working container provider
- A [git.drupalcode.org](https://git.drupalcode.org) personal access token —
  only to merge, comment or publish; reading needs none (next section)

upkeep is a single static binary. It needs no runtime installed: no PHP, no
Composer, no Go unless you are building it yourself.

> **Note for Colima / Docker Desktop users:** environments are bind-mounted
> into the container runtime's VM, and macOS providers only share your home
> directory by default. The projects root (where environments live) must
> therefore be under `$HOME`, and upkeep **refuses** a
> `--projects-root`/`UPKEEP_PROJECTS_ROOT` (or a
> `base-artifacts:build --scratch-dir`) that resolves outside it, exiting 2
> with an explanation. That is cheaper than an opaque mount failure minutes
> into a provision. The check is done on the resolved path, so a symlink or a
> `..` pointing out of `$HOME` is refused too, and there is no override — a
> path outside your home directory cannot work. The defaults
> (`<cockpit>/projects`, `~/.upkeep/projects`, `~/.upkeep/scratch`) are all
> inside `$HOME` already, so this only matters if you move them.

## Install

### macOS

```bash
brew install owenbush/tap/upkeep
```

Signed with a Developer ID and notarized, so Gatekeeper runs it without
argument.

### Linux

Download the archive for your architecture from the
[releases page](https://github.com/owenbush/upkeep/releases) — `amd64` and
`arm64` are both built — and put the binary on your `PATH`:

```bash
tar xzf upkeep_*_linux_amd64.tar.gz
sudo install -m 0755 upkeep /usr/local/bin/upkeep
```

Not brew: Homebrew on Linux does not install casks, and the archive is one
command.

### Windows

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

### With Go

```bash
go install github.com/owenbush/upkeep/cmd/upkeep@latest
```

### From a clone

For working on upkeep itself:

```bash
git clone https://github.com/owenbush/upkeep.git
cd upkeep
go build -o upkeep ./cmd/upkeep
```

Requires Go >= 1.24. There is nothing to install first — `go build` fetches
what it needs and the checkout is ready.

Sanity check, however you installed:

```bash
upkeep --help
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
