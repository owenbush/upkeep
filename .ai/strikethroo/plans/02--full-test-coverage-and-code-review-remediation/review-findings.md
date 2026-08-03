---
plan: 02--full-test-coverage-and-code-review-remediation
produced_by: task 05 — Conduct the security and best-practice review
date: 2026-08-02
status: complete
---

# Security and Best-Practice Review Findings

Read-only review of 99 files under `src/`, plus `bin/upkeep` and `tests/`, across
three lenses: credential handling, subprocess invocation, filesystem path and
result-file handling, and design/best practice. The PHPStan level-max report
(302 errors, task 1 baseline) was mined for errors that indicate **real
defects** rather than missing annotations; those are recorded here.

**Anchoring convention.** Task 4 (PSR-12 reformatting) is running concurrently
and is shifting line numbers across `src/`, `bin/`, and `tests/`. Every finding
is therefore anchored to a **class and method name** plus a short verbatim
quote. Line numbers, where given, are marked *approx.* and are the pre-task-4
positions.

**Scope rule (from PRE_PLAN, restated).** A change qualifies for tasks 6–9 only
if it resolves a finding recorded here. Improvements that are not defects are
recorded as `OUT OF SCOPE` with a stated reason rather than assigned to a
remediation task.

---

## Summary

| Lens | Critical | High | Medium | Low | Total |
| --- | --- | --- | --- | --- | --- |
| security | 1 | 2 | 15 | 7 | 25 |
| best-practice | 1 | 1 | 16 | 12 | 30 |
| **Total** | **2** | **3** | **31** | **19** | **55** |

| Owning task | Count | IDs |
| --- | --- | --- |
| Task 6 — credential handling and subprocess output leakage | 9 | SEC-CRED-01…04, SEC-PROC-01…05 |
| Task 7 — filesystem path and result-file handling | 16 | SEC-FS-01…16 |
| Task 8 — GitLab error-taxonomy consistency | 9 | BP-GL-01…09 |
| Task 9 — command-class duplication and adapter-boundary compliance | 16 | BP-CMD-01…16 |
| OUT OF SCOPE (with stated reason) | 5 | OOS-01…05 |

Severity definitions used here: **Critical** = fatal at runtime on a reachable
path; **High** = silent data loss, false success reporting, or a
credential/destructive-operation hazard; **Medium** = incorrect behaviour,
unenforced documented invariant, or a design defect that will cause drift;
**Low** = correctness or consistency defect with bounded consequence.

---

## Part A — Credential handling: the complete trace from `TokenResolver::resolve()`

`Gitlab\TokenResolver::resolve()` returns `?string`. The value is assigned to a
local `$token` at **eight** sites and, in every one of them, its **only**
onward use is the second constructor argument of `Gitlab\GitlabClient`:

| Consumer (class::method) | Onward use of the resolved token |
| --- | --- |
| `Command\AbstractMrCommand::resolveContext()` | `new GitlabClient(HttpClient::create(), $token)` |
| `Command\ApiProbeCommand::execute()` | `new GitlabClient(HttpClient::create(), $token)` |
| `Command\DashboardCommand::buildClient()` | `new GitlabClient(HttpClient::create(), $token)` |
| `Command\MergeCommand::buildClient()` | `new GitlabClient(HttpClient::create(), $token)` |
| `Command\NotesCommand::execute()` | `new GitlabClient(HttpClient::create(), $token)` |
| `Command\IssueCommand::buildGitlabClient()` | `new GitlabClient(HttpClient::create(), $token)` |
| `Command\NeedsWorkCommand::buildGitlabClient()` | `new GitlabClient(HttpClient::create(), $token)` |
| `Command\ModulesAddCommand::buildClient()` | `new GitlabClient(HttpClient::create(), $token)` |
| `Command\PatchesCommand::buildGitlabClient()` | `new GitlabClient(HttpClient::create(), $token)` |

Inside `GitlabClient` the token is a `private readonly` promoted property
carrying `#[\SensitiveParameter]`, and it is referenced at exactly one place —
`GitlabClient::request()`:

```php
$response = $this->http->request($method, $url, $extraOptions + [
    'headers' => ['PRIVATE-TOKEN' => $this->token],
]);
```

### Sink-by-sink verdict

| Sink | Can the token reach it? | Evidence |
| --- | --- | --- |
| Console output on the **missing-token** path | **No** | All eight guards print only `describeSources()` or `DEFAULT_ENV_VAR` + `defaultConfigFile()`. No token exists at that point by construction (`$token === null`). |
| `GitlabClient::httpErrorMessage()` | **No** | Quotes only the response body's `message`/`error` field plus the status and browser URL. Request headers are never read. |
| `TransportError` messages | **No** | Three constructions: `'HTTP transport failure: ' . $e->getMessage()` (curl/TLS text, no headers), `sprintf('Unexpected non-JSON-object response (HTTP %d) from %s', $status, $url)`, and `sprintf('Undecodable response body from %s: %s', $url, ...)`. The token travels in a header, never in `$url`. |
| `EndpointClosed` / `NotFound` / `RateLimited` messages | **No** | Built from status, browser URL, and `Retry-After` only. |
| PHP stack traces (uncaught exception, `-vvv`) | **No** | `#[\SensitiveParameter]` on `GitlabClient::__construct`'s `$token` redacts it from traces; PHP does not place local variables in traces at all. |
| Persisted files (`<cockpit>/results/**`, `<cockpit>/cache/dashboard/**`, `registry.yml`, artifact meta, `.upkeep-env.yml`) | **No** *directly* | No writer receives the token. But see Part B — child-process output is persisted, and the token is in the child environment. |
| `Process` **argument vector** | **No** | Verified at all six `new Process(` sites — none receives token material; the argv is built from constants, validated project paths, and git refspecs. |
| `Process` **child environment** | **YES** | See SEC-PROC-01. This is the one reachable path. |
| `ProcessRunner::run()` `AdapterException` message | **YES, conditionally** | Via child output only, never via `getCommandLine()`. See SEC-PROC-01/02. |
| `ProcessRunner` log closures (`start()`, `capture()`) | **YES, conditionally** | Same mechanism. |
| `CapturedProcess::$output` → `CheckResult::$output` → `ResultsCache::store()` JSON on disk | **YES, conditionally** | Same mechanism; this is the one sink that **persists**. |

**Overall credential verdict:** the "never prints or persists a token" property
holds for every *direct* path, including all error paths. The single residual
exposure is indirect and is recorded as SEC-PROC-01.

---

## Part B — Subprocess: the `ProcessRunner` token-leak reachability verdict

### B.1 Shell interpolation — confirmed absent

Every `Process` construction site in `src/` and `bin/` uses **list-form argv**:

| Site | Command |
| --- | --- |
| `Adapter\ProcessRunner::start()` | `new Process($command, $cwd, timeout: $timeout)` |
| `Adapter\ProcessRunner::capture()` | `new Process($command, $cwd, timeout: $timeout)` |
| `Command\ExecCommand::execute()` | `new Process($cmd, $path, timeout: null)` |
| `Command\IssueCommand::execute()` | `new Process([$opener, $issueUrl])` |
| `Command\NeedsWorkCommand::execute()` | `new Process([$opener, $issueUrl])` |
| `Maintenance\DiskUsage` (constructor path) | `new Process(['du', '-sk', $path], timeout: 300)` |
| `BaseArtifact\BaseArtifactBuilder::run()` | `new Process($command, $cwd, timeout: self::PROCESS_TIMEOUT)` |

There is **no** `Process::fromShellCommandline`, no string commandline, no
`shell_exec`/`exec`/`proc_open`/`system`/`passthru` in `src/` or `bin/`. The
three recursive deletions (`BaseArtifactBuilder::build()`,
`DdevContribAdapter::teardownProject()`, `ThrowawaySite::teardown()`) all pass
the path as a **separate argv element**, so path metacharacters are inert.

Caveat, recorded for completeness and **not** a defect: on PHP builds with
`--enable-sigchild`, `Symfony\Component\Process\Process::start()` converts the
argv to a shell line via `buildShellCommandline()`, which applies
`escapeshellarg()` per element. The no-injection property survives.

### B.2 The reachability verdict

**Question:** can any caller place token material into a `Process` argument
vector, a child environment variable, or child output that `ProcessRunner`
then interpolates into an `AdapterException` via `getCommandLine()` or the
combined output?

**Answer, three parts:**

1. **Argument vector — NO.** No call site passes the PAT to any `Process`. The
   `getCommandLine()` half of the `AdapterException` message is therefore
   token-free by construction.

2. **Child environment variable — YES.** `ProcessRunner` constructs
   `new Process($command, $cwd, timeout: $timeout)` with `$env = null`.
   Symfony's `Process::start()` then does
   `$env += $this->getDefaultEnv();`, and `getDefaultEnv()` returns
   `getenv()` — **the entire parent environment**. When the operator supplies
   the PAT via `UPKEEP_GITLAB_TOKEN` (the first and documented source), that
   variable is present in the environment of **every** child process the tool
   spawns. `ProcessRunner` exposes no way to alter this.

3. **Child output → exception message, logs, and persisted results — YES,
   conditionally on (2).** Any child that echoes its own environment (a
   `set -x` trace, a `printenv`, a composer/PHPUnit plugin dumping `$_ENV`, a
   crash handler, or simply `upkeep exec <module> -- env`) writes the token to
   stdout/stderr, from where it flows into all four reporting sinks:
   `ProcessRunner::run()`'s `AdapterException` message, the `start()` and
   `capture()` log closures (console at `VERBOSITY_VERBOSE`),
   `CheckCommand::renderSummary()`'s 2000-byte failure excerpt, and — the one
   that persists — `ResultsCache::store()`'s 4000-byte `'output'` excerpt
   written world-readable to `<cockpit>/results/<module>/<iid>/<core>/<sha>.json`.

**Verdict: not reachable via argv; reachable via inherited environment plus
child output, with one persisting sink.** Severity Medium (requires a
cooperating child), but the mitigation is cheap and complete — see SEC-PROC-01.

---

## Part C — Findings

### Lens: security — credential handling

---

#### SEC-CRED-01 — `modules:add` dies with a fatal `Error` on the missing-token path

- **File / anchor:** `src/Command/ModulesAddCommand.php` — `ModulesAddCommand::buildClient()` (approx. L157–160)
- **Lens:** security (credential handling, error path)
- **Severity:** Critical
- **Evidence:**
  ```php
  $io->error(sprintf('No GitLab token found. Configure one of: env var %s, config file %s.', TokenResolver::ENV_VAR, TokenResolver::CONFIG_PATH_HINT));
  ```
  `Gitlab\TokenResolver` is `final readonly` and declares exactly one constant,
  `DEFAULT_ENV_VAR`. Neither `ENV_VAR` nor `CONFIG_PATH_HINT` exists anywhere
  in `src/` or `tests/`. PHPStan confirms:
  `Access to undefined constant Upkeep\Gitlab\TokenResolver::CONFIG_PATH_HINT`
  and `...::ENV_VAR` (`identifier=classConstant.notFound`).
