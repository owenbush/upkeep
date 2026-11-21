# GitLab token

Reading git.drupalcode.org needs no credential. A token is for writing — merging, commenting and publishing.

## GitLab token (PAT)

**A token is for writing, not for looking.** `dashboard`, `check` and `review`
read git.drupalcode.org anonymously when none is configured — public projects
serve their merge requests, refs and forks without one — and say so once.
Merging, commenting (`needs-work`) and `publish` need a real credential and
refuse without it. Two things anonymous reading costs: a *private* project
answers with "not found" rather than "not allowed", because GitLab hides
existence, so a module you can see while signed in reads as missing; and rate
limits are tighter.

Upkeep talks to the Drupal.org GitLab instance (git.drupalcode.org) for MR
listings, pipeline status, and fast-lane merges.

**Getting a token:** sign in at
[git.drupalcode.org](https://git.drupalcode.org) (use your Drupal.org
account), then go to **User Settings → Personal access tokens** and create
one. If GitLab only offers you the fine-grained flow, the permissions Upkeep
needs are: **Repository: read**, **Merge requests: read and write**, **CI/CD:
read**. On the classic scope picker, the `api` scope covers all of it.

**Configuring it** — Upkeep resolves the token in this order:

1. the `UPKEEP_GITLAB_TOKEN` environment variable, if non-empty;
2. the file `~/.config/upkeep/drupal-pat` (or
   `$XDG_CONFIG_HOME/upkeep/drupal-pat`), content trimmed.

For the file route:

```bash
mkdir -p ~/.config/upkeep
touch ~/.config/upkeep/drupal-pat
chmod 600 ~/.config/upkeep/drupal-pat
```

then paste the token into that file (a single line). Upkeep never prints the
token.

**The drupalorg-cli commands use the same token.** `issue:fork`,
`issue:get-access`, the assign family and the label family run
[drupalorg-cli](./install.md#requirements), and upkeep hands it the token it
resolved above as `DRUPALORG_GITLAB_TOKEN`, in that one process's environment:
never on the command line, and scrubbed from anything it prints. Those commands
post a comment on the issue's GitLab work item, so the token has to be allowed
to do that; the classic `api` scope covers it. If upkeep resolves no token,
drupalorg-cli falls back to `glab`'s configured one for git.drupalcode.org.
`issue:setup-remote`, `issue:fork-checkout` and `mr:logs` need no token.

Verify connectivity (also useful any time you want a quick look at a
module's open MRs and head pipeline):

```bash
upkeep api:probe conditions_helper
```
