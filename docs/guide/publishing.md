# Publishing: issue forks and SSH

How work reaches drupal.org: an issue fork, an SSH key, and why upkeep never hands git a credential.

Reading needs nothing — cloning, checking a merge request, applying a patch and
running the suite all work over anonymous HTTPS with no credentials at all.

Publishing needs two things, and both are drupal.org's model rather than
upkeep's choice.

**An issue fork.** On drupal.org a merge request never comes from the canonical
project: drupal.org mints a fork at `issue/<module>-<nid>`, the branch is pushed
there, and the merge request is opened *across* projects into the canonical
repository. (Measured on pathauto: 100 of 100 open merge requests come from a
fork, none from the project.) So `upkeep publish` resolves the fork, pushes to
it as a remote named `issue-<nid>`, and opens the merge request with
`target_project_id` naming the canonical project.

If the issue has no fork yet, publish refuses **before pushing anything** and
tells you to make one:

```
Issue #3597857 has no issue fork yet, and that is where the branch has to go.
  1. Open https://www.drupal.org/node/3597857
  2. Click "Create issue fork" (under the issue summary)
  3. Re-run: upkeep publish entity_type_access_conditions 3597857
```

upkeep does not create it. drupal.org mints the fork *and* links it to the
issue; one made straight from the GitLab API would be a repository nothing
points at, which is harder to clean up than the click was to make. Same browser
handoff as the issue status and the credit.

**Push access to that fork.** Creating an issue fork does not grant you write
access to it — that is a *second* button on the issue page ("Get push access",
beside the fork it names). upkeep asks GitLab before pushing, so a missing
grant arrives as a refusal naming the button rather than as a rejected push:

```
You do not have push access to issue/entity_type_access_conditions-3597857.
Your SSH key worked — GitLab knows who you are and will not let you write here.
```

If GitLab does not say either way, upkeep pushes anyway and lets the server
decide: an unknown is not a no, and refusing on one would block pushes that
would have worked.

**An SSH key on your drupal.org account.** Add one at
<https://git.drupalcode.org/-/user_settings/ssh_keys>, then check it:

```bash
ssh -T git@git.drupal.org
ssh-add -l                       # the agent actually holds it
```

Note the host: git.drupalcode.org serves the web and the API, but the SSH
remote GitLab advertises is **git.drupal.org**. upkeep never assembles that URL
— it uses the `ssh_url_to_repo` the API supplies, so it cannot get the host
wrong.

**upkeep never hands git a credential**, and that is why it is SSH rather than
your PAT. Every way of giving git a token writes it into `.git/config`, into a
credential store on disk, or into process argv where `ps` can read it — each of
which would be a second exception to the rule that `UPKEEP_GITLAB_TOKEN` never
reaches a child process. Your SSH agent answers instead. The PAT stays what it
is: an API credential for reading merge requests, pipelines and issues.

Origin is never pushed to and never altered.

## What the merge request says

The description is the issue reference, then a body:

```
Closes #3223746

Opened with `upkeep publish`.
```

The reference is upkeep's and is always the first line — it is what GitLab
closes the issue on, and what upkeep's own pairing reads to put the merge
request on the right dashboard row. `Closes #<nid>` is also the form drupal.org
itself auto-populates: of pathauto's open merge requests, half are that line
and nothing else.

The body is yours:

```bash
upkeep publish pathauto 3223746 --description="## What

Two preg_replace() calls work on bytes while the string is UTF-8."

upkeep publish pathauto 3223746 --description-file=notes.md

upkeep publish pathauto 3223746 --no-footer
```

| Flag | What you get after the reference |
| --- | --- |
| nothing | `Opened with upkeep publish.` |
| `--description=<text>` | your text |
| `--description-file=<path>` | the file's contents |
| `--no-footer` | nothing — the reference alone |

**Giving a description replaces upkeep's note rather than appending to it.**
Somebody who wrote a description did not ask for a signature under it, and
needing a second flag to say so would be a poor trade. `--no-footer` is for
wanting neither.

Two sources for one body is a refusal rather than a precedence rule nobody
would remember, and a `--description-file` that cannot be read or is empty is a
refusal too — falling back to the default would publish a merge request whose
description is not the one you wrote.

`--title` works the same way, defaulting to drupal.org's `Issue #NNN: Title`
convention so the merge request pairs with its issue by the ordinary rule.

## Publishing from a script

`publish` writes to drupal.org under your name, so it refuses when there is no
terminal to confirm at:

```bash
upkeep publish pathauto 3223746 --yes
```

`--yes` is the same word `prune` uses for "I meant this". It is a flag rather
than a hard refusal because deliberate scripted publishing is a reasonable
thing to want — unlike `merge`, which refuses outright whatever you pass,
because the approval it consumes is one only a human can give.

What counts as a terminal is a real terminal, not a character device. That
distinction matters more than it sounds: `/dev/null` is a character device, and
it is what CI steps and many agent runners hand a child process, so a check
that accepted it would have left the gate open in exactly the case it exists
for.