- **Impact:** `upkeep modules:add` with no token configured throws an uncaught
  `Error: Undefined constant ...` instead of the intended guidance. The line is
  reached whenever no `GitlabClient` is injected and `resolve()` returns null —
  i.e. exactly the first-run case. No test covers it because
  `ModulesAddCommandTest` always injects a client.
- **Proposed resolution:** Replace with the single shared token-guard helper
  introduced by SEC-CRED-03, which uses `$resolver->describeSources()`.
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

#### SEC-CRED-02 — No permission check on `~/.config/upkeep/drupal-pat` (DECISION RECORDED)

- **File / anchor:** `src/Gitlab/TokenResolver.php` — `TokenResolver::resolve()`
- **Lens:** security (credential handling)
- **Severity:** Medium
- **Evidence:**
  ```php
  if (is_readable($this->configFile)) {
      $content = trim((string) file_get_contents($this->configFile));
  ```
  No `stat()`, no mode inspection, no symlink check. A PAT file left at the
  common `0644` (the mode a plain `echo $PAT > ~/.config/upkeep/drupal-pat`
  produces under the default `umask 022`) is readable by every local account.
- **DECISION: WARN, do not refuse.** Recorded explicitly, with reasoning:
  - The file is **created by hand by the operator**, so a group/world-readable
    mode is the *default outcome* of the documented setup instructions, not an
    anomaly. Refusing would break first-run setup for most users.
  - Precedent supports warning: `ssh` refuses because `ssh-keygen` always
    creates `0600`, making a permissive mode an anomaly. Files that users
    create by hand — `~/.netrc` (curl warns), `git-credential-store`, `gh`,
    Docker's `config.json` — warn or ignore. upkeep's file is in the second
    category.
  - Blast radius is bounded: the PAT is a read/merge token on a
    block-by-default public GitLab, and the DA one-human-approval-per-MR policy
    means a stolen token cannot be used for unattended merges through this tool.
- **Proposed resolution:** In `TokenResolver::resolve()`, after a successful
  file read, `stat()` the file and when `($mode & 0o077) !== 0` emit one
  warning line through an **injected** warning sink
  (`\Closure(string): void`, defaulting to a no-op so the class stays I/O-pure
  and testable). Wording must name the path and the exact fix
  (`chmod 600 <path>`) and must never include token material. Do **not** refuse
  and do **not** add a strict mode. Cover both branches with tests.
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

#### SEC-CRED-03 — Four divergent "no GitLab token" messages, one of them silent

- **File / anchor:** `src/Command/` — `AbstractMrCommand::resolveContext()`,
  `ApiProbeCommand::execute()`, `DashboardCommand::buildClient()`,
  `MergeCommand::buildClient()`, `NotesCommand::execute()`,
  `IssueCommand::buildGitlabClient()`, `NeedsWorkCommand::buildGitlabClient()`,
  `ModulesAddCommand::buildClient()`, `PatchesCommand::buildGitlabClient()`
- **Lens:** security (credential handling — reporting consistency)
- **Severity:** Medium
- **Evidence:** Four wordings for one condition.
  - V1 (×4, verbatim identical): `'No GitLab token found. Configure one of: %s. (The token is never printed or logged.)'` with `describeSources()`.
  - V1b (`AbstractMrCommand`): `'No GitLab token found; MR resolution needs one. Configure one of: %s. ...'` thrown as `WorkflowException` → exit **2**, whereas V1 exits **1**.
  - V2 (`IssueCommand`, `NeedsWorkCommand`): re-derives `describeSources()`'s output from `TokenResolver::DEFAULT_ENV_VAR` + `TokenResolver::defaultConfigFile()`. Because it reports the *default* config path rather than the resolver's actual `$configFile`, it would lie for any resolver built with a custom path.
  - V3 (`ModulesAddCommand`): fatal — see SEC-CRED-01.
  - V4 (`PatchesCommand`): `if ($token === null) { return null; }` — **no diagnostic at all**; the user sees only a generic note about missing cross-referencing.
- **Impact:** The credential-configuration message is the tool's primary
  security-relevant UX. Four wordings, two exit codes, and one silent path mean
  the operator's experience of "I have no token" depends on which command they
  typed.
- **Proposed resolution:** One shared resolver-and-guard seam (e.g.
  `GitlabClientFactory::fromResolvedToken(TokenResolver, SymfonyStyle): ?GitlabClient`,
  or a trait) used by all nine sites, emitting one message built from
  `describeSources()` and returning one exit code. Fixing this also removes
  the SEC-CRED-01 line. Coordinate with BP-CMD-03 (exit-code contract).
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

#### SEC-CRED-04 — Resolved token is not shape-validated; a multi-line file yields an opaque failure

- **File / anchor:** `src/Gitlab/TokenResolver.php` — `TokenResolver::resolve()`
- **Lens:** security (credential handling)
- **Severity:** Low
- **Evidence:** `$content = trim((string) file_get_contents($this->configFile));` —
  `trim()` strips surrounding whitespace only. A file containing a `.netrc`-style
  block, a `KEY=value` line, or a trailing comment is returned whole and used as
  a `PRIVATE-TOKEN` header value. A header value containing `\n` is rejected by
  the HTTP client with an error that names neither the cause nor the file.
- **Proposed resolution:** Take the **first non-empty line** only, and reject
  (returning null with a specific message) a value containing characters
  invalid in an HTTP header value. Never echo the value in the diagnostic.
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

### Lens: security — subprocess

---

#### SEC-PROC-01 — `UPKEEP_GITLAB_TOKEN` is inherited by every child process; child output is logged and persisted

- **File / anchor:** `src/Adapter/ProcessRunner.php` — `ProcessRunner::start()`,
  `ProcessRunner::capture()` (both `new Process($command, $cwd, timeout: $timeout)`
  with no `$env`); sinks in `ProcessRunner::run()`,
  `Command\CheckCommand::renderSummary()`, `Results\ResultsCache::store()`
- **Lens:** security (credential handling × subprocess)
- **Severity:** Medium
- **Evidence:** Symfony `Process::start()` performs
  `$env += $this->getDefaultEnv();` where `getDefaultEnv()` returns `getenv()`.
  With `$env = null` (every construction site in this codebase), the child
  receives the complete parent environment including the PAT. The output of that
  child then reaches:
  ```php
  throw new AdapterException(sprintf(
      "Command failed (%s): %s\n%s",
      $process->getExitCode() ?? -1,
      $process->getCommandLine(),
      trim($process->getErrorOutput() . "\n" . $process->getOutput()),
  ));
  ```
  and `($this->log)('  ' . $line)` in both `start()` and `capture()`, and
  `'output' => substr($check->output, 0, self::OUTPUT_EXCERPT_BYTES)` in
  `ResultsCache::store()` — a world-readable file on disk (see SEC-FS-05).
- **Proposed resolution (two layers, both required):**
  1. **Scrub at the source.** Give `ProcessRunner` an explicit child
     environment that removes the credential variable. Symfony honours `false`
     as "unset": `new Process($command, $cwd, [TokenResolver::DEFAULT_ENV_VAR => false], ...)`
     — verified against `Process::start()`'s
     `if (false !== $v && ...) { $envPairs[] = $k.'='.$v; }`. Apply at every
     `new Process(` site, including `ExecCommand`, `DiskUsage`, and
     `BaseArtifactBuilder`. This makes the leak unreachable rather than merely
     redacted.
  2. **Redact at the reporting boundary.** Add a single redaction function
     applied to child output before it enters an exception message, a log
     closure, a rendered excerpt, or a persisted result, so that a token
     arriving by any future route is masked. Cover with a test that seeds a
     sentinel token and asserts it appears in none of the four sinks (this is
     also self-validation step 10 of the plan).
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

#### SEC-PROC-02 — `ProcessRunner::run()` interpolates the full, unbounded child output into an exception message

- **File / anchor:** `src/Adapter/ProcessRunner.php` — `ProcessRunner::run()`
- **Lens:** security (subprocess output handling)
- **Severity:** Medium
- **Evidence:** `trim($process->getErrorOutput() . "\n" . $process->getOutput())`
  — no length bound, no redaction. A failing `composer install` or `ddev start`
  produces megabytes of output, all of which becomes a single exception message
  that commands then hand to `$io->error()`.
- **Impact:** Independent of the token question, this is an unbounded blast of
  untrusted child output into an error message. Combined with SEC-PROC-01 it is
  the leak vector; on its own it is a usability and log-hygiene defect.
- **Proposed resolution:** Bound the interpolated output to a documented tail
  (mirroring `CheckCommand::EXCERPT_BYTES`), run it through the SEC-PROC-01
  redactor, and state the truncation in the message.
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

#### SEC-PROC-03 — `run()` and `tryRun()` let `ProcessTimedOutException` escape the adapter's error boundary

- **File / anchor:** `src/Adapter/ProcessRunner.php` — `ProcessRunner::run()`,
  `ProcessRunner::tryRun()`, `ProcessRunner::start()` (contrast with
  `ProcessRunner::capture()`)
- **Lens:** security (subprocess error handling) / best-practice
- **Severity:** Medium
- **Evidence:** Only `capture()` handles the timeout:
  ```php
  } catch (\Symfony\Component\Process\Exception\ProcessTimedOutException) {
      $timedOut = true;
  }
  ```
  `start()` — used by both `run()` and `tryRun()` — has no such catch. A
  provisioning step that hits `DEFAULT_TIMEOUT` (3600s) therefore throws a raw
  Symfony exception past the `AdapterException` boundary that every command's
  `catch (WorkflowException | AdapterException | RegistryException $e)` relies
  on, producing a stack trace and an unmapped exit code instead of the
  documented `ExitCode::INFRASTRUCTURE`.
- **Proposed resolution:** Catch `ProcessTimedOutException` in `start()` and
  convert it to `AdapterException` naming the command and the elapsed timeout;
  `tryRun()` should return `null`. Cover both.
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

#### SEC-PROC-04 — Unescaped interpolation into a `bash -c` script body in the check commands

- **File / anchor:** `src/Adapter/DdevContribAdapter::checkCommand()` (approx. L604, L623, L631)
- **Lens:** security (subprocess)
- **Severity:** Low
- **Evidence:**
  ```php
  $modulePath = sprintf('"$DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH"/%s', $environment->moduleName);
  ...
  sprintf('phpcs -s --report-full --report-summary --report-source %s --ignore=*/.ddev/*', $modulePath),
  ...
  'phpstan analyze ' . $modulePath,
  ```
  The argv is list-form (`['ddev', 'exec', 'bash', '-c', <script>]`), so there
  is no injection *into the argv*. But `<script>` is a shell script into which
  `$environment->moduleName` is interpolated **unquoted and unescaped**.
