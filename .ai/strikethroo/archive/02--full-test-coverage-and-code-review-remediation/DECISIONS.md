# Execution Decisions — Plan 02

Rulings given by the maintainer during execution, in response to open questions
raised by the task 5 review. These are binding on tasks 6-9 and on every
downstream task that tests or documents the affected behaviour.

## D1 — Adapter boundary: full DI refactor

**Question**: `grep -r "ddev" src/ --exclude-dir=Adapter` passes only because
the class name capitalises the D. Five command classes construct
`DdevContribAdapter` directly (`AbstractMrCommand:147`, `EnvPathCommand:113`,
`DevCommand:148`, `PruneCommand:211`, `ExecCommand:130`), so the documented
invariant is nominally satisfied but substantively breached.

**Ruling**: Perform a **full DI refactor**. Nothing constructs its own engine
dependency. Introduce a service factory / composition root so commands receive
`EngineAdapterInterface` by injection rather than instantiating a concrete
engine.

**Noted at decision time**: this was flagged as going beyond what the recorded
finding requires. The maintainer chose it explicitly. It is therefore in scope,
and the usual "a change qualifies only if it resolves a recorded finding" rule
is relaxed for this specific refactor only.

**Consequences**:
- The `CLAUDE.md` adapter-boundary guard must be updated to a case-insensitive
  form (`grep -ri`), since the current guard gives false assurance. Task 19
  documents this.
- Removes five duplicated construction sites.
- Commands become constructible with a fake engine directly, which simplifies
  tasks 12 and 15.

## D2 — Exit-code contract: extend to all commands

**Question**: The documented 0 pass / 1 check failed / 2 infrastructure
contract is referenced by only 3 of 21 command classes. "No token" currently
exits 1 from six commands but 2 from check/review.

**Ruling**: **Extend the contract to all commands.** Every command maps its
failures onto the 0/1/2 contract, expressed in one place rather than duplicated.
No exemption for `upkeep exec`.

**Consequences**:
- The "no token" inconsistency is resolved to a single code across all
  commands; whichever is chosen must be applied uniformly and documented.
- `upkeep exec` no longer passes the child's exit code through untouched. This
  is a deliberate behaviour change and must be documented in `README.md` by
  task 19, since it affects anyone scripting against `exec`.
- Task 12's end-to-end assertions test the extended contract, not the current
  inconsistent behaviour.

## D3 — Non-lens defects are in scope

**Question**: The review surfaced confirmed defects outside the security and
best-practice lenses.

**Ruling**: **Fix them in this plan.** A confirmed defect is in scope for tasks
6-9 regardless of which lens surfaced it.

**Explicitly included**:
- The two Critical fatal errors in `ModulesAddCommand`: references to the
  undefined constants `TokenResolver::ENV_VAR` and `::CONFIG_PATH_HINT` on the
  no-token path, and `$projects->message()` called where `ApiFailure` exposes a
  public promoted property rather than a method. Both verified present.
- `prune --older-than` reading `last_used_at`, which
  `InventoryScanner::readEnvMeta()` reads but nothing ever writes, silently
  falling back to `created_at` and so able to delete actively-used
  environments.
- `RegistryEditor::add()` writing a sibling temp file with the final bytes,
  deleting it, then writing the registry a second time instead of `rename()`ing
  — a crash in that window destroys the user's registry.
- The 11 `file_put_contents` call sites in `src/` that do not check their
  return value, five of which produce false success claims (including `init`
  reporting "Cockpit created" with no `registry.yml`).

**Still out of scope**: improvements that are not defects. The scope rule holds
for everything except D1's refactor and the confirmed defects listed above.
