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