- **Assessment:** Not currently exploitable — `Environment::$moduleName` is
  produced on paths gated by `ProjectName::for()`'s `/^[a-z][a-z0-9_]*$/`. It is
  recorded because the safety depends on a validator two classes away rather
  than on the construction itself, and because SEC-FS-12 shows that validator is
  not universally applied.
- **Proposed resolution:** Pass the module path as a positional parameter to the
  script (`['bash', '-c', $script, '--', $modulePath]`, referenced as `"$1"`) so
  the value never enters the script text, or apply `escapeshellarg()`.
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

#### SEC-PROC-05 — `ExecCommand` runs an arbitrary user command with the credential in its environment

- **File / anchor:** `src/Command/ExecCommand.php` — `ExecCommand::execute()`
- **Lens:** security (subprocess)
- **Severity:** Low
- **Evidence:** `$process = new Process($cmd, $path, timeout: null);` where
  `$cmd` is the user's `IS_ARRAY` `cmd` argument. Environment inheritance is the
  same as SEC-PROC-01, so `upkeep exec <module> -- printenv` prints the PAT.
- **Assessment:** This is the operator running their own command as themselves,
  so it is not a privilege boundary. It is recorded because it is the simplest
  demonstration of SEC-PROC-01 and because the SEC-PROC-01 fix must cover this
  site too (it is outside `ProcessRunner`).
- **Proposed resolution:** Covered by SEC-PROC-01 layer 1 — scrub the credential
  variable here as well, and document that `upkeep exec` does not forward it.
- **Owning task:** **Task 6 — credential handling and subprocess output leakage**

---

### Lens: security — filesystem

---

#### SEC-FS-01 — The `$HOME` containment invariant for the projects root is documented in four places and enforced in none

