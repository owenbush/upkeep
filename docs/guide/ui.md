# Browser UI

The same core behind a local web page, for when a table is the wrong shape for the question.

```bash
upkeep ui
```

serves the cockpit as a page on `127.0.0.1` and opens it. Filterable, modules
expandable in place — the progressive disclosure a terminal cannot do, which is
what makes a 244-row module readable.

Two views, mirroring the two questions:

- **Waiting for you** — the contribution rows. A **Check** button runs `check`
  or `patch:check` in the background and streams the output into a drawer.
- **Issue queue** — every open issue, contribution as a column, unclaimed work
  highlighted. **Start** opens a work branch; **Publish** pushes it and opens
  the merge request.

A finished job refreshes the rows it affected.

`--port=N` picks the port, `--no-open` suppresses the browser. It runs in the
foreground until Ctrl-C — there is no daemon, no pid file, and no port left
listening afterwards.

**If the page says the link is from a previous run:** the token is minted fresh
each time `upkeep ui` starts, so a tab from an earlier run carries a dead one.
No command re-prints the current link, but you rarely need one:

1. find the `127.0.0.1` address ending `?token=…` in your browser history — the
   newest is the live one; or
2. use the URL the running `upkeep ui` printed; or
3. stop it with Ctrl-C and run `upkeep ui` again.

Following a fresh launch URL is always enough — the token in it takes
precedence over whatever cookie the browser is still holding, and refreshes it.

Starting a second `upkeep ui` while the first still holds the port is refused
rather than half-started, because the port would keep answering with the *old*
token and any URL printed would already be dead.

**How it is kept safe, and why it bothers.** It runs on your machine, but the
port is reachable by anything else running there — including **any web page you
visit**, which can POST to `127.0.0.1` without being able to read the reply.
That matters because the process holds your GitLab PAT, which can push branches
and open merge requests in your name. So:

- the link carries a token minted per run and never written to disk, and every
  path is behind it, assets included;
- the page also sets it as an `HttpOnly`, `SameSite=Strict` cookie, which
  authenticates its own scripts and API calls and keeps that cookie off
  cross-site requests entirely;
- every refusal is an identical 404, except a plain visit to the page, which
  explains itself.

The token **stays in the address bar**. Removing it looked tidier and stranded
people: a restart mints a new one, and a tab whose URL had been cleaned had
nothing left to present and no way to find the current link except the terminal
it came from. Left there, the link is recoverable from your history. The trade
is that a screenshot of the window shows the token for the life of that run.
The browser never supplies a command line: it names one of three actions
(`check`, `patch-check`, `refresh`) whose parameters are validated into shapes
they already had to have. Binding to loopback keeps the port off the network,
but not away from other software on the machine — the token is the actual
barrier.

**Publish is the only thing that leaves your machine, and it asks first.** A
merge request is public the moment it exists, so the button opens a
confirmation rather than firing. `Start` needs no such guard: it is local, and
resumes an existing branch rather than resetting it, so pressing it twice loses
nothing.

**It cannot merge.** The Drupal Association stance is one human approval per
merge, and `merge --fast-lane`'s per-MR prompt is what earns that; a button
that posts an action name is not the same thing, and a table of checkboxes
beside a "merge selected" control is exactly the batch mode this tool refuses
to have. Publishing is not that call — it proposes work for review, which is
what the policy protects rather than restricts — but *merging* from the browser
still needs its own design first.

**It is a renderer, not a second tool.** What it shows comes from the same
`RowFactory` the CLI table and the fast-lane gate consume, and what it *does*
is run the `upkeep` binary as a subprocess — so exit codes, adapter behaviour
and secret redaction are inherited rather than reimplemented, and the page
cannot drift from the terminal.
