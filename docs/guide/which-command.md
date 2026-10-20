# Which command do I want?

Thirty commands, organised by what you are trying to do rather than by name.

The [command reference](../commands.md) lists every command, every flag and
what each one changes. This page is the other question: *given where I am, what
do I run?* Each answer links to the guide that covers it properly.

## Start here, always

```bash
upkeep dashboard
```

Everything open across every module you watch, one line each. Name a module —
`upkeep dashboard pathauto` — and every row carries a plain-English status
**and the command to run next**. If you remember nothing else on this page,
remember that the dashboard tells you what to type.

## By what you are trying to do

### Find out what needs attention

| You want | Run |
| --- | --- |
| The overview across every watched module | `upkeep dashboard` |
| One module's rows in detail | `upkeep dashboard <module>` |
| Every open issue, including ones nobody has touched | `upkeep issues <module>` |
| Only issues nobody has contributed to | `upkeep issues <module> --unclaimed` |
| Issues carrying patch files rather than merge requests | `upkeep patches [<module>]` |
| What a word in the output means | `upkeep explain <term>` |

[The dashboard →](./dashboard.md) · [Work on an issue →](./issue-loop.md) ·
[Patch contributions →](./patches.md)

### Judge somebody else's contribution

| You want | Run |
| --- | --- |
| A verdict on a merge request | `upkeep check <module> <iid>` |
| A verdict on a specific core version | `upkeep check <module> <iid> --version=12` |
| To look at it in a browser, with a login | `upkeep review <module> <iid>` |
| A verdict on a patch posted to an issue | `upkeep patch:check <module> <nid>` |
| To merge the ones that cleared every gate | `upkeep merge --fast-lane` |
| To tell a contributor it needs work | `upkeep needs-work <module> <iid>` |

`check` runs the suite; `review` runs nothing and hands you a URL. Green checks
and looking at the thing are different acts, and you usually want both.

[Running checks →](./checks.md) · [Review and merge →](./review-and-merge.md)

### Do the work yourself

| You want | Run |
| --- | --- |
| To start a fix for an issue | `upkeep start <module> <nid>` |
| To check what you have written, on any branch | `upkeep check <module> --working-copy` |
| To push it and open a merge request | `upkeep publish <module> <nid>` |
| To take over somebody's merge request | `upkeep checkout <module> <iid>` |
| To build on somebody's patch | `upkeep patch:promote <module> <nid>` |
| A shell, a path, or a running site to poke at | `upkeep dev <module>` |

[Work on an issue →](./issue-loop.md) ·
[Take over an MR →](./taking-over-an-mr.md) ·
[Working in an environment →](./environments.md)

### Set upkeep up, or keep it tidy

| You want | Run |
| --- | --- |
| A cockpit, first time | `upkeep init <path>` |
| The per-core building blocks every environment copies | `upkeep base-artifacts:build --version=11` |
| To watch a module | `upkeep modules:add <module>` |
| To test a module against another core version | `upkeep modules:track <module> <core>` |
| To stop watching one | `upkeep modules:untrack <module>` |
| To see where the disk went | `upkeep status` |
| To get the disk back | `upkeep prune` |

[Set up a cockpit →](./cockpit.md) ·
[Base artifacts →](../base-artifacts.md) ·
[Disk housekeeping →](../reference/disk.md)

## What will it do to me?

Three things are worth knowing before you run something unfamiliar. All three
are generated per command in the
[command reference](../commands.md#what-each-one-does-to-you); the shape of the
answer is this:

**Most of the surface changes nothing.** Every command that only reads —
`dashboard`, `issues`, `patches`, `status`, `explain`, `notes`, `env:path`,
`api:probe`, `modules`, `issue`, `version`, `base-artifacts:status` — is safe to
run at any time, in any state.

**Four commands write to drupal.org**, and only those four:

| Command | What it does out there |
| --- | --- |
| `publish` | pushes your branch and opens a merge request |
| `merge` | merges one, after asking you about it |
| `needs-work` | posts a comment |
| `modules:add` | nothing — but it reads *your* memberships, so it needs a token |

Those are the only commands that need a **GitLab token**. Everything else reads
anonymously, because git.drupalcode.org serves a public project's merge
requests, refs, forks and raw files without one.

**They also need somebody present.** `publish` and `needs-work` refuse when
there is no terminal to confirm at, unless you pass `--yes`; `merge` refuses
outright, because an approval it consumes is one only a human can give. So a
script, a pipe or an agent cannot open a merge request or comment on somebody's
issue by accident:

```bash
echo | upkeep publish pathauto 3223746
# Error: publishing pushes your branch and opens a merge request on
# drupal.org under your name, and there is no terminal here to confirm
# at. Re-run with --yes if that is what you meant
```

`needs-work --dry-run` is exempt, since it prints the comment and posts
nothing.

**Five can lose something.** Nothing else can:

| Command | What it can lose |
| --- | --- |
| `prune` | environments, cached results and snapshots — **only with `--yes`**, and it lists candidates first |
| `merge` | nothing local, but the merge itself is not undoable from here |
| `modules:untrack` | the module's core list, which is recorded nowhere else |
| `base-artifacts:build --force` | that core's existing artifact set |
| `exec` | anything the command you hand it does |

Plus one category rather than a command: `check`, `review`, `patch:apply` and
`patch:check` **force-update the disposable branch they own** — `mr-<iid>` or
`patch-<nid>` — on every run. A commit you make on one of those is a commit
waiting to be destroyed. That is what `upkeep checkout` and `upkeep start`
exist for: they put you on a branch upkeep never resets.

## Fixtures

`--fixture=<name>` loads a named database fixture before the command does its
work. It is offered by three commands:

```bash
upkeep check <module> <iid> --fixture=smoke
upkeep patch:check <module> <nid> --fixture=smoke
upkeep review <module> <iid> --fixture=smoke
```

**Nothing loads a fixture unless you ask.** There is no default and no
automatic selection — a check runs against a clean minimal install until you
name one, which is what keeps a verdict reproducible. An unknown name aborts
before any check runs rather than quietly testing the wrong state.

Loading one **overwrites the environment's database**. That is the whole point
on `check` and `patch:check`, whose environments are about to be used and
discarded. On `review` it is worth a thought: you may have been poking at that
site.

[Fixtures →](./fixtures.md)

## The flags you will actually use

Four appear nearly everywhere:

| Flag | What it does |
| --- | --- |
| `--version=N` | the target **Drupal core major**, never an upkeep version. Defaults to the first core the module tracks |
| `--cockpit=<path>` | which cockpit to use, if not `$UPKEEP_COCKPIT` or the current directory |
| `-v` | every line of the engine's own output, rather than the latest. The way to see a truncated check report in full |
| `--fixture=<name>` | on the three commands above |
| `--json` | one JSON document on stdout instead of a table, on the nine commands a program reads |

`upkeep <command> --help` is authoritative for the rest, and
[the reference](../commands.md) lists them all in one page.

If you are scripting any of this, or pointing an agent at it, read
[structured output](../reference/json.md) rather than parsing the tables: the
columns truncate titles and are free to change, and the JSON is a contract.

## Exit codes

Three, and they mean the same thing everywhere:

| Code | Meaning |
| --- | --- |
| `0` | upkeep did what you asked |
| `1` | the work it supervised failed — a red check, a patch that would not apply |
| `2` | upkeep could not do the job — a bad argument, no environment, no credential |

So `1` is about your code and `2` is about your setup.

[Exit codes →](../reference/exit-codes.md)
