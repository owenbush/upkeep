# Structured output

`--json` makes a command emit one JSON document on stdout instead of a table.

```bash
upkeep dashboard --json
upkeep issues pathauto --json
upkeep check pathauto 183 --version=12 --json
```

It exists because the tables are built for a person and are hostile to a
program: titles are truncated to fit a column, an empty cell is an en dash, and
a column is as wide as the widest row needed. All of that is right for reading
and wrong for parsing, and none of it would announce a change.

So `--json` is not the table serialised. It is a separate payload carrying the
untruncated values, and the table stays free to change shape.

## Which commands have it

| Command | Document |
| --- | --- |
| `dashboard` | per-module summaries, or rows when a module is named |
| `issues` | open issues and what has been contributed to each |
| `patches` | issues carrying patch files |
| `check`, `patch:check` | the verdict, per check |
| `status` | the disk inventory |
| `review` | the site URL and the one-time login URL |
| `publish` | the merge request, and whether it was opened or updated |
| `patch:promote` | the commit, or the rejects when partial |

[The command reference](../commands.md#what-each-one-does-to-you) has a
`--json` column, generated from each command's own declaration, so it cannot
claim structured output a command does not have.

## The envelope

Every document has the same outer shape:

```json
{
  "upkeep": "1.0.0",
  "schema": 1,
  "command": "issues",
  "warnings": [],
  "data": {
    "issues": []
  }
}
```

- **`schema`** is an integer. Check it. It is bumped only when a field changes
  meaning or goes away; adding a field is not a bump, so treat unknown fields
  as ignorable rather than as an error.
- **`command`** says which shape `data` holds, so a document read from a log
  or a pipe still says what it describes. `dashboard` emits `dashboard` for the
  overview and `dashboard-rows` when a module narrows it.
- **`warnings` is not decoration.** upkeep's readers degrade by returning
  *less data*, which at the call site is indistinguishable from there being
  less data. A non-empty `warnings` means the counts and lists below it are
  **lower bounds**. It is always an array, so you can iterate it without a
  null check.

## Two rules worth relying on

**stdout holds exactly one JSON document and nothing else.** Progress, stage
lines, warnings and errors all go to stderr, in JSON mode as out of it. A
command that found nothing still emits a document, with an empty array rather
than silence — so "no open issues" and "the command stopped earlier" are
different answers rather than the same empty stdout.

**Exit codes are unchanged.** `0` upkeep did what you asked, `1` the work it
supervised failed, `2` upkeep could not do the job. A failed check emits its
payload *and* exits 1, so a consumer can act on the code without parsing
anything:

```bash
upkeep check pathauto 183 --json > verdict.json
case $? in
  0) echo "green" ;;
  1) jq -r '.data.checks[] | select(.status=="failed") | .check' verdict.json ;;
  2) echo "upkeep could not run it" >&2 ;;
esac
```

## Reading a dashboard row

Every row carries the two things the dashboard exists to give — a plain-English
status and the literal command to run next — alongside the structured facts:

```json
{
  "module": "pathauto",
  "branch": "8.x-1.x",
  "issue": { "nid": 3311669, "status": "review", "patches": 2 },
  "merge_request": { "iid": 40, "draft": false },
  "ci": { "status": "failed", "green": false },
  "local": { "10": "fail", "11": "pass" },
  "verdict": { "status": "BLOCKED", "reasons": ["ci-red"] },
  "ready_auto": false,
  "status": "CI failed",
  "next": "upkeep check pathauto 40 --version=10"
}
```

`local` is one entry per applicable core, using the same four words the table
prints: `pass`, `fail`, `stale`, `unchecked`.

`ci` is null when the merge request has no pipeline. That is different from
`ci_unavailable`, which is set when upkeep could not ask — a rate limit is not
the same answer as no CI, and collapsing them would report the wrong one.
