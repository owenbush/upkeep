# Working in an environment

Getting a shell, a path and a running site — and what to do when the engine refuses a project name.

## Interact with an environment

Once a `check` or `review` has provisioned an environment, you can run
commands in it directly without knowing the project path:

```bash
upkeep exec entity_type_access_conditions -- ddev drush cr
upkeep exec entity_type_access_conditions -- ddev ssh
upkeep exec entity_type_access_conditions --version=10 -- ddev logs
```

Everything after `--` is run with the environment directory as the working
directory.

**The child's exit code is not passed through.** `exec` answers on the same
0/1/2 contract as every other command: 0 if the command succeeded, **1 for any
non-zero exit** — 1, 2, 7 and 127 all collapse to 1 — and 2 if Upkeep could not
run it at all (unregistered module, untracked core version, no provisioned
environment, or a child process that never started). Collapsing is deliberate:
it means a child exiting 2 can never be mistaken for an Upkeep infrastructure
failure. If you need the child's own code, have the child report it — for
example `upkeep exec mod -- sh -c 'mycmd; echo "rc=$?"'`.

**The child does not inherit `UPKEEP_GITLAB_TOKEN`.** The credential is
removed from every child environment, because Symfony's process layer
otherwise copies the whole parent environment into every subprocess — and this
tool logs, renders and caches that subprocess's output. If a command you run
through `exec` needs a GitLab token, give it one from its own source rather
than relying on inheritance.

Upkeep's own diagnostics from `exec` go to **stderr**; stdout belongs entirely
to the wrapped command, so piping it stays clean.

To get the bare path (for `cd` or other tools):

```bash
cd $(upkeep env:path entity_type_access_conditions)
upkeep env:path entity_type_access_conditions --version=10
```

`env:path` prints the path and nothing else on stdout — its errors also go to
stderr, so `cd $(upkeep env:path …)` can never capture an error message into
the path.

Both commands default to the first core version tracked in the registry when
`--version` is omitted.

## When the engine refuses a project name

ddev project names are global to your machine, and upkeep derives them from
`upkeep-<module>-d<core>`. So if your projects root moves — a new cockpit, or a
`--projects-root` / `UPKEEP_PROJECTS_ROOT` that differs from one you used
before — ddev still has the old path registered under that name and refuses to
configure the new one.

upkeep now catches this before it seeds anything, and tells you what to run:

```
The engine already knows a project called "upkeep-widget-d11", at a different path:
  registered: /Users/owen/.upkeep-scratch/projects/upkeep-widget-d11
  wanted:     /Users/owen/contrib/upkeep/projects/upkeep-widget-d11

If the registered path is stale, deregister it and re-run:
  ddev stop --unlist upkeep-widget-d11

That removes the engine's record of it; it does not delete the directory or
anything in it. If the registered path is the one you actually want, point
--projects-root at it instead.
```
