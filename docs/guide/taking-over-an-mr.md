# Working on somebody else's merge request

Reading a contribution and changing one are different jobs, and they use
different commands. `check` and `review` answer *"is this good?"*. This page is
the other half: taking a merge request that is nearly right, or deliberately
incomplete, and finishing it.

The worked example is the one that prompted this page, and it is the commonest
shape in contrib maintenance:

> The Project Update Bot has opened a merge request for Drupal 12
> compatibility. It fixes the code but does not touch
> `core_version_requirement`, so nothing can actually be tested against core
> 12 yet. I want to add that, verify it, and push it back to the bot's merge
> request.

## Why `check` is not the way in

`check` and `review` fetch `refs/merge-requests/<iid>/merge` onto a branch
called `mr-<iid>`. That is exactly right for producing a verdict and exactly
wrong for working:

- It is **force-updated on every apply**, so a commit on it is a commit waiting
  to be destroyed.
- `publish` refuses it, for the same reason.
- It is the *merge* tree — the contribution merged into the current tip of its
  target — which is a tree that exists on neither side and cannot be pushed
  anywhere.

What you want is the merge request's own **source branch**, which on
drupal.org lives on an issue fork rather than on the project. `mr:checkout`
gets it.

## The loop

```bash
upkeep mr:checkout jumplinks 1 --version=12
```

That resolves which fork the branch is on, adds the remote that pushes back to
it, fetches the branch and checks it out — then tells you the two commands you
will want next:

```
  Environment  upkeep-jumplinks-d12
  Branch       project-update-bot-only
  Module path  /…/upkeep-jumplinks-d12/module

  Check it as you work:  upkeep check jumplinks --working-copy --version=12
  Send the work back:    upkeep publish jumplinks 3628056 --branch project-update-bot-only --version=12
```

**`--version` is not optional on any of these.** An environment is one per
module *and core*, and every command defaults the core to the first entry in
the module's `core_versions`. `modules:track jumplinks 12` appends, precisely
so that adding a core does not silently retarget your bare `upkeep check` — so
a module tracking `["11", "12"]` defaults to 11 while you are working in the 12
environment. Omit it and the command goes looking in a different directory,
and refuses on a branch that was never there.

The issue node id and the branch name are printed because you have no reason to
know either and every reason to mistype them.

Now edit the module — it is an ordinary git checkout at the module path — and
iterate locally, with no push and no merge request in the way:

```bash
upkeep check jumplinks --working-copy --version=12
```

`--working-copy` runs the whole suite against whatever the working copy holds.
It needs no merge request, no token, and it **caches nothing**: every other
verdict is keyed by a subject and a revision so staleness is detectable, and a
working copy has neither.

When it passes, commit and push it back:

```bash
cd "$(upkeep env:path jumplinks --version=12)/module"
git commit -am "Declare Drupal 12 compatibility"
cd -
upkeep publish jumplinks 3628056 --branch project-update-bot-only --version=12
```

`publish` finds the merge request already open on that branch and reports
*"Updated the open merge request for this branch: !1"*. It never opens a second
one.

And now the thing you could not do at the start:

```bash
upkeep check jumplinks 1 --version=12
```

This works because the core check reads the merge request's **own** tree, not
the branch it targets — so the `^12` you just added is what it sees.

## Why `mr:checkout` does not check the core

Every other merge-request command refuses a core the code does not declare,
because gathering evidence against an unsupported core produces failures that
say nothing about the contribution.

`mr:checkout` deliberately does not ask. The reason to take over a merge
request is frequently that it does *not* support the core yet — that is the
work. Refusing to check out the branch because the branch lacks what you are
about to add would be the tool declining its own purpose. `check` asks the
question, at the point a verdict is produced, where it means something.

For the same reason, `upkeep dev <module> --version=12` will provision an
environment for a core the module does not declare. The module is linked into
the site rather than installed as a composer package, so its own
`drupal/core` constraint is not imposed on the site.

## Nothing here resets anything

`mr:checkout` is safe to re-run. An existing local branch of that name is
checked out **as it stands** — it may hold commits that exist nowhere else, and
discarding those is precisely what the managed `mr-<iid>` branch does and this
command does not. If you want the fork's version of it, move your own commits
aside first; upkeep will not do it for you.

A dirty working copy is refused before anything is fetched, naming what is
uncommitted.

## What you need, and what you do not

| | |
|---|---|
| Checking out the branch | nothing. The fork is fetched over anonymous HTTPS |
| Running checks | nothing |
| Pushing it back | an **SSH key**, and push access to the issue fork |

upkeep never hands git a password or a token — `publish` pushes over SSH to a
URL GitLab itself supplies. A `HTTP Basic: Access denied` means something tried
to push over HTTPS; a personal access token will not fix it.

Two push refusals need opposite answers, and upkeep tells them apart:

- **GitLab does not know you** — your SSH key. Add one at
  [git.drupalcode.org SSH keys](https://git.drupalcode.org/-/user_settings/ssh_keys),
  then `ssh -T git@git.drupal.org` and `ssh-add -l`.
- **GitLab knows you and says no** — push access. On drupal.org, creating an
  issue fork does not grant push access to it; that is a separate button on the
  issue page. If the fork is somebody else's and you are not a maintainer, the
  access is theirs to give.

## When the branch is not on a fork

`mr:checkout` only handles a merge request whose branch is on an issue fork,
which measured on pathauto is 100 of 100 open merge requests. If the branch is
on the project itself, or the merge request does not say where it is, the
command refuses and says so rather than guessing — check it out with git, or
start the work as your own contribution:

```bash
upkeep start <module> <issue>      # your own branch, cut from a fresh base
upkeep publish <module> <issue>    # pushed to your issue fork, opened as an MR
```

That is the other entry point, and it is the right one when you are not
continuing somebody's existing work. See [Work on an
issue](issue-loop.md).
