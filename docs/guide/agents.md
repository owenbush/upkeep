# Agents

Letting an AI agent drive upkeep, and where to make it stop.

upkeep happens to suit this better than most CLIs, for reasons that were not
designed for it. Exit codes are a three-value contract, so an agent can tell
*your work failed* from *you used the tool wrong*. Refusals name the command to
run. Every dashboard row ends in a next command. Reading needs no credential,
so an agent can triage without being trusted with anything.

What it needed adding was structured output and a published skill.

## Installing the skill

```bash
npx skills add owenbush/upkeep
```

That is the route, whatever you use. The [skills CLI](https://skills.sh) knows
where each of seventy-odd harnesses keeps its skills, detects which ones you
have installed, and takes `--agent` if you want to choose. upkeep deliberately
does not try to do that job: it would mean carrying a table of directories for
every agent on the market and keeping it current.

Check what a repository offers before installing anything:

```bash
npx skills add owenbush/upkeep --list
```

**Without Node, or offline**, the skill ships inside the release archive at
`skills/upkeep/SKILL.md`. Copy it to wherever your agent reads skills from —
your harness's own documentation is the authority on that path, and the skills
CLI's [agents table](https://github.com/vercel-labs/skills/blob/main/docs/agents.md)
lists them if you would rather look it up.

The file is also in the repository at the same path, so a clone has it too.

## What the skill tells it

The command list is already in [Which command do I want?](./which-command.md).
The skill carries the judgement that is not in any help text:

- **Stop before anything that writes to drupal.org.** `publish`, `needs-work`
  and `merge` act in public under your name. Prepare the work, report the
  command, let a person run it.
- **Which branches survive.** `check`, `review`, `patch:apply` and
  `patch:check` force-update the branch they own, so a commit there is a commit
  waiting to be destroyed. `start` and `checkout` give branches upkeep never
  resets.
- **Exit 2 is your own mistake** — never retry it unchanged.
- **Checks cost minutes.** Read the JSON you have rather than running it again.
- **A green drupal.org pipeline does not mean CI found nothing**, and **CI tests
  the merge ref rather than the branch**. Both are things an agent otherwise
  reports wrongly and confidently.

## The guard is in the binary, not only in the skill

A skill is instructions, and instructions can be misread. So the two commands
an agent could most damagingly reach refuse on their own:

```bash
echo | upkeep publish pathauto 3223746
# Error: publishing pushes your branch and opens a merge request on
# drupal.org under your name, and there is no terminal here to confirm at.
# Re-run with --yes if that is what you meant
```

`merge` refuses outright, with no flag to pass: the approval it consumes is one
only a human can give. `publish` and `needs-work` take `--yes`, because
deliberate scripted publishing is a reasonable thing to want.

If an agent passes `--yes` to get past one of those without being asked to,
that is the agent doing something wrong, not the gate being inconvenient.

## Reading the output

Use `--json` and never parse the tables. See
[Structured output](../reference/json.md) for the envelope, the `warnings`
field — which means the lists are lower bounds — and the exit-code contract.

```bash
upkeep dashboard --json
upkeep check pathauto 183 --version=12 --json
```