- **File / anchor:** `src/Adapter/ProjectsRoot.php` — `ProjectsRoot::resolve()`
- **Lens:** security (filesystem) / best-practice
- **Severity:** Medium
- **Evidence:**
  ```php
  if ($explicit !== null && $explicit !== '') {
      return $explicit;
  }

  $env = getenv(self::ENV_VAR);
  if ($env !== false && $env !== '') {
      return $env;
  }
  ```
  Both `--projects-root` and `$UPKEEP_PROJECTS_ROOT` are returned **verbatim** —
  no canonicalisation, no absoluteness check, no `$HOME` prefix check. The only
  thing actually refused is the *implicit* temp-dir fallback when `$HOME` is
  unset. An explicit `--projects-root=/tmp/x` is accepted silently. The
  invariant is asserted in the `ProjectsRoot` class docblock, in that method's
  own exception text ("Refusing to fall back to a temp dir — Docker providers
  only mount the home directory"), in `CLAUDE.md`, and in `README.md`.
  `grep -rn 'realpath' src bin/upkeep` returns **no matches**; there is no
  containment check of any kind anywhere in the codebase.
- **Impact:** The documented failure mode ("a projects root under /tmp can never
  start") arrives much later as an opaque ddev/Docker mount error rather than as
  the clear up-front refusal the docs promise.
- **Proposed resolution:** `ProjectsRoot::resolve()` is the single chokepoint —
  every caller (`AbstractMrCommand::adapter()`, `DevCommand`, `EnvPathCommand`,
  `ExecCommand`, `PruneCommand`, `StatusCommand`) routes through it. Canonicalise
  the resolved path and assert it is under `$HOME`, throwing `AdapterException`
  with the Docker-mount reason when it is not. Apply the same to
  `BaseArtifactsBuildCommand`'s `--scratch-dir`, whose help text makes the same
  unenforced promise. If an escape hatch is wanted, make it an explicit opt-out
  flag rather than silence.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-02 — No path canonicalisation anywhere; traversal and symlink handling are absent by omission

- **File / anchor:** `src/Cockpit/Cockpit.php` — `Cockpit::resolve()`;
  `src/Adapter/ProjectsRoot.php` — `ProjectsRoot::resolve()`;
  `src/Command/InitCommand.php` — `InitCommand::execute()`
- **Lens:** security (filesystem)
- **Severity:** Medium
- **Evidence:** `realpath()`, `readlink()`, and `SplFileInfo::getRealPath()` do
  not appear in `src/` or `bin/upkeep` at all. The one `is_link()` in the
  codebase is an unrelated invariant assertion in
  `DdevContribAdapter::requireWorkingCopyBranch()`. Every path in the system is
  raw string concatenation from `Cockpit::$root` or `ProjectsRoot`'s return
  value, so `..` segments survive into every derived path.
- **Consequences observed:**
  - `Maintenance\PruneSelector::protectionReason()` protects the base-artifact
    and fixture roots with a **lexical** `str_starts_with($item->path, $prefix . '/')`.
    Because neither side is canonicalised, protection is string equality, not
    path identity — a candidate reaching the executor under an equivalent-but-
    differently-spelled path (symlinked projects root, `..` segments) would not
    match the protected prefix.
  - `Maintenance\InventoryScanner::snapshots()` uses
    `glob($projectPath . '/' . SnapshotLayout::MATERIALIZED_DIR . '/*.sql')`;
    `glob` resolves through symlinked path components, so a symlinked
    `materialized/` directory would enumerate — and `PruneExecutor` would
    `unlink` — real files outside the tree. (Bounded: only `*.sql` files, and
    `unlink` on a symlink removes the link, not the target.)
- **Proposed resolution:** Canonicalise `Cockpit::$root` and the projects root
  once at resolution, then compare canonicalised paths in
  `PruneSelector::protectionReason()`. Add an `is_link()` guard on the
  `materialized/` directory in `InventoryScanner::snapshots()`.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-03 — `ResultsCache` builds paths from unvalidated module name, core version, and a remote-supplied SHA, then `mkdir -p`s them

- **File / anchor:** `src/Results/ResultsCache.php` — `ResultsCache::entryDir()`,
  `ResultsCache::store()`, `ResultsCache::find()`, `ResultsCache::latest()`
- **Lens:** security (filesystem — traversal)
- **Severity:** Medium
- **Evidence:**
  ```php
  private function entryDir(string $module, int $mrIid, string $coreMajor): string
  {
      return $this->resultsDir . '/' . $module . '/' . $mrIid . '/' . $coreMajor;
  }
  ```
  and `file_put_contents($dir . '/' . $sha . '.json', ...)` after
  `mkdir($dir, 0o755, true)`. Component provenance:
  - `$module` — a **key of the `modules:` mapping in `registry.yml`**.
    `Cockpit\ModuleRegistry::buildModule()` validates `project` and
    `core_versions` but **never validates the name**:
    `$modules[$name] = self::buildModule($path, (string) $name, $definition);`
  - `$coreMajor` — an entry of that module's `core_versions`, checked only for
    list membership by `Workflow\MrContextResolver::selectCoreVersion()`.
  - `$sha` — `MergeRequest::fromApi()`'s
    `headSha: isset($data['sha']) ? (string) $data['sha'] : null;` — an
    **unvalidated remote value from the GitLab API**, used directly as the
    filename. Not hex-checked anywhere.
  - `$mrIid` — `int`, and pre-validated `/^\d+$/`. Safe.
- **Assessment:** The asymmetry is the defect. Three strict validators exist and
  work — `Adapter\ProjectName::for()` (`/^[a-z][a-z0-9_]*$/` + `/^\d+$/`),
  `BaseArtifact\ArtifactLayout::assertVersion()` (correctly funnelled through
  `versionDir()` so the whole layout inherits it), and
  `InventoryScanner::PROJECT_DIR_PATTERN`. `ResultsCache` bypasses all of them
  while consuming the same `$module->name`.
- **Proposed resolution:** Validate the machine name and the core version in
  `ModuleRegistry::buildModule()` at load, reusing `ProjectName`'s patterns —
  this closes SEC-FS-03, SEC-FS-04, and SEC-FS-12 at the source. Additionally
  assert `$sha` is `/^[0-9a-f]{7,64}$/` in `ResultsCache` before it becomes a
  filename.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-04 — `DashboardCache` builds its path from the same unvalidated module name

- **File / anchor:** `src/Dashboard/DashboardCache.php` — `DashboardCache::path()`, `DashboardCache::save()`
- **Lens:** security (filesystem — traversal)
- **Severity:** Medium
- **Evidence:** `return $this->cacheDir . '/' . $module . '.json';` with
  `$module` again a `registry.yml` mapping key. Callers:
  `DashboardCommand::execute()` and `PatchesCommand::collectMrIssueNids()`.
- **Proposed resolution:** Closed by the SEC-FS-03 fix at `ModuleRegistry`.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-05 — Files carrying PAT-scoped remote data and raw check output are written world-readable

- **File / anchor:** `src/Results/ResultsCache.php` — `ResultsCache::store()`;
  `src/Dashboard/DashboardCache.php` — `DashboardCache::save()`
- **Lens:** security (filesystem — permissions)
- **Severity:** Medium
- **Evidence:** No `chmod()` exists anywhere in `src/`. Directories are created
  with an explicit `0755`/`0o755`; files get the umask default, typically
  `0644`. Content:
  - `<cockpit>/results/**/<sha>.json` embeds
    `'output' => substr($check->output, 0, self::OUTPUT_EXCERPT_BYTES)` — 4000
    bytes of raw PHPUnit/PHPStan/PHPCS/drush output per check, routinely
    containing absolute host paths, source fragments, stack traces, and DB
    diagnostics; and, per SEC-PROC-01, potentially the PAT.
  - `<cockpit>/cache/dashboard/<module>.json` — `ModuleSnapshot::toJson()`
    serialises the *raw* upstream payloads
    (`'project' => ..., 'merge_requests' => ..., 'issues' => ...`), i.e.
    complete GitLab API responses fetched with the maintainer's PAT. For any
    limited-visibility project this is token-scoped data landing world-readable.
- **Proposed resolution:** Create both caches' directories `0700` and their
  files `0600` (explicit `chmod` after write, since `file_put_contents` does not
  take a mode). Note in the plan's documentation task that a cockpit under
  version control should exclude `results/` and `cache/`.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-06 — `RegistryEditor::add()` destroys the module registry on a mid-write crash, and already writes the temp file it fails to rename

- **File / anchor:** `src/Cockpit/RegistryEditor.php` — `RegistryEditor::add()`
- **Lens:** security (filesystem — atomicity)
- **Severity:** High
- **Evidence:**
  ```php
  $probe = $this->registryPath . '.probe';
  file_put_contents($probe, $yaml);
  try {
      ModuleRegistry::fromFile($probe);
  } finally {
      @unlink($probe);
  }

  file_put_contents($this->registryPath, $yaml);
  ```
  The class docblock claims *"a bad entry can never corrupt the file"* — true of
  semantically bad content, false of a truncated write. This is the **only**
  writer of the tool's single source of truth, and it is a direct in-place
  overwrite. It is also 90% of an atomic write already: it writes a sibling
  temp file containing the exact final bytes, then **deletes it and writes the
  real file a second time** instead of `rename()`ing it into place.
- **Proposed resolution:** Replace the second `file_put_contents` with
  `rename($probe, $this->registryPath)` (atomic on the same filesystem, which
  the sibling path guarantees), moving the `@unlink` into the failure path only.
  Use `tempnam()`/`O_EXCL` rather than the fixed, predictable `registry.yml.probe`
  name so concurrent `modules:add` runs cannot collide and a same-user symlink
  plant on that name cannot redirect the probe write.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-07 — Every `file_put_contents` in the codebase discards its return value; five of them produce a false success claim

- **File / anchor:** all 11 sites —
  `Results\ResultsCache::store()`, `Dashboard\DashboardCache::save()`,
  `Cockpit\RegistryEditor::add()` (×2), `Command\InitCommand::execute()` (×4),
  `BaseArtifact\BaseArtifactBuilder::doBuild()` (×2),
  `Adapter\DdevContribAdapter::provision()`,
  `Adapter\DdevContribAdapter::wireModule()`,
  `Adapter\DdevContribAdapter::adaptAddOnConfig()`
- **Lens:** security (filesystem — silent data loss)
- **Severity:** High
- **Evidence:** There is not a single checked `file_put_contents` in `src/`. By
  contrast 4 of the 5 `mkdir` sites use the correct race-tolerant
  `!mkdir(...) && !is_dir(...)` idiom with a thrown exception or `FAILURE`
  return — `DashboardCache::save()` is the lone exception and checks neither.
  Highest-consequence silent failures, in order:
  1. `RegistryEditor::add()` → `ModulesAddCommand` prints
     `'Registered %d module(s) tracking core %s: %s'` over an **unmodified**
     registry.
  2. `InitCommand::execute()` → prints `'Cockpit created at "%s": ...'` and
     returns `SUCCESS` with **no `registry.yml`**; the next command reports
     "Module registry not found".
  3. `DdevContribAdapter::provision()` → the `.upkeep-env.yml` *completion
     marker* silently missing means `staleReasons()` reports
     `'No %s completion marker — a previous provision did not finish.'` and the
     tool tears down and re-provisions a multi-gigabyte environment **on every
     invocation, forever**.
  4. `BaseArtifactBuilder::doBuild()` → `meta.yml` / `canonical` marker missing
     while the command reports the build succeeded; `ArtifactScanner` then
     reports the set incomplete and `PruneSelector` no longer protects it.
  5. `ResultsCache::store()` → `CheckCommand::cacheResults()` prints
     `'Results cached: ...'` unconditionally.
- **Proposed resolution:** Introduce one small write helper that checks the
  return (and, per SEC-FS-05, sets the mode) and throws a typed exception naming
  the path; route all 11 sites through it. Also fix `DashboardCache::save()`'s
  unchecked `mkdir` to the race-tolerant idiom used everywhere else.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-08 — No write in the codebase is atomic; two read-modify-write sites can brick an environment

- **File / anchor:** `src/Adapter/DdevContribAdapter.php` —
  `DdevContribAdapter::wireModule()`, `DdevContribAdapter::adaptAddOnConfig()`
  (plus the sites in SEC-FS-06 and SEC-FS-07)
- **Lens:** security (filesystem — atomicity)
- **Severity:** Medium
- **Evidence:**
  ```php
  file_put_contents($composerJsonPath, ModuleWiring::withPathRepository(
      (string) file_get_contents($composerJsonPath),
      './' . self::MODULE_DIR,
  ));
  ```
  and the equivalent over `.ddev/config.contrib.yaml`. Both are read-modify-write
  cycles over a file the environment cannot function without. A crash mid-write
  leaves an unparseable `composer.json`; because the `.upkeep-env.yml` marker is
  still present, the next `ensureEnv()` **reuses** the broken environment rather
  than re-provisioning it. Both reads also cast a possible `false` to `''`.
- **Note on what is handled well (recorded so it is not "fixed" away):**
  `ResultsCache::read()` is deliberately lenient (`catch (\Throwable) { return null; }`)
  so a torn results file degrades to a cache miss; `.upkeep-env.yml` is
  deliberately written **last** as a completion marker so a torn write correctly
  forces re-provisioning; `BaseArtifactBuilder`'s
  `catch (\Throwable)` → `rm -rf $versionDir` → rethrow guarantees no partial
  artifact set survives. `DashboardCache::load()` is the one reader with **no**
  such protection — it calls `ModuleSnapshot::fromJson($content)` with no
  try/catch, and `DashboardCommand` does not guard the call, so a torn dashboard
  cache surfaces as an uncaught exception requiring a manual `rm`.
- **Proposed resolution:** Write via temp file + `rename()` in the shared helper
  from SEC-FS-07. Separately, make `DashboardCache::load()` lenient in the same
  documented way as `ResultsCache::read()`.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-09 — `PruneExecutor` reports snapshots as deleted and counts their bytes as freed before an unchecked, suppressed `unlink`

- **File / anchor:** `src/Maintenance/PruneExecutor.php` — `PruneExecutor::execute()`
- **Lens:** security (filesystem — destructive-operation reporting)
- **Severity:** Medium
- **Evidence:**
  ```php
  $freed += $item->sizeBytes;
  $deleted[] = $item;
  ($this->log)(sprintf('Removing materialized snapshot %s ...', $item->path));
  @unlink($item->path);
  ```
  The accounting happens **before** the deletion and the deletion's result is
  both suppressed and discarded. A failed removal is reported to the operator as
  successfully reclaimed space.
- **Impact:** `prune` is the tool's only destructive command; its report is the
  only feedback the operator gets. Over-reporting on a destructive operation is
  the wrong direction for a defect.
- **Proposed resolution:** Drop the `@`, check the return, and move the
  `$freed`/`$deleted` accounting after a confirmed deletion; route failures into
  the existing `$skipped` list with the error reason.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-10 — `--older-than` age filtering silently uses creation time: `last_used_at` is read but never written

- **File / anchor:** `src/Maintenance/InventoryScanner.php` —
  `InventoryScanner::readEnvMeta()`; `src/Adapter/EnvironmentMeta.php` — `EnvironmentMeta::toYaml()`
- **Lens:** security (destructive-operation correctness) / best-practice
- **Severity:** Medium
- **Evidence:**
  ```php
  // Age prefers last_used_at (stamped on adapter reuse) over created_at.
  $lastUsed = self::timestamp($data['last_used_at'] ?? null) ?? self::timestamp($data['created_at'] ?? null);
  ```
  `grep -rn "last_used_at\|lastUsedAt" src/` shows `last_used_at` is **read at
  this one place and written nowhere**. `EnvironmentMeta::toYaml()` emits only
  `module`, `core_major`, `seed_core_version`, `addon_version`, `created_at`.
  The comment's "stamped on adapter reuse" is not implemented.
- **Impact:** `upkeep prune --older-than 30d` deletes environments that were
  reused yesterday, provided they were *created* more than 30 days ago. This is
  data loss driven by a stale-by-design field that is never refreshed.
- **Proposed resolution:** Either stamp `last_used_at` in
  `DdevContribAdapter::reuse()` (matching the comment and the intent) or delete
  the `last_used_at` branch and rename the concept to `created_at` throughout so
  the CLI help does not promise last-use semantics it cannot deliver. The first
  is preferable; record the choice in the commit message.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-11 — `ResultsCache::read()`'s `@var` docblock asserts a shape untrusted file content cannot guarantee

- **File / anchor:** `src/Results/ResultsCache.php` — `ResultsCache::read()` (approx. L101–103)
- **Lens:** security (filesystem — untrusted input) / best-practice
- **Severity:** Medium
- **Evidence:** The docblock
  `/** @var array{sha?: string, recorded_at?: string, results?: list<...>} $data */`
  is applied to a `json_decode()` of an arbitrary on-disk file. PHPStan then
  reports the *real* runtime guard as dead:
  `src/Results/ResultsCache.php:103: Negated boolean expression is always false. [identifier=booleanNot.alwaysFalse]`
  for `!is_array($data['results'])`.
- **Impact:** This is not a missing annotation — it is a **false** annotation
  over untrusted input, and it will cause tasks 10/11 to delete a live guard as
  "unreachable". The subsequent `CheckType::from($check['type'] ?? '')` and
  `new CheckResult(...)` calls depend on that guard.
- **Proposed resolution:** Remove the `@var` assertion and narrow the decoded
  value with real runtime checks (or a small typed parser) so the guards are
  both true and statically visible. Flagged to tasks 10/11 as a
  do-not-blindly-suppress case.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-12 — `ModuleRegistry` never validates the module machine name; the failure surfaces mid-prune as an uncaught `InvalidArgumentException`

- **File / anchor:** `src/Cockpit/ModuleRegistry.php` — `ModuleRegistry::buildModule()`;
  failure site `src/Maintenance/PruneExecutor.php` — `PruneExecutor::resolveEnvironment()`
- **Lens:** security (filesystem — input validation) / best-practice
- **Severity:** Medium
- **Evidence:** `buildModule()` validates `project` and `core_versions` but
  takes the name on trust: `$modules[$name] = self::buildModule($path, (string) $name, $definition);`.
  A name that fails `ProjectName::MODULE_PATTERN` is accepted at load and first
  rejected deep inside a destructive command:
  ```php
  if (ProjectName::for($module->name, $coreMajor) === $item->projectName) {
  ```
  `ProjectName::for()` throws `\InvalidArgumentException`, which no command
  catches, so `upkeep prune` aborts with a stack trace **after** having already
  torn down some environments.
- **Proposed resolution:** Validate the machine name (and each core version) in
  `ModuleRegistry::buildModule()`, raising `RegistryException` with the offending
  key — the same exception every command already handles. This also closes
  SEC-FS-03 and SEC-FS-04.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-13 — `Cockpit::resolve()` accepts an empty `--cockpit` and an unchecked `getcwd()`, yielding filesystem-root-relative paths

- **File / anchor:** `src/Cockpit/Cockpit.php` — `Cockpit::resolve()`
- **Lens:** security (filesystem)
- **Severity:** Low
- **Evidence:**
  ```php
  return new self($option ?? ($env !== false && $env !== '' ? $env : getcwd()));
  ```
  The env var is guarded against the empty string; the option is guarded only
  against `null`. `--cockpit=""` therefore yields `root === ''`, and every
  derived path becomes absolute at the filesystem root: `'/registry.yml'`,
  `'/results'`, `'/base-artifacts'`. `getcwd()` returning `false` (deleted cwd)
  produces the same state. `ProjectsRoot::resolve()` handles the empty-string
  case correctly; `Cockpit::resolve()` does not. There is also no `rtrim`, so
  `--cockpit=/foo/` gives `/foo//registry.yml` — two spellings of one cockpit
  that produce non-equal strings for `PruneSelector`'s lexical prefix check
  (SEC-FS-02).
- **Proposed resolution:** Mirror `ProjectsRoot::resolve()`'s guard
  (`$option !== null && $option !== ''`), `rtrim` the result, and fail
  explicitly when `getcwd()` returns `false`.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-14 — `InitCommand` uses `file_exists` rather than `is_file` for its "cockpit already exists" guard

- **File / anchor:** `src/Command/InitCommand.php` — `InitCommand::execute()`
- **Lens:** security (filesystem)
- **Severity:** Low
- **Evidence:** `if (file_exists($cockpit->registryPath())) { ... }` — a
  *directory* named `registry.yml` satisfies the guard, so `init` refuses; and
  in the inverse case a directory at that path would make the subsequent
  (unchecked, per SEC-FS-07) `file_put_contents` fail silently while `init`
  reports success.
- **Proposed resolution:** Use `is_file()`, and let the SEC-FS-07 write helper
  surface the failure.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-15 — `ArtifactScanner::directorySize()` has no failure handling for an unreadable subdirectory

- **File / anchor:** `src/BaseArtifact/ArtifactScanner.php` — `ArtifactScanner::directorySize()`
- **Lens:** security (filesystem) / best-practice
- **Severity:** Low
- **Evidence:**
  ```php
  $iterator = new \RecursiveIteratorIterator(
      new \RecursiveDirectoryIterator($dir, \FilesystemIterator::SKIP_DOTS),
  );
  ```
  No try/catch: an unreadable subdirectory throws `UnexpectedValueException` out
  of `base-artifacts:status`.
  **Verified favourable:** `FOLLOW_SYMLINKS` is not set and
  `RecursiveDirectoryIterator::hasChildren()` defaults to `$allowLinks = false`,
  so this does **not** descend into symlinked directories. Recorded so the
  reviewer of task 7 does not "fix" a non-problem.
- **Proposed resolution:** Pass `\RecursiveIteratorIterator::CATCH_GET_CHILD`
  (or catch and skip), so an unreadable subtree degrades to an
  under-count rather than a crash.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

#### SEC-FS-16 — `glob()`/`scandir()` failures are coalesced with "empty" throughout the inventory path

- **File / anchor:** `src/Maintenance/InventoryScanner.php` —
  `InventoryScanner::libraryDumps()`, `::projects()`, `::projectItems()`,
  `::snapshots()`; `src/Results/ResultsCache.php` — `ResultsCache::latest()`;
  `src/BaseArtifact/ArtifactLayout.php` — `ArtifactLayout::versionsOnDisk()`
- **Lens:** security (filesystem) / best-practice
- **Severity:** Low
- **Evidence:** The `?: []` idiom on every `glob`/`scandir` makes an unreadable
  directory indistinguishable from an empty one.
- **Assessment:** For the prune path this fails **safe** (under-report, never
  over-delete), which is the right direction; recorded because
  `ResultsCache::latest()` fails the other way — an unreadable results directory
  silently reports "never checked", which the fast-lane gate reads as
  `local-missing` and correctly denies. Net effect is conservative, but the
  diagnosis is invisible.
- **Proposed resolution:** Distinguish `false` from `[]` and surface a warning
  (not an exception) naming the unreadable directory.
- **Owning task:** **Task 7 — filesystem path and result-file handling**

---

### Lens: best-practice — GitLab error taxonomy

---

#### BP-GL-01 — `ModulesAddCommand` calls `ApiFailure::message()`, which does not exist (fatal `Error`)

- **File / anchor:** `src/Command/ModulesAddCommand.php` — `ModulesAddCommand::execute()` (approx. L69–70)
- **Lens:** best-practice (error taxonomy — API misuse)
- **Severity:** Critical
- **Evidence:**
  ```php
  if ($projects instanceof ApiFailure) {
      $io->error('Could not list your project memberships: ' . $projects->message());
  ```
  `Gitlab\ApiFailure` exposes a **public promoted property** `public string $message`.
  There is no `message()` method anywhere in `src/Gitlab/` and no `__call`.
  Every other consumer uses the property (`$project->message`, `$result->message`,
  `$fresh->message`). PHPStan confirms:
  `Call to an undefined method Upkeep\Gitlab\ApiFailure::message(). [identifier=method.notFound]`.
- **Impact:** Any GitLab failure during `membershipProjects()` — a closed
  endpoint, a rate limit, a network blip — turns `upkeep modules:add` into an
  uncaught `Error`. Untested because the test always injects a healthy client.
- **Proposed resolution:** Fix as part of giving `ApiFailure` the uniform
  accessor surface in BP-GL-04, so every consumer goes through one API.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**
- **File contention note:** Task 6 (SEC-CRED-01) and task 9 (BP-CMD-*) also
  touch `ModulesAddCommand`. See "Coordination notes" below.

---

#### BP-GL-02 — `mergedSinceTag()` reports a domain-level tag miss as an HTTP 404

- **File / anchor:** `src/Gitlab/GitlabClient.php` — `GitlabClient::mergedSinceTag()`
- **Lens:** best-practice (error taxonomy)
- **Severity:** Medium
- **Evidence:** After a **successful** `tags()` call whose result simply does not
  contain the requested tag:
  ```php
  return new NotFound($project->webUrl . '/-/tags');
  ```
  `NotFound`'s constructor unconditionally produces
  `'Resource not found (HTTP 404). Check in the browser: %s'`. No HTTP 404
  occurred. The user of `upkeep notes --since-tag=X` for a tag that does not
  exist is told the API returned 404.
- **Proposed resolution:** Introduce a distinct failure type for a resolved-but-
  absent domain resource (e.g. `TagNotFound`, or make `NotFound` carry the
  resource description and drop the hardcoded "HTTP 404" from its message).
  Decide once and apply consistently.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**

---

#### BP-GL-03 — `EndpointClosed` conflates 401 (bad credential) with 403 (endpoint closed) and always advises the browser

- **File / anchor:** `src/Gitlab/GitlabClient.php` — `GitlabClient::request()`;
  `src/Gitlab/EndpointClosed.php` — `EndpointClosed::__construct()`
- **Lens:** best-practice (error taxonomy) — with credential-UX consequences
- **Severity:** Medium
- **Evidence:**
  ```php
  if ($status === 401 || $status === 403) {
      return new EndpointClosed($status, $browserUrl);
  }
  ```
  producing `'Endpoint closed to API access (HTTP %d). Use the browser instead: %s'`.
  On git.drupalcode.org a **403** genuinely means the block-by-default instance
  has not opened that endpoint — "use the browser" is correct. A **401** means
  the PAT is missing, invalid, expired, or lacks the scope, and the correct
  advice is to re-check the credential (the `describeSources()` wording), not to
  open a browser tab.
- **Impact:** For a tool whose entire security story is credential handling, the
  one message that tells the operator their token is bad instead tells them
  something unrelated. `MergeCommand::mergeOne()`'s documented degraded path
  ("GitLab refuses API merges here") also mis-fires on an expired token.
- **Proposed resolution:** Split into `EndpointClosed` (403 only, keeps the
  browser fallback) and a new `Unauthorized` (401, message points at
  `TokenResolver::describeSources()` and must not echo the token). Update
  `DashboardRow::failureCell()` and `MergeCommand::mergeOne()`.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**

---

#### BP-GL-04 — `ApiFailure` has no polymorphic API; three separate ad-hoc discriminators exist outside the hierarchy

- **File / anchor:** `src/Gitlab/ApiFailure.php`;
  `src/Command/NotesCommand.php` — `NotesCommand::failureName()`;
  `src/Command/ApiProbeCommand.php` — `ApiProbeCommand::failureName()`;
  `src/Dashboard/DashboardRow.php` — `DashboardRow::failureCell()`
- **Lens:** best-practice (error taxonomy)
- **Severity:** Medium
- **Evidence:** Two byte-identical private methods:
  ```php
  private function failureName(ApiFailure $failure): string
  {
      return (new \ReflectionClass($failure))->getShortName();
  }
  ```
  plus a third, structurally different discriminator:
  ```php
  return 'n/a (' . match (true) {
      $failure instanceof EndpointClosed => (string) $failure->status,
      $failure instanceof NotFound => '404',
      $failure instanceof RateLimited => 'rate-limited',
      $failure instanceof TransportError => $failure->status === null ? 'transport' : (string) $failure->status,
      default => 'error',
  } . ')';
  ```
  The `default` arm exists only because the hierarchy is not sealed — it is
  unreachable today and will be an uncoverable line for task 13/16.
- **Proposed resolution:** Give `ApiFailure` an abstract `shortCode(): string`
  (or `kind(): FailureKind` enum) that each subtype implements; delete both
  `failureName()` copies and replace the `match (true)` with a total dispatch
  that needs no `default`. Reflection-based naming in production code is a smell
  in its own right.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**

---

#### BP-GL-05 — The `ApiFailure` subtypes have inconsistent shapes, so callers cannot query them uniformly

- **File / anchor:** `src/Gitlab/EndpointClosed.php`, `src/Gitlab/NotFound.php`,
  `src/Gitlab/RateLimited.php`, `src/Gitlab/TransportError.php`
- **Lens:** best-practice (error taxonomy)
- **Severity:** Medium
- **Evidence:** `browserUrl` exists on `EndpointClosed` and `NotFound` but not
  on `RateLimited` or `TransportError`. `status` exists on `EndpointClosed`
  (`int`) and `TransportError` (`?int`) but not on the others.
  `MergeCommand::mergeOne()` can print a browser URL only for `EndpointClosed`;
  every other failure degrades to a bare message, even though a browser URL is
  exactly what the operator needs in each case.
- **Proposed resolution:** Lift `?string $browserUrl` and `?int $status` onto
  `ApiFailure` (populated where known) so consumers can offer the browser
  fallback uniformly. Backwards compatibility is waived, so change the
  constructors freely.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**

---

#### BP-GL-06 — Transient failures are memoized for the lifetime of the run

- **File / anchor:** `src/Gitlab/GitlabClient.php` — `GitlabClient::get()`
- **Lens:** best-practice (error taxonomy / robustness)
- **Severity:** Medium
- **Evidence:** `return $this->getCache[$url] ??= $this->request('GET', $url, [], $browserUrl);`
  — the cache is declared `@var array<string, array|ApiFailure>`, so a returned
  `ApiFailure` is stored and every later request for the same URL in that run
  gets the cached failure.
- **Impact:** `RateLimited` and `TransportError` are transient by definition. A
  single blip during a long `upkeep dashboard` run permanently fails that
  resource for the rest of the invocation, and the dashboard shows a stale
  failure cell. `NotFound` and `EndpointClosed` are stable and are correctly
  cacheable.
- **Proposed resolution:** Memoize successes plus the stable failure types only;
  do not memoize `RateLimited`/`TransportError`. The `fresh()` escape hatch
  already exists for the merge re-check and is unaffected.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**

---

#### BP-GL-07 — `membershipProjects()` paginates in an unbounded loop with no page cap

- **File / anchor:** `src/Gitlab/GitlabClient.php` — `GitlabClient::membershipProjects()`
- **Lens:** best-practice (robustness)
- **Severity:** Medium
- **Evidence:**
  ```php
  for ($page = 1;; ++$page) {
      $data = $this->get(... . '&page=' . $page, ...);
      if ($data instanceof ApiFailure) { return $data; }
      if ($data === []) { return $projects; }
      foreach (array_values($data) as $item) { $projects[] = Project::fromApi($item); }
  }
  ```
  The only exits are a typed failure and an exactly-empty array. A response that
  is a non-empty **associative** array (an error object shaped `{"message": ...}`
  that still returns HTTP 200, or a proxy interstitial) is neither, so the loop
  runs forever, issuing one request per iteration.
- **Proposed resolution:** Cap the page count with a named constant, and treat a
  non-list response as `TransportError` rather than as data.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**

---

#### BP-GL-08 — The `tags()` docblock is orphaned above `membershipProjects()`, and that is the direct cause of a PHPStan error

- **File / anchor:** `src/Gitlab/GitlabClient.php` — between
  `GitlabClient::headPipeline()` and `GitlabClient::membershipProjects()` (approx. L113–125)
- **Lens:** best-practice
- **Severity:** Low
- **Evidence:** The block
  ```php
  /**
   * Repository tags, newest first (GitLab default ordering).
   *
   * @return list<Tag>|ApiFailure
   */
  ```
  is immediately followed by a **second** docblock for `membershipProjects()`
  and then by `membershipProjects()` itself. `tags()`, declared ~20 lines later,
  has no docblock and no `@return`, so its `array|ApiFailure` stays untyped —
  which is precisely why PHPStan reports
  `Cannot access property $createdAt on mixed` and
  `Cannot access property $name on mixed` inside `mergedSinceTag()`.
- **Proposed resolution:** Move the docblock back onto `tags()`. Recorded here
  rather than left to task 10 because it is a real documentation defect, not a
  missing annotation, and task 10 would otherwise "fix" the symptom by adding a
  second `@return` while leaving the misplaced block in place.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**

---

#### BP-GL-09 — `GitlabClient::request()` catches only `TransportExceptionInterface` and `JsonException`

- **File / anchor:** `src/Gitlab/GitlabClient.php` — `GitlabClient::request()`
- **Lens:** best-practice (error taxonomy — totality)
- **Severity:** Low
- **Evidence:** The class contract is documented as
  *"Never throws for HTTP-level outcomes"* and *"HTTP-level outcomes are returned
  as typed values (never thrown)"*. But `$response->getHeaders(false)` and
  `$response->getContent(false)` can raise other Symfony contract exceptions
  (`RedirectionException`, `ClientException`, `ServerException` are suppressed by
  the `false` argument, but `TransportExceptionInterface` is the only one caught
  for the header call). Anything outside the two caught types escapes the typed
  taxonomy entirely and reaches commands as an uncaught exception.
- **Proposed resolution:** Make the taxonomy total: catch
  `\Symfony\Contracts\HttpClient\Exception\ExceptionInterface` and map to
  `TransportError`, so the documented "never throws" contract is actually true.
- **Owning task:** **Task 8 — GitLab error-taxonomy consistency**

---

### Lens: best-practice — command classes and the adapter boundary

---

#### BP-CMD-01 — Four mutually inconsistent policies for "cockpit or registry missing"; five commands let `RegistryException` escape uncaught

- **File / anchor:** 17 sites across `src/Command/`. Uncaught in:
  `EnvPathCommand::execute()`, `ExecCommand::execute()`, `IssueCommand::execute()`,
  `NeedsWorkCommand::execute()`, `PruneCommand::execute()`
- **Lens:** best-practice (duplication + error handling)
- **Severity:** High
- **Evidence:** `Cockpit::resolve($input->getOption('cockpit'))` appears in 17
  methods under four policies:
  - **A. try/catch → `$io->error` → `FAILURE`** (6): `DashboardCommand`,
    `DevCommand`, `MergeCommand`, `ModulesCommand`, `PatchesCommand`,
    `ModulesAddCommand`.
  - **B. no catch at all** (5): the commands listed above, e.g.
    ```php
    $cockpit = Cockpit::resolve($input->getOption('cockpit'));
    $modules = $cockpit->loadRegistry()->modules();
    ```
    `ModuleRegistry::fromFile()`'s carefully written message ("Run `upkeep init`
    to create a cockpit, or point --cockpit / UPKEEP_COCKPIT at an existing
    one.") is replaced by a Symfony stack trace.
  - **C. `file_exists()` pre-check with a third, different message** (4,
    verbatim identical): `BaseArtifactsBuildCommand`, `BaseArtifactsStatusCommand`,
    `PruneCommand`, `StatusCommand` —
    `'No cockpit found at "%s" (missing %s). Run \`upkeep init\` first.'`
  - **D. swallowed** (1): `NotesCommand::resolveProjectPath()`
    (`catch (RegistryException) { return $module; }` — deliberate).
- **Impact:** Three different user-facing messages plus a stack trace for one
  condition, chosen by which command was typed.
- **Proposed resolution:** One shared `cockpitAndRegistry(InputInterface, SymfonyStyle)`
  seam on a shared base (or a small `CockpitLoader` collaborator) with a single
  message and a single exit code. `NotesCommand`'s deliberate fallback stays,
  documented.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-02 — `PruneCommand` loads the registry after printing the reclaim table, so a malformed registry crashes mid-run

- **File / anchor:** `src/Command/PruneCommand.php` — `PruneCommand::execute()`
  (`file_exists` pre-check approx. L89; `$registry = $cockpit->loadRegistry();` approx. L142)
- **Lens:** best-practice (ordering / destructive command safety)
- **Severity:** Medium
- **Evidence:** The `file_exists($cockpit->registryPath())` gate only proves the
  file exists. The parse happens ~50 lines later, **after** inventory scanning,
  selection, and the reclaim table have been rendered. A registry that exists but
  is invalid YAML throws an uncaught `RegistryException` at that point.
- **Impact:** On the tool's only destructive command, the user has already been
  shown a reclaim plan when the run aborts with a stack trace.
- **Proposed resolution:** Load and validate the registry once, up front, through
  the BP-CMD-01 shared seam.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-03 — The documented 0/1/2 exit-code contract is honoured by 2 of 19 commands

- **File / anchor:** `src/Workflow/ExitCode.php`; honoured by
  `CheckCommand::execute()` and `ReviewCommand::execute()` only; 15 other
  `execute()` methods use `Command::SUCCESS`/`Command::FAILURE`
- **Lens:** best-practice
- **Severity:** Medium
- **Evidence:** `ExitCode` documents *"kept in one place so scripting can rely
  on it"*: `OK = 0`, `CHECKS_FAILED = 1`, `INFRASTRUCTURE = 2`. Every other
  command returns `Command::FAILURE` (= 1) for infrastructure failures. Concrete
  divergence: "no GitLab token" exits **1** from `ApiProbeCommand`,
  `DashboardCommand`, `MergeCommand`, `NotesCommand`, `IssueCommand`, and
  `NeedsWorkCommand`, and exits **2** from `CheckCommand` and `ReviewCommand` —
  same cause, different code.
- **Proposed resolution:** Either extend `ExitCode` to the whole CLI surface
  (infrastructure = 2 everywhere) or restrict its documented scope to
  check/review explicitly. The first matches the contract's stated purpose. No
  BC constraint applies. Must be settled before task 12 writes the e2e
  exit-code assertions.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-04 — `MergeCommand` exits 0 after failed merges, and uses `Command::INVALID` (= 2) for bad usage

- **File / anchor:** `src/Command/MergeCommand.php` — `MergeCommand::execute()`, `MergeCommand::mergeOne()`
- **Lens:** best-practice
- **Severity:** Medium
- **Evidence:** `mergeOne()` increments `$tally['failed']` on a merge failure,
  but `execute()` ends with an unconditional
  ```php
  $this->renderSummary($io, $tally);

  return Command::SUCCESS;
  ```
  Separately, the fast-lane-mode guard returns `Command::INVALID` (= 2), which
  collides numerically with `ExitCode::INFRASTRUCTURE` while meaning "bad usage".
- **Impact:** A fast-lane run where every merge failed exits 0.
- **Proposed resolution:** Return a failure code when `$tally['failed'] > 0`,
  and resolve the `INVALID` collision as part of BP-CMD-03.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-05 — `ExecCommand` returns the child's raw exit code, outside both contracts — **OPEN QUESTION**

- **File / anchor:** `src/Command/ExecCommand.php` — `ExecCommand::execute()`
- **Lens:** best-practice
- **Severity:** Medium
- **Evidence:** `return $process->getExitCode() ?? Command::FAILURE;` — any value
  0–255. A wrapped command exiting 2 is indistinguishable from an upkeep
  infrastructure failure; exiting 1 is indistinguishable from `CHECKS_FAILED`.
- **OPEN QUESTION — needs a decision:** pass-through is arguably the *correct*
  behaviour for an `exec` wrapper (that is what `docker exec`, `ddev exec`, and
  `git -c ... exec` all do), and forcing it into 0/1/2 would make
  `upkeep exec <module> -- phpunit` useless in a script. The alternatives are:
  (a) keep pass-through and document `exec` as explicitly exempt from the
  `ExitCode` contract; (b) reserve 0/1/2 for upkeep and offset child codes.
  **Recommendation: (a).** Recorded rather than decided unilaterally because it
  changes a documented contract's scope.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-06 — `DashboardCommand` re-derives the pipeline that `RowAssembler` exists to own

- **File / anchor:** `src/Command/DashboardCommand.php` — `DashboardCommand::execute()`
  (approx. L175–181), `DashboardCommand::fetchModule()`;
  `src/Dashboard/RowAssembler.php` — `RowAssembler::assemble()`
- **Lens:** best-practice (misplaced responsibility)
- **Severity:** Medium
- **Evidence:** `RowAssembler`'s own docblock states it was *"Extracted from
  DashboardCommand so the fast-lane merge command consumes the exact same
  classification pipeline instead of re-deriving status"*. `MergeCommand::execute()`
  uses it (`$assembler = new RowAssembler($client, $cache);`). `DashboardCommand`
  does not — it rebuilds the project → MRs → gate pipeline by hand:
  ```php
  $rows[] = DashboardRow::forMergeRequest(
      ..., $gate->classify($mr, $core, $local),
  ```
  duplicating `RowAssembler::assemble()`'s
  `$this->gate->classify($detail, $core, $local)`. `RowAssembler::assemble()`
  even accepts the `?string $versionFilter` that `DashboardCommand` applies
  inline.
- **Impact:** The extraction was performed for a stated safety reason and then
  only half-adopted. The dashboard's classification and the merge command's
  classification can now drift — and drift here **misclassifies merge
  eligibility**, which is the one thing `FastLaneGate`'s docblock says must not
  happen ("A misclassification here merges the wrong thing").
- **Proposed resolution:** Route `DashboardCommand` through `RowAssembler`,
  deleting the inline pipeline. Highest-value best-practice fix in the set.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-07 — Five command classes select the concrete engine; the documented adapter-boundary guard misses them by capitalisation — **OPEN QUESTION**

- **File / anchor:** `AbstractMrCommand::adapter()`, `DevCommand::buildAdapter()`,
  `EnvPathCommand::buildAdapter()`, `ExecCommand::buildAdapter()`,
  `PruneCommand::execute()` — each `return new DdevContribAdapter(` / `new DdevContribAdapter(`,
  each with `use Upkeep\Adapter\DdevContribAdapter;`
- **Lens:** best-practice (adapter boundary)
- **Severity:** Medium
- **Evidence:** The documented guard passes:
  ```
  $ grep -r "ddev" src/ --exclude-dir=Adapter     # no output, exit 1
  ```
  It passes **only because the class is spelled `DdevContribAdapter` with a
  capital D**. Case-insensitively, five command classes name and instantiate the
  concrete engine implementation — i.e. the *choice of engine* is made outside
  `src/Adapter/`.
  Related, lower-grade vocabulary leakage found by the same case-insensitive
  sweep (recorded, not necessarily actionable): docker/volume/container nouns in
  `StatusCommand`/`PruneCommand`/`BaseArtifactsBuildCommand` help text, direct
  use of `Adapter\VolumeProbe` by `StatusCommand` and `PruneCommand`, and
  `Category::ProjectVolume` plus container/volume prose throughout
  `src/Maintenance/`.
- **OPEN QUESTION — needs a decision:** three readings are defensible.
  (a) Only `src/Adapter/` may *name* an engine implementation — commands should
  receive an `EngineAdapterInterface` from a factory wired in `bin/upkeep`, and
  the guard should become case-insensitive.
  (b) Instantiating a class that lives *inside* `src/Adapter/` is not a breach;
  the rule is about engine *knowledge* (command names, container names, flags),
  and none of that leaks. The `Maintenance` "volume" vocabulary is a deliberate,
  documented domain concept.
  (c) Middle ground: introduce the factory (it also removes five copies of the
  same four-argument construction, BP-CMD-12) but leave `src/Maintenance/`'s
  vocabulary alone.
  **Recommendation: (c)**, plus making the CLAUDE.md guard case-insensitive so
  it actually tests what it claims. Recorded rather than decided unilaterally
  because it changes a documented project invariant and `CLAUDE.md`.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-08 — Option definitions duplicated verbatim, with the descriptions already drifting

- **File / anchor:** `configure()` across `src/Command/`
- **Lens:** best-practice (duplication)
- **Severity:** Medium
- **Evidence:**
  - `--cockpit`: **16** byte-identical definitions —
    `sprintf('Path to the cockpit directory (defaults to $%s, then the current directory)', Cockpit::ENV_VAR)`.
  - `--projects-root`: **6** byte-identical definitions.
  - `--version`: 6 definitions, **4 different descriptions** and **2 different
    semantics** — a target-core *selector* in `AbstractMrCommand`/`DevCommand`/
    `EnvPathCommand`/`ExecCommand`/`NeedsWorkCommand`, but a *filter* in
    `DashboardCommand` (`'Only show rows targeting this core major version (e.g. 11)'`).
    `NeedsWorkCommand`'s wording has already drifted
    (`'Target core major version (defaults to first tracked version)'`).
  - `module` argument: 4 descriptions; and `PatchesCommand` makes it an
    **option** (`--module`) while every other command makes it a positional
    argument.
  - `mr` argument: 2 descriptions. `--no-open`: 2 wordings.
- **Proposed resolution:** Extract the shared option definitions into one place
  (a trait or a shared base's `configureCockpitSurface()` /
  `configureEnvSurface()`), so a description change cannot drift again.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-09 — Six different "module not registered" messages across two output mechanisms

- **File / anchor:** `DevCommand::execute()`, `EnvPathCommand::execute()`,
  `ExecCommand::execute()`, `IssueCommand::execute()`, `PatchesCommand::execute()`,
  `ModulesAddCommand::execute()`, `Workflow\MrContextResolver::resolve()`
- **Lens:** best-practice (duplication)
- **Severity:** Medium
- **Evidence:** `DevCommand` uses
  `$io->error(sprintf('Module "%s" is not registered. Run \`upkeep modules\` to see what is.', $name));`
  while `EnvPathCommand` and `ExecCommand` emit the **same sentence** through a
  raw `$output->writeln(sprintf('<error>...</error>', $name));`. `IssueCommand`
  and `PatchesCommand` use a truncated variant. `MrContextResolver` uses a fifth
  wording that helpfully lists the registered modules
  (`'Module "%s" is not registered in the cockpit. Registered modules: %s.'`).
  `ModulesAddCommand` has a sixth.
- **Proposed resolution:** One lookup helper returning the `MrContextResolver`
  wording (the most useful of the six), emitted through `SymfonyStyle` uniformly.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-10 — Core-version selection and validation duplicated verbatim in three commands, re-implementing `MrContextResolver`

- **File / anchor:** `DevCommand::execute()`, `EnvPathCommand::execute()`,
  `ExecCommand::execute()`; existing implementation
  `Workflow\MrContextResolver::selectCoreVersion()`
- **Lens:** best-practice (duplication / misplaced responsibility)
- **Severity:** Medium
- **Evidence:** Identical in all three:
  ```php
  $version = $input->getOption('version');
  $coreMajor = $version !== null ? (string) $version : $module->coreVersions[0] ?? null;

  if ($coreMajor === null || !\in_array($coreMajor, $module->coreVersions, true)) {
  ```
  followed by the identical `'Core version %s is not tracked for %s (tracked: %s).'`
  message (via `$io->error` in `DevCommand`, via raw `<error>` writeln in the
  other two).
- **Proposed resolution:** Use `MrContextResolver::selectCoreVersion()` (or
  extract it to a small shared value resolver) in all three.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-11 — Three different MR-IID validation policies, one of which is no validation at all

- **File / anchor:** `AbstractMrCommand::resolveContext()`,
  `NeedsWorkCommand::execute()`, `IssueCommand::execute()`
- **Lens:** best-practice
- **Severity:** Medium
- **Evidence:**
  - `AbstractMrCommand`: `preg_match('/^\d+$/', $iidRaw) !== 1` → `WorkflowException` → exit 2.
  - `NeedsWorkCommand`: `!preg_match('/^\d+$/', $iidRaw) || (int) $iidRaw < 1` → `$io->error` → exit 1, different message.
  - `IssueCommand`: **none** — `$iid = (int) $input->getArgument('mr');`, so
    `upkeep issue foo abc` silently becomes a request for MR `!0`.
- **Proposed resolution:** One validator on the shared base; `IssueCommand`
  adopts it. Note `AbstractMrCommand`'s `/^\d+$/` also accepts `0`, which
  `NeedsWorkCommand` correctly rejects — take the stricter rule.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-12 — Eight further duplicated helpers across the command layer

- **File / anchor:** see list
- **Lens:** best-practice (duplication)
- **Severity:** Low
- **Evidence:**
  1. Adapter construction (5 copies) — `AbstractMrCommand::adapter()`,
     `DevCommand::buildAdapter()`, `EnvPathCommand::buildAdapter()`,
     `ExecCommand::buildAdapter()`, `PruneCommand::execute()` — with **three
     different logging policies**, two of them no-op closures:
     ```php
     new ProcessRunner(static function (): void {
     }),
     static function (): void {
     },
     ```
     (so `upkeep exec` and `upkeep env-path` silently discard all engine output).
  2. Browser opener (2 copies, verbatim) — `IssueCommand::execute()`,
     `NeedsWorkCommand::execute()`:
     `$opener = \PHP_OS_FAMILY === 'Darwin' ? 'open' : 'xdg-open';`
  3. Hand-rolled table renderer + `colorCells()` (2 near-identical copies) —
     `DashboardCommand`, `PatchesCommand`.
  4. `PatchesCommand::truncate()` duplicates `DashboardRow::truncate()`.
  5. `BaseArtifactsStatusCommand::formatBytes()` re-implements
     `Maintenance\ByteFormat::human()`.
  6. `EXCERPT_BYTES = 2000` declared in both `CheckCommand` and
     `NeedsWorkCommand`, with **different** truncation implementations.
  7. `VolumeProbe::withRunner(...)` construction duplicated —
     `StatusCommand::defaultVolumeProbe()` and inline in `PruneCommand::execute()`.
  8. The "collect project trees, then probe volumes" loop duplicated verbatim in
     `StatusCommand::execute()` and `PruneCommand::execute()`.
  9. `failureName()` (2 copies) — see BP-GL-04.
- **Proposed resolution:** Consolidate. Items 1 and 7 are resolved by the
  BP-CMD-07 factory; 3–6 and 8 move to the existing service classes that already
  own those concepts.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-13 — Cockpit sub-paths hardcoded at six call sites while `Cockpit` provides accessors for the other three

- **File / anchor:** `src/Cockpit/Cockpit.php`; callers `CheckCommand::cacheResults()`,
  `DashboardCommand::execute()`, `MergeCommand::execute()`, `NeedsWorkCommand::execute()`
  (`$cockpit->root . '/results'`), and `DashboardCommand::execute()`,
  `PatchesCommand::collectMrIssueNids()` (`$cockpit->root . '/cache/dashboard'`)
- **Lens:** best-practice
- **Severity:** Low
- **Evidence:** `Cockpit` defines `registryPath()`, `baseArtifactsPath()`,
  `fixturesPath()`, and `projectsPath()` with matching constants, but the
  `results/` and `cache/dashboard/` layout lives as literal strings at six call
  sites.
- **Proposed resolution:** Add `resultsPath()` and `dashboardCachePath()` with
  constants, matching the existing pattern. Coordinate with SEC-FS-05, which must
  set the mode on those directories.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-14 — `\assert()` used for load-bearing null invariants; assertions are compiled out in production

- **File / anchor:** `MergeCommand::execute()`, `MergeCommand::mergeOne()`,
  `MergeCommand::renderContext()`, `MergeCommand::describeNonActionable()`;
  `DashboardRow::verdictCell()` and one other `DashboardRow` accessor
- **Lens:** best-practice
- **Severity:** Low
- **Evidence:** `\assert($row->project !== null && $row->mergeRequest !== null);`
  immediately preceding `$client->fresh()->mergeRequest($row->project, $iid)`.
  With `zend.assertions=-1` (the `php.ini-production` default) the assertion is
  not compiled at all, so a violated invariant becomes a `TypeError` from inside
  the GitLab client rather than a clear failure at the guard.
- **Proposed resolution:** Replace with real guards throwing a domain exception,
  or narrow the types so the nullable state is unrepresentable (e.g. separate
  `DashboardRow` subtypes for the failure and merge-request cases). The latter
  also removes the nullable-property `?->` noise.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-15 — `DevCommand::buildAdapter()` is declared nullable but never returns null, leaving a dead guard

- **File / anchor:** `src/Command/DevCommand.php` — `DevCommand::buildAdapter()`, `DevCommand::execute()`
- **Lens:** best-practice
- **Severity:** Low
- **Evidence:** PHPStan:
  `Method Upkeep\Command\DevCommand::buildAdapter() never returns null so it can be removed from the return type. [identifier=return.unusedType]`.
  `execute()` then carries an unreachable
  `if ($adapter === null) { return Command::FAILURE; }`. `EnvPathCommand` and
  `ExecCommand` declare the same method non-nullable and correctly have no guard.
- **Proposed resolution:** Non-nullable return, delete the guard. Resolved for
  free by the BP-CMD-07 factory.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

#### BP-CMD-16 — `BaseArtifactsBuildCommand` uses `--core` on the strength of a constraint that no longer exists

- **File / anchor:** `src/Command/BaseArtifactsBuildCommand.php` — `BaseArtifactsBuildCommand::configure()`
- **Lens:** best-practice
- **Severity:** Low
- **Evidence:**
  ```php
  // Named --core, not --version: Symfony Console reserves -V/--version
  // at the application level (it prints the app version before any
  // command runs), so a command-scoped --version can never be received.
  ```
  That constraint was removed by `Command\VersionOptionInput` plus the
  `$definition->setOptions(array_filter(...))` block in `bin/upkeep`; six other
  commands now do receive `--version`. The comment is stale and the flag is the
  odd one out.
- **Proposed resolution:** Rename to `--version` for consistency (no BC
  constraint) or keep `--core` and rewrite the comment to state the real reason.
  Either way the stale claim must go, and `README.md` must match.
- **Owning task:** **Task 9 — command-class duplication and adapter-boundary compliance**

---

### OUT OF SCOPE (recorded with reasons)

---

#### OOS-01 — `nullsafe.neverNull` dead defensive code (5 occurrences)

- **File / anchor:** `BaseArtifactsStatusCommand::execute()` (×3 —
  `?->coreVersion`, `?->phpVersion`, `?->dbEngine`), `NotesCommand::execute()`
  (`?->createdAt`), `NotesCommand::resolveProjectPath()` (`?->project`)
- **Lens:** best-practice · **Severity:** Low
- **Reason out of scope:** No behavioural defect — the `?->` is simply redundant
  on a non-nullable expression. Removing it is exactly the mechanical work
  tasks 10 and 11 exist to do, and it will fall out of reaching PHPStan max.
  Recorded so it is not lost, and so task 17 can confirm it was removed rather
  than suppressed.

---

#### OOS-02 — `mixed`-typed decoding at the GitLab and drupal.org boundaries

- **File / anchor:** `Gitlab\Tag::fromApi()`, `Gitlab\MergeRequest::fromApi()`,
  `Gitlab\GitlabClient::mergedSinceTag()`, `Drupal\Issue::fromApi()`,
  `Drupal\IssueFile::fromApi()`, `Drupal\Issue` (`'und'` offset),
  `BaseArtifact\ComposerLock`, `Adapter\ThrowawaySite` (`'dbinfo'`/`'raw'`),
  `Adapter\DdevContribAdapter::describe()`, `Maintenance\InventoryScanner::projectItems()`,
  `BaseArtifact\ArtifactScanner::directorySize()`
- **Lens:** best-practice · **Severity:** Low–Medium
- **Reason out of scope:** These are the `offsetAccess.nonOffsetAccessible` /
  `method.nonObject` / `binaryOp.invalid` clusters (~40 PHPStan errors). They are
  real robustness gaps — e.g. `DdevContribAdapter::describe()`'s
  `is_array($decoded['raw'] ?? null)` throws if the engine returns a JSON scalar
  — but the correct fix is exactly the mandate of **task 10** (GitLab and
  configuration boundary) and **task 11** (adapter and remaining namespaces):
  *"narrow at the boundary — decode and validate external input into typed value
  objects once."* Assigning them to tasks 6–9 would duplicate that work and
  fragment the boundary design across four agents.
  **Two members of this cluster are NOT out of scope** and are recorded as
  findings above because they are false assertions or misplaced documentation
  rather than missing annotations: SEC-FS-11 (`ResultsCache::read()`'s lying
  `@var`) and BP-GL-08 (the orphaned `tags()` docblock).

---

#### OOS-03 — Unreachable match arm in `DdevContribAdapter::checkCommand()`

- **File / anchor:** `Adapter\DdevContribAdapter::checkCommand()` (approx. L552–553)
- **Lens:** best-practice · **Severity:** Low
- **Evidence:** PHPStan: `Match arm comparison between ...CheckType::ModuleInstall and ...CheckType::ModuleInstall is always true. [identifier=match.alwaysTrue]`
  — because `CheckType::FunctionalSmoke, CheckType::Deprecation => throw new \LogicException('Handled above.')`
  is genuinely unreachable (both are dispatched earlier), the preceding arm
  becomes total.
- **Reason out of scope:** Intentional defensive code, not a defect. It is,
  however, an uncoverable line and therefore belongs to **task 13**'s coverage
  work and **task 17**'s `@codeCoverageIgnore` budget, where it will need a
  written justification.

---

#### OOS-04 — `list<string>` return-type mismatch on `colorCells()`

- **File / anchor:** `DashboardCommand::colorCells()`, `PatchesCommand::colorCells()`
- **Lens:** best-practice · **Severity:** Low
- **Reason out of scope:** Pure annotation precision
  (`non-empty-array<int<0, max>, string>` vs `list<string>`). No defect. Task 11.
  Note the two methods are duplicates — that aspect **is** in scope as BP-CMD-12.

---

#### OOS-05 — Test doubles record call data that no assertion reads

- **File / anchor:** anonymous `EngineAdapterInterface` doubles in
  `tests/Command/DevCommandTest.php`, `tests/Command/EnvPathCommandTest.php` (×2),
  `tests/Command/PruneCommandTest.php`, `tests/Maintenance/PruneExecutorTest.php` (×2)
- **Lens:** best-practice · **Severity:** Low
- **Evidence:** PHPStan `property.onlyWritten` on `$calls` (×3) and
  `$teardowns` (×3): the fakes capture invocations that no test then asserts on —
  including, in `PruneExecutorTest`, the teardown calls of the tool's only
  destructive operation.
- **Reason out of scope:** This is a *test-quality* finding, not a production
  defect, and it maps directly onto the plan's stated quality risk
  ("coverage measures execution, not assertion … any test that executes a path
  without asserting an outcome is a finding"). It belongs to the coverage tasks
  that rewrite these suites — **task 13** (adapter) and **task 16** (remaining
  namespaces) — which should either assert on the captured calls or delete the
  capture. Recorded here so task 17's audit can check it was resolved rather
  than suppressed.

---

## Coordination notes for tasks 6–9

**File contention.** Three files are touched by more than one remediation task.
Sequence or coordinate to avoid conflicting rewrites:

| File | Tasks | Findings |
| --- | --- | --- |
| `src/Command/ModulesAddCommand.php` | 6, 8, 9 | SEC-CRED-01 (token guard), BP-GL-01 (`->message()`), BP-CMD-01/08/09 |
| `src/Command/AbstractMrCommand.php` | 6, 9 | SEC-CRED-03, BP-CMD-01/03/08/10/11/12 |
| `src/Adapter/ProcessRunner.php` | 6 only | SEC-PROC-01/02/03 |
| `src/Cockpit/ModuleRegistry.php` | 7 only | SEC-FS-03/04/12 (single-point fix) |

**Suggested order within phase 3:** task 6 lands the shared token guard first
(it rewrites one line in `ModulesAddCommand`), then task 8 lands the `ApiFailure`
accessor change, then task 9 restructures the command layer on top of both.
Task 7 is independent of all three.

**Cross-task dependencies:**
- BP-CMD-03 (exit-code contract) must be settled before **task 12** writes the
  hermetic e2e exit-code assertions.
- SEC-FS-05 (file modes) and BP-CMD-13 (`Cockpit::resultsPath()`) touch the same
  layout and should land together.
- SEC-PROC-01's sentinel-token test is the same assertion as the plan's
  self-validation step 10 — write it once, in task 6.
- SEC-FS-11 and BP-GL-08 are explicitly flagged to tasks 10/11 as
  do-not-blindly-suppress cases.

**On-disk format impact (per the plan's "strand the maintainer's cockpit" risk):**
SEC-FS-05 changes the mode of existing `results/` and `cache/dashboard/` files —
existing files keep their old mode unless re-created, so document a one-line
`chmod -R go-rwx` for an existing cockpit. SEC-FS-10's `last_used_at` fix adds a
key to `.upkeep-env.yml`; existing environments simply fall back to `created_at`
until next reused, so no rebuild is required.

---

## Open questions requiring the user's decision

1. **BP-CMD-05 — `upkeep exec` exit codes.** Keep raw child-exit-code
   pass-through (recommended, and document `exec` as exempt from the 0/1/2
   contract), or force it into the contract? Affects task 12's e2e assertions.
2. **BP-CMD-07 — what the adapter boundary actually forbids.** Is
   `new DdevContribAdapter(...)` inside `src/Command/` a breach? Recommendation
   is the middle path: introduce a factory in `bin/upkeep` (which also removes
   five duplicate constructions), leave `src/Maintenance/`'s documented
   "volume" vocabulary alone, and make the `CLAUDE.md` guard case-insensitive so
   it tests what it claims. This changes a documented project invariant, so it
   is recorded rather than decided.
3. **BP-CMD-03 — scope of the `ExitCode` contract.** Extend infrastructure = 2
   across all 19 commands, or narrow the documented contract to check/review
   only? Recommendation: extend, since `ExitCode`'s docblock says it exists
   "so scripting can rely on it".

Everything else in this record has a single defensible resolution and needs no
further input.
