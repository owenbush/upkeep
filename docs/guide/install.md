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

A `brew install` wires this up for you — the cask generates the bash, zsh and
fish completions at install time, and there is nothing to do. The rest of this
section is for a binary you built or downloaded yourself.

**Completion keys on the command name, so `upkeep` has to be on your `PATH`.**
A binary you run as `./upkeep` gets none: the shell is completing the word
`./upkeep`, which is not the command the script registers. Check that the
`upkeep` your shell finds is the one you mean, which matters if an older
install is still around:

```bash
which upkeep     # expect your Go binary, not ~/.composer/vendor/bin/upkeep
```

### zsh, if you are working on upkeep

One line in `~/.zshrc`, **below** whatever runs `compinit`:

```bash
eval "$(upkeep completion zsh)"
```

No directory to create, no `fpath`, and no completion dump to go stale — which
were three of the four ways the file below goes wrong. It regenerates from the
binary at every shell start, so a command you add and rebuild completes
immediately, with no step to remember. That is the reason to prefer it while
the surface is still moving.

The cost is running `upkeep` once per shell start. It is a static binary doing
one thing, so this is a few milliseconds; if you measure your startup and care,
use the installed file instead.

### zsh, for an installed binary

Generated once, so nothing runs at shell start. The file on its own does
nothing, though — zsh reads completions only from directories on its `fpath`,
and only when `compinit` runs *after* they are added:

```bash
mkdir -p ~/.zsh/completions
upkeep completion zsh > ~/.zsh/completions/_upkeep
```

Then in `~/.zshrc`, **above** the line that runs `compinit` (with oh-my-zsh,
above `source $ZSH/oh-my-zsh.sh`, which runs it for you):

```bash
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit && compinit
```

Open a new shell. If nothing completes, zsh is probably serving a cached dump:

```bash
rm -f ~/.zcompdump* && exec zsh
```

Regenerate the file whenever you upgrade, or the completions describe the
version you had. A `brew upgrade` does it for you; a `go build` does not.

### fish

Nothing else to do; fish reads this directory itself.

```bash
upkeep completion fish > ~/.config/fish/completions/upkeep.fish
```

The same trade as zsh applies — `upkeep completion fish | source` in
`~/.config/fish/config.fish` tracks a binary you are rebuilding, at the cost of
running it each time.

### bash

```bash
upkeep completion bash | sudo tee /etc/bash_completion.d/upkeep
```

Or, tracking a binary you are rebuilding, in `~/.bashrc`:

```bash
source <(upkeep completion bash)
```

On macOS this needs Homebrew's bash and bash-completion v2 — the bash Apple
ships is 3.2, which cannot show the descriptions below and does not read that
directory. `brew install bash bash-completion@2`, then follow what that formula
prints. zsh is the default shell on macOS and needs none of this.

### What completes

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

Every command and flag carries its one-line description, so TAB is also how you
read the surface without leaving the prompt:

```
$ upkeep modules:<TAB>
modules:add      -- Register maintained modules from your git.drupalcode.org project memberships
modules:track    -- Change which Drupal core majors a registered module is tracked for
modules:untrack  -- Stop watching a module: remove its entry from the cockpit module registry
```

zsh and fish show those descriptions out of the box. bash shows them only with
bash-completion v2; with v1 you get the names alone, which is the shell's limit
and not something upkeep can supply.
