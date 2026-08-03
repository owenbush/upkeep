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
| Task 6 — credential handling and subprocess output leakage | 9 | SEC-CRED-01…04, SEC-PROC-01…05 — **all resolved 2026-08-03** |
| Task 7 — filesystem path and result-file handling | 16 | SEC-FS-01…16 |
| Task 8 — GitLab error-taxonomy consistency | 9 | BP-GL-01…09 |
| Task 9 — command-class duplication and adapter-boundary compliance | 16 | BP-CMD-01…16 — **all resolved 2026-08-03** |
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
- **RESOLVED (task 6, 2026-08-03):** `ModulesAddCommand::buildClient()` no longer
  references the undefined `TokenResolver::ENV_VAR` / `::CONFIG_PATH_HINT`; it
  delegates to the new shared seam `Gitlab\GitlabClientFactory` (SEC-CRED-03).
  The command gained an optional `?TokenResolver` constructor argument so the
  no-token path is testable without touching the real `~/.config/upkeep/`.
  Covered by `ModulesAddCommandTest::testMissingTokenExplainsTheSourcesInsteadOfFatallyErroring()`.

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
- **RESOLVED (task 6, 2026-08-03):** `TokenResolver` gained an injected warning
  sink (`?\Closure(string): void`, defaulting to a no-op). After a successful
  read it `stat()`s the file and, when `($mode & 0o077) !== 0`, emits **one**
  warning per resolver naming the path, the octal mode, and `chmod 600 <path>`.
  It does **not** refuse. The message contains no token material.
  `GitlabClientFactory::resolver()` wires the sink to `SymfonyStyle::warning()`
  so the warning actually reaches the operator. Covered by
  `TokenResolverTest::testGroupOrWorldReadableTokenFileWarnsWithoutEchoingTheToken()`,
  `::testWarnsOnlyOnceAcrossRepeatedResolves()`, `::testPrivateTokenFileDoesNotWarn()`
  and `GitlabClientFactoryTest::testForConsoleSurfacesTheTokenFilePermissionWarning()`.

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
- **RESOLVED (task 6, 2026-08-03):** New `Gitlab\GitlabClientFactory` is the one
  seam: `missingTokenMessage()` produces the single wording from
  `describeSources()`, `fromResolvedToken()` reports it and returns null, and
  `forConsole()` does both with the warning sink wired. All nine sites now route
  through it — `AbstractMrCommand::resolveContext()` (throws `WorkflowException`
  carrying the shared message), `ApiProbeCommand`, `NotesCommand`,
  `DashboardCommand`, `MergeCommand`, `IssueCommand`, `NeedsWorkCommand`,
  `ModulesAddCommand`, and `PatchesCommand` (V4's silent path now emits the
  message as a **warning**, once, since missing-token there is a documented
  degraded mode rather than a failure). Exit codes were left as they are: D2
  assigns the 0/1/2 unification to task 9, which now has one message site to
  work from. Covered by `GitlabClientFactoryTest`.

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
- **RESOLVED (task 6, 2026-08-03):** `TokenResolver::resolve()` now takes the
  **first non-empty line** of either source (`firstLine()`), and refuses a value
  containing characters illegal in an HTTP header value
  (`/[\x00-\x1F\x7F]/`), returning null and warning through the injected sink.
  The diagnostic names the source (env var or file path), never the value.
  Covered by `TokenResolverTest::testOnlyTheFirstNonEmptyLineOfTheConfigFileIsUsed()`
  and `::testValueWithCharactersIllegalInAnHttpHeaderIsRejectedWithoutEchoingIt()`.

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
- **RESOLVED (task 6, 2026-08-03):** Both layers implemented.
  **Layer 1 (unreachable):** new `Security\CredentialEnvironment::scrubbed()`
  returns `[TokenResolver::DEFAULT_ENV_VAR => false]`, passed as Process's `$env`
  at **every** construction site in `src/` — `ProcessRunner::start()` and
  `::capture()`, `ExecCommand`, `DiskUsage`, `BaseArtifactBuilder`,
  `IssueCommand`, `NeedsWorkCommand`. Verified against Symfony 7's
  `Process::start()` filter `if (false !== $v && ...)`.
  **Layer 2 (redacted):** new `Security\SecretRedactor` (literal replacement of
  the secret values in play, longest first, values under 8 bytes ignored) is
  injected into `ProcessRunner` — defaulting to `SecretRedactor::fromEnvironment()`
  — and applied at every exit: the `run()` failure and timeout messages, the log
  closures in both `start()` and `capture()`, and the combined output returned in
  `CapturedProcess`. Because `CapturedProcess::$output` is redacted at source,
  `CheckCommand::renderSummary()`'s excerpt and `ResultsCache::store()`'s
  persisted `'output'` field inherit the guarantee with no change to either file.
  Covered by `ProcessRunnerTest` (sentinel in argv *and* output → present in
  neither the exception message nor the log stream; child-environment absence
  with a non-scrubbed control process; redaction surviving into the cached
  results JSON), `SecretRedactorTest`, and the structural
  `ProcessEnvironmentInvariantTest`, which asserts the scrub at every
  `new Process(` in `src/` so a future site cannot silently reopen the hole.

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
- **RESOLVED (task 6, 2026-08-03):** `ProcessRunner::run()` now bounds the
  interpolated child output to the last `ProcessRunner::FAILURE_OUTPUT_BYTES`
  (4000, mirroring `ResultsCache::OUTPUT_EXCERPT_BYTES`) and states the
  truncation in the message (`[output truncated to the last N bytes]`). The
  whole message is passed through the redactor. Covered by
  `ProcessRunnerTest::testFailureMessageBoundsUnboundedChildOutput()`.

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
- **RESOLVED (task 6, 2026-08-03):** `ProcessRunner::start()` now catches
  `ProcessTimedOutException` and reports it through a by-reference `$timedOut`
  flag. `run()` converts it to an `AdapterException` naming the elapsed timeout
  and the command line (redacted); `tryRun()` returns null. `capture()` keeps
  reporting the timeout as data. Covered by
  `ProcessRunnerTest::testRunConvertsATimeoutIntoAnAdapterException()`,
  `::testTryRunReturnsNullOnTimeoutInsteadOfThrowing()` and
  `::testCaptureStillReportsATimeoutAsData()`.

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
- **RESOLVED (task 6, 2026-08-03):** New `Adapter\ShellArgument::quote()` applies
  deterministic POSIX single-quoting (not `escapeshellarg()`, whose output is
  platform-dependent, while the script always runs in a Linux container), and
  `DdevContribAdapter::checkCommand()` quotes `$environment->moduleName` before
  splicing it into the `bash -c` script body. The safety no longer depends on
  `ProjectName::for()` two classes away. Covered by `ShellArgumentTest`, which
  round-trips metacharacters through a real `bash -c`.

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
- **RESOLVED (task 6, 2026-08-03):** Covered by SEC-PROC-01 layer 1 —
  `ExecCommand::execute()` passes `CredentialEnvironment::scrubbed()`, so
  `upkeep exec <module> -- printenv` no longer prints the PAT. Covered by
  `ExecCommandTest::testDoesNotForwardTheGitlabCredentialToTheChild()`.
  **Follow-up for task 19 (documentation):** this is an operator-visible
  behaviour change — `upkeep exec` no longer forwards `UPKEEP_GITLAB_TOKEN` to
  the child — and should be noted in `README.md` alongside D2's exec exit-code
  change.

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
- **RESOLVED (task 7, 2026-08-03):** New `Adapter\MountablePath::requireUnderHome()`
  canonicalises a candidate path and asserts it is under `$HOME`, throwing
  `AdapterException` whose message states the reason (environments are
  bind-mounted into the Docker VM; macOS providers only share the home
  directory), names the resolved path, and points at the default.
  `ProjectsRoot::resolve()` routes all four sources — `--projects-root`,
  `$UPKEEP_PROJECTS_ROOT`, the cockpit `projects/` dir, and the `~/.upkeep`
  default — through it, and `BaseArtifactsBuildCommand` holds `--scratch-dir`
  to the same rule its help text promises. Covered by `ProjectsRootTest`
  (`testAnExplicitProjectsRootOutsideHomeIsRefusedWithTheDockerMountReason`,
  `testTheEnvironmentVariableIsHeldToTheSameHomeContainmentRule`,
  `testATraversalEscapeOutOfHomeIsRefusedEvenThoughItStartsInsideHome`,
  `testASymlinkOutOfHomeIsRefusedThoughNoDotDotAppearsInTheSpelling`).
  **Note:** no escape hatch was added — the constraint is enforced with no
  opt-out.

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
- **RESOLVED (task 7, 2026-08-03):** New `Filesystem\PathGuard` provides
  `canonicalize()` (realpath, falling back to canonicalising the deepest
  existing ancestor for a path being created, and refusing an unresolvable
  `..` in the not-yet-existing tail) and `isWithin()`. `Cockpit::__construct()`
  canonicalises the root once, so `..` and symlinked spellings no longer
  survive into any derived path and two spellings of one cockpit compare
  equal. `PruneSelector::protectionReason()` now compares protected roots by
  path identity via `PathGuard::isWithin()` (lexical prefix only as a
  last-resort fallback for an uncanonicalisable path, which can only protect
  more). `InventoryScanner::snapshots()` skips a symlinked `materialized/`
  directory and records a warning. Covered by `PathGuardTest`, `CockpitTest`,
  `PruneSelectorTest::testProtectionCompareUsesPathIdentityNotStringSpelling`,
  `…::testASiblingSharingAStringPrefixWithAProtectedRootIsNotProtected`, and
  `InventoryScannerTest::testASymlinkedMaterializedDirectoryIsNotEnumerated`.

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
- **RESOLVED (task 7, 2026-08-03):** `ProjectName` gained public predicates
  `isModuleName()` / `isCoreMajor()`; `ModuleRegistry::buildModule()` validates
  the registry key and every `core_versions` entry with them at load, raising
  `RegistryException` naming the offending key. `ResultsCache::entryDir()`
  re-asserts both (it is a public API) and `store()`/`find()` require the SHA
  to match `/^[0-9a-f]{7,64}$/` before it becomes a filename — `store()`
  throws, `find()`/`latest()` treat an impossible identity as a miss. Covered
  by `ModuleRegistryTest::testRejectsAModuleNameThatIsNotADrupalMachineName`
  (8 cases), `…::testRejectsACoreVersionThatIsNotAWholeMajorVersion`, and
  `ResultsCacheTest::testAShaThatIsNotAShaIsRefusedBeforeItBecomesAFilename`
  (6 cases), `…::testAModuleNameThatIsNotAMachineNameNeverBecomesAPathSegment`,
  `…::testAReadWithAnInvalidShaIsAMissRatherThanACrash`.

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
- **RESOLVED (task 7, 2026-08-03):** Closed at `ModuleRegistry` per SEC-FS-03,
  and `DashboardCache::path()` re-asserts the machine name itself. Covered by
  `DashboardCacheTest::testAModuleNameThatIsNotAMachineNameNeverBecomesAFilename`.

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
- **RESOLVED (task 7, 2026-08-03):** `ResultsCache` and `DashboardCache` now
  create their directories `0700` and their files `0600` via
  `Filesystem\FileWriter` (which takes an explicit mode and chmods the temp
  file before the rename, since `file_put_contents` cannot take one). Covered
  by `ResultsCacheTest::testStoredResultsAreOwnerOnlyOnDiskBecauseTheyCarryRawCheckOutput`
  and `DashboardCacheTest::testCachedSnapshotsAreOwnerOnlyBecauseTheyCarryTokenScopedRemoteData`.
  **On-disk impact:** files already written keep their old mode; an existing
  cockpit needs a one-off
  `chmod -R go-rwx <cockpit>/results <cockpit>/cache`. Still open for the
  documentation task: telling users to exclude `results/` and `cache/` from
  version control.

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
- **RESOLVED (task 7, 2026-08-03):** `RegistryEditor::add()` now writes the
  exact final bytes to a `tempnam()`-created sibling (unpredictable name, so
  concurrent `modules:add` runs cannot collide and a same-user symlink plant
  on `registry.yml.probe` has nothing to redirect), validates *that file*
  through `ModuleRegistry::fromFile()`, and `rename()`s it into place —
  unlinking it only on the rejection path. A `RegistryException` from the
  probe is rethrown with the temp path rewritten to the real registry path so
  the message names the file the user asked to change. Covered by
  `RegistryEditorTest::testTheValidatedBytesAreRenamedIntoPlaceSoACrashCannotDestroyTheRegistry`,
  `…::testAFailedWriteIsReportedRatherThanReturningTheNamesAsAdded`,
  `…::testAValidationFailureNamesTheRealRegistryNotTheTemporaryFile`.

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
- **RESOLVED (task 7, 2026-08-03):** New `Filesystem\FileWriter::write()` /
  `writeTemporary()` / `commit()` / `ensureDirectory()` check every step and
  throw `Filesystem\FilesystemException` naming the path. All 11 sites now
  route through it — `grep -rn file_put_contents src/ bin/` matches only the
  one call inside the helper. The five false-success paths:
  `InitCommand` reports the scaffold failure and returns FAILURE instead of
  "Cockpit created" (also `is_file()` per SEC-FS-14);
  `ModulesAddCommand` catches `FilesystemException` alongside
  `RegistryException` so it cannot print "Registered N module(s)" over an
  unmodified registry; `DdevContribAdapter::provision()`'s `.upkeep-env.yml`
  completion marker write is checked, so a missing marker can no longer make
  the tool re-provision forever; `BaseArtifactBuilder::doBuild()`'s `meta.yml`
  and `canonical` writes are checked (the enclosing catch then removes the
  partial set); `ResultsCache::store()` throws before `CheckCommand` prints
  "Results cached". `DashboardCache::save()`'s unchecked `mkdir` is now
  `FileWriter::ensureDirectory()`. Covered by `FileWriterTest` (14 tests),
  `InitCommandTest`, `RegistryEditorTest`, `ResultsCacheTest`,
  `DashboardCacheTest`. Not directly covered by a test: the
  `DdevContribAdapter` and `BaseArtifactBuilder` sites, which need a live
  engine / network; they inherit the helper's guarantee.

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
- **RESOLVED (task 7, 2026-08-03):** Every write in `src/` is now
  temp-file-plus-`rename()` through `FileWriter`, including both
  read-modify-write cycles (`wireModule()`'s `composer.json`,
  `adaptAddOnConfig()`'s `config.contrib.yaml`), whose reads no longer cast a
  possible `false` to `''` but raise `AdapterException`. `DashboardCache::load()`
  is now lenient in the same documented way as `ResultsCache::read()`. The
  favourable behaviours recorded in this finding were left alone:
  `ResultsCache::read()` stays lenient, `.upkeep-env.yml` stays the
  last-written completion marker (and is now *rewritten* by rename, so the
  path never stops existing during a `last_used_at` stamp), and
  `BaseArtifactBuilder`'s catch → `rm -rf` → rethrow is untouched. Covered by
  `FileWriterTest::testWriteIsAtomicSoAReaderNeverObservesAPartialFile`,
  `…::testWriteReplacesTheTargetInPlaceRatherThanUnlinkingItFirst`,
  `ResultsCacheTest::testAStoreIsAtomicSoAReaderNeverSeesAPartialResultFile`,
  `DashboardCacheTest::testASaveIsAtomicSoAReaderNeverSeesAPartialSnapshot`,
  `…::testATornCacheFileDegradesToAMissRatherThanAnUncaughtException`,
  `EnvironmentMetaTest::testStampingLastUseRewritesTheDotfileWithoutEverRemovingIt`.

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
- **RESOLVED (task 7, 2026-08-03):** `PruneExecutor::execute()` drops the `@`,
  checks the `unlink` return, and only then adds to `$freed`/`$deleted`; a
  failed removal goes into `$skipped` with the reason. A sidecar `.meta` that
  cannot be removed is logged but does not fail the snapshot's accounting.
  Covered by
  `PruneExecutorTest::testASnapshotThatCannotBeRemovedIsSkippedNotReportedAsFreedSpace`.

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
- **RESOLVED (task 7, 2026-08-03):** Took the first option — the field is now
  written, matching the comment and the CLI help's last-use semantics. Chosen
  over deleting the branch because `--older-than` on a destructive command
  should mean "not used since", and creation-time semantics let prune delete
  an environment reused yesterday; renaming the concept would have made the
  command permanently less useful rather than fixing it.
  `EnvironmentMeta` gained an optional `lastUsedAt`, emitted as `last_used_at`
  by `toYaml()` and read (optionally, with a real timestamp parse) by
  `fromYaml()`, plus `withLastUsedAt()`, `writeTo()` and
  `stampLastUsed()`. `DdevContribAdapter::provision()` stamps it at creation
  and `::reuse()` re-stamps it on every reuse, atomically, so the completion
  marker never disappears mid-stamp. Covered by `EnvironmentMetaTest`
  (`testLastUsedAtRoundTripsSoAgeFilteringHasSomethingToRead`,
  `testAMetaWithoutLastUsedAtStillParses`,
  `testAnUnparseableLastUsedAtIsRefusedRatherThanSilentlyIgnored`,
  `testStampingLastUseRewritesTheDotfileWithoutEverRemovingIt`).
  **On-disk impact:** `.upkeep-env.yml` gains a key; existing environments
  parse unchanged and fall back to `created_at` until next reused, so no
  rebuild is required.

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
- **RESOLVED (task 7, 2026-08-03):** The false `@var` assertion is gone.
  `ResultsCache::read()` narrows the decoded value with real runtime checks
  (`is_array`, `is_string`, `is_int`, `is_numeric` per field), so the guards
  are both true and statically visible; PHPStan no longer reports
  `booleanNot.alwaysFalse` there. Covered by
  `ResultsCacheTest::testAResultFileWhoseShapeIsWrongDegradesToAMiss` (three
  wrong shapes) and `…::testMissesAndMalformedFilesReadAsNull`.

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
- **RESOLVED (task 7, 2026-08-03):** Closed by the `ModuleRegistry::buildModule()`
  validation described under SEC-FS-03: an invalid machine name is now a
  `RegistryException` at load — the exception every command already handles —
  rather than an uncaught `InvalidArgumentException` from
  `PruneExecutor::resolveEnvironment()` after environments have been torn
  down.

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
- **RESOLVED (task 7, 2026-08-03):** `Cockpit::resolve()` now refuses an empty
  `--cockpit` with a message naming the flag, falls through an empty env var
  to the working directory, and fails explicitly when `getcwd()` returns
  false. The root is canonicalised and `rtrim`ed in the constructor, so
  `--cockpit=/foo/` and `/foo` produce one path. Covered by `CockpitTest`
  (`testAnEmptyCockpitOptionIsRefusedRatherThanRootingEveryPathAtTheFilesystemRoot`,
  `testAnEmptyEnvironmentVariableFallsThroughToTheWorkingDirectory`,
  `testATrailingSlashDoesNotProduceADoubleSlashInDerivedPaths`).

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
- **RESOLVED (task 7, 2026-08-03):** `InitCommand` uses `is_file()`, and the
  SEC-FS-07 write helper surfaces a directory at that path as the real
  failure. Covered by
  `InitCommandTest::testADirectoryNamedRegistryYmlIsNotReportedAsAnExistingCockpit`.

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
- **RESOLVED (task 7, 2026-08-03):** `ArtifactScanner::directorySize()` passes
  `\RecursiveIteratorIterator::CATCH_GET_CHILD`, so an unreadable subtree
  under-counts instead of throwing `UnexpectedValueException` out of
  `base-artifacts:status`. The verified-favourable symlink behaviour was left
  intact and is now stated in the method's docblock so it is not "fixed" away
  later. Covered by
  `ArtifactScannerTest::testAnUnreadableSubdirectoryUnderCountsInsteadOfThrowing`.

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
- **RESOLVED (task 7, 2026-08-03):** `false`/unreadable is now distinguished
  from empty at every site, with the direction chosen per consumer:
  `InventoryScanner` records a warning naming the directory and carries on
  (under-reporting fails safe for prune) and exposes `warnings()`, which
  `status` and `prune` print; `ArtifactLayout::versionsOnDisk()` throws
  `FilesystemException` on an unreadable base-artifacts directory, which
  `ArtifactScanner`/`base-artifacts:status` surfaces and `InventoryScanner`
  catches into a warning; `ResultsCache::latest()` throws rather than
  reporting "never checked", since that was the one site failing in the
  misleading direction. Covered by
  `InventoryScannerTest::testAnUnreadableDirectoryIsWarnedAboutRatherThanReportedAsEmpty`,
  `…::testACleanScanReportsNoWarnings`,
  `…::testAnUnreadableBaseArtifactsDirectoryDegradesToAWarningRatherThanFailingThePruneScan`,
  `ArtifactScannerTest::testAnUnreadableBaseArtifactsDirectoryIsReportedRatherThanReadAsEmpty`,
  `ResultsCacheTest::testAnUnreadableResultsDirectoryIsReportedRatherThanReadAsNeverChecked`.

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
- **RESOLVED (task 8, 2026-08-03):** No `message()` method was introduced; the
  uniform accessor surface is the public promoted property `ApiFailure::$message`
  (now on the sealed base alongside `?int $status` and `?string $browserUrl`),
  plus two abstract methods `shortCode()` and `isTransient()`. Task 6 had already
  corrected the call site to `$projects->message`, and that remains correct —
  every consumer in `src/` now reaches message text the same way. Verified by
  `ModulesAddCommandTest` plus PHPStan, which no longer reports
  `method.notFound` for `ApiFailure`.

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
- **RESOLVED (task 8, 2026-08-03):** Decided in favour of a distinct type. New
  `Gitlab\ResourceMissing` covers "the call succeeded, the collection just does
  not contain what you asked for": `status` is null, `shortCode()` is `missing`,
  and the message reads `No tag "9.9.9" in project/foo. Check in the browser: …`
  — it never claims an HTTP status that did not occur. `NotFound` is now strictly
  HTTP 404, so its "HTTP 404" wording is always true. A tag that IS present but
  carries no commit date (so the date cannot be resolved) is `MalformedResponse`
  rather than being silently reported as absent. Covered by
  `ErrorTaxonomyTest::testDomainLevelTagMissIsResourceMissingAndNeverClaimsAnHttpStatus`
  and two `GitlabClientTest` cases.

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
- **RESOLVED (task 8, 2026-08-03):** Split as proposed. `EndpointClosed` is now
  403-only and keeps the browser fallback; new `Gitlab\Unauthorized` handles 401
  and advises re-checking the credential, naming the sources via the new
  `TokenResolver::describeDefaultSources()` (a static twin of `describeSources()`
  so a failure raised deep in the client needs no resolver instance). The advice
  contains no token material, and a regression test asserts the message does not
  say "Use the browser instead". `DashboardRow::failureCell()` needed no
  per-type edit (see BP-GL-04). `MergeCommand::mergeOne()`'s
  "GitLab refuses API merges here" branch is now unreachable for a bad token —
  `Unauthorized` falls through to the failure branch whose message names the
  credential. Covered by `ErrorTaxonomyTest` (401 vs 403 data sets and
  `testUnauthorizedAdvisesTheCredentialAndNotTheBrowser`).

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
- **RESOLVED (task 8, 2026-08-03):** `ApiFailure` gained `abstract public
  shortCode(): string`, implemented by all eight concrete types (`401`, `403`,
  `404`, `missing`, `rate-limited`, the status for `RequestRejected`,
  `malformed`, `transport`). Both byte-identical `failureName()` copies are
  deleted from `NotesCommand` and `ApiProbeCommand` — no reflection remains in
  production code — and `DashboardRow::failureCell()` is now
  `'n/a (' . $failure->shortCode() . ')'`, so the `match (true)` and its
  unreachable `default` arm are gone (nothing left for task 13/16 to fail to
  cover). Existing dashboard output is unchanged: `n/a (403)` still renders as
  `n/a (403)`.

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
- **RESOLVED (task 8, 2026-08-03):** `?int $status` and `?string $browserUrl`
  are lifted onto `ApiFailure` and populated by every subtype the client can
  raise, so `browserUrl` is available for a rate limit and a transport failure
  too. `status` is now honest rather than merely present: it is the received
  status where a response arrived (401/403/404/429/4xx/5xx, and the 2xx of a
  `MalformedResponse`) and null where none did (`TransportError`,
  `ResourceMissing`). `MergeCommand::mergeOne()`'s failure branch now prints the
  browser URL for ANY failure type carrying one, not only `EndpointClosed`.

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
- **RESOLVED (task 8, 2026-08-03):** `ApiFailure::isTransient()` is abstract, so
  each type states its own answer, and `GitlabClient::get()` stores a result only
  when it is a success or a non-transient failure. `RateLimited`,
  `TransportError`, `MalformedResponse` and `RequestRejected` are transient and
  are never memoized; `Unauthorized`, `EndpointClosed` and `NotFound` are stable
  and still are (rate-limit friendliness preserved). The lookup moved from
  `??=` to `array_key_exists()` so the two concerns stay separable. Covered by
  `ErrorTaxonomyTest::testTransientFailuresAreNotMemoized` (four data sets, each
  proving a second attempt re-hits the transport and succeeds) and
  `testStableFailuresStayMemoized`.

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
- **RESOLVED (task 8, 2026-08-03):** `GitlabClient::MAX_PAGES = 50` caps the
  loop, and a page that is not a list (`array_is_list()`) is now
  `MalformedResponse` rather than being fed to `Project::fromApi()` or spun on.
  Exhausting the cap is also `MalformedResponse`. The RED run for this finding
  hung the suite on the real unbounded loop before the fix, which is the
  strongest evidence the defect was live. Covered by
  `ErrorTaxonomyTest::testMembershipPaginationTreatsANonListResponseAsMalformedRatherThanLoopingForever`
  and `testMembershipPaginationIsCapped`.

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
- **RESOLVED (task 8, 2026-08-03):** The orphaned block was moved back onto
  `tags()` — the documentation defect itself, not the symptom. Nothing was
  suppressed and no second `@return` was added, so the note to tasks 10/11
  stands satisfied: PHPStan's `Cannot access property $createdAt/$name on mixed`
  inside `mergedSinceTag()` is gone at source. Total PHPStan errors fell 289 →
  284 across this task with no new baseline entries.

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
- **RESOLVED (task 8, 2026-08-03):** `GitlabClient::request()` now catches
  `Symfony\Contracts\HttpClient\Exception\ExceptionInterface` — the root of
  every symfony/http-client contract exception, transport and otherwise — and
  maps it to `TransportError`, making the documented "never throws for
  HTTP-level outcomes" contract true. The `\JsonException` catch was removed as
  dead weight rather than widened: body decoding no longer uses
  `JSON_THROW_ON_ERROR`, and an undecodable or non-array body is
  `MalformedResponse` carrying `json_last_error_msg()`. Covered by
  `ErrorTaxonomyTest::testNoRawSymfonyHttpClientExceptionEscapes`, a data
  provider over `TransportException`, `TimeoutException` and `ServerException`
  (a non-transport contract exception the old code let escape), plus the same
  proof on a write path via `merge()`.

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
- **RESOLVED (task 9, 2026-08-03):** One seam on the new `Command\UpkeepCommand`
  base: `cockpit()` (the documented `--cockpit` > `$UPKEEP_COCKPIT` > cwd
  order), `modules()` (loads and validates the registry) and
  `requireCockpit()` (both, for commands needing a cockpit but not its
  contents). Policies B, C and D collapse into it: nothing catches
  `RegistryException`/`FilesystemException` locally any more — `execute()` is
  final on the base and maps every domain exception to
  `ExitCode::INFRASTRUCTURE` with the exception's own message, so
  `ModuleRegistry::fromFile()`'s wording ("Run `upkeep init` ...") is what the
  operator sees everywhere. The four verbatim `file_exists()` pre-checks are
  deleted. `NotesCommand::resolveProjectPath()`'s deliberate fallback stays
  and now also tolerates `FilesystemException`, with the reason in a comment.

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
- **RESOLVED (task 9, 2026-08-03):** `PruneCommand::perform()` calls
  `$this->modules($cockpit)` before the inventory scan, so the registry is
  parsed before any reclaim plan is rendered. The modules are then handed
  straight to `PruneExecutor`; the late `$cockpit->loadRegistry()` is gone.

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
- **RESOLVED (task 9, 2026-08-03):** Extended to the whole CLI per D2. The
  three codes now read 0 the command did what was asked / 1 the work it
  supervised reported failure / 2 upkeep could not do the job, and
  `CHECKS_FAILED` was renamed `FAILED` to be honest about covering merges and
  `exec` as well as checks. The mapping lives in exactly one place:
  `UpkeepCommand::execute()` is final and catches `WorkflowException`,
  `AdapterException`, `RegistryException`, `FilesystemException`,
  `BuildException` and `MetaException`. All 19 commands extend the base
  (asserted by `CommandSurfaceTest::testEveryCommandExtendsTheSharedBase`) and
  no `Command::SUCCESS/FAILURE/INVALID` remains in `src/`. **"No token" is now
  2 everywhere**, matching `ExitCode`'s stated meaning — no credential means
  no verdict was produced. The exception: `patches` without a token is a
  documented degraded mode, warns once and still exits 0.

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
- **RESOLVED (task 9, 2026-08-03):** `MergeCommand::perform()` ends with
  `self::outcome($tally)`: `INFRASTRUCTURE` when a merge was refused with
  `Unauthorized` (the operator's credential, not a verdict on any MR),
  `FAILED` when `$tally['failed'] > 0`, else `OK`. The `Command::INVALID`
  collision is gone — the missing-`--fast-lane` guard throws a
  `WorkflowException` and so maps to `INFRASTRUCTURE`, which now means one
  thing. Covered by `MergeCommandTest`'s
  `::testMergeFailureIsReportedPerMrAndTheLoopContinues`,
  `::testARejectedCredentialExitsWithTheInfrastructureCode` and
  `::testOmittingFastLaneIsAnInfrastructureFailure`.

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
- **RESOLVED (task 9, 2026-08-03):** Decided by D2 against the recorded
  recommendation: **no exemption**. `ExecCommand` returns
  `ExitCode::forChildProcess($process->getExitCode())` — 0 stays 0, every
  non-zero child code collapses to 1, and a child that produced no exit code
  at all is 2. This is an operator-visible behaviour change for anyone
  scripting `upkeep exec`; task 19 documents it in `README.md`. Covered by
  `ExitCodeTest::testEveryNonZeroChildCodeCollapsesToFailed` and
  `ExitCodeContractTest::testWrappedCommandFailureCollapsesToTheFailedCode`
  (child codes 1, 2, 42, 127 through the console).

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
- **RESOLVED (task 9, 2026-08-03):** The pipeline is now
  `Dashboard\RowFactory` — (module, project, merge requests) → rows, expanded
  across tracked core versions and classified by `FastLaneGate`. It takes MRs
  it did not fetch, which is what lets both consumers share it:
  `RowAssembler` feeds it live client data (so `MergeCommand` is unchanged)
  and `DashboardCommand` feeds it its cached `ModuleSnapshot`. The dashboard's
  inline project → MRs → gate loop and its inline `--version` filtering are
  deleted. Covered by `RowFactoryTest`.

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
- **RESOLVED (task 9, 2026-08-03):** Decided by D1 as reading (a), stronger
  than the recorded recommendation (c): a **full DI refactor**. New
  `Adapter\EngineAdapterFactory` interface with the single production
  implementation `Adapter\DdevContribAdapterFactory`; commands receive the
  factory by constructor injection and call
  `create($cockpit, $projectsRootOption, $stageLog, $processLog)`. All five
  `new DdevContribAdapter(...)` sites are gone; `grep -rn "new DdevContribAdapter" src/`
  now matches only inside `src/Adapter/`. `bin/upkeep` is the composition root
  and the only place outside `src/Adapter/` that names an engine — it also
  builds the `SecretRedactor` and the `VolumeProbe` and injects both.
  **`grep -ri "ddev" src/ --exclude-dir=Adapter` is silent**, so the guard now
  tests what it claims; task 19 updates `CLAUDE.md` to the `-i` form.
  `src/Maintenance/`'s documented "volume" vocabulary is untouched, per (c).

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
- **RESOLVED (task 9, 2026-08-03):** `UpkeepCommand` defines them once:
  `addCockpitOption()`, `addProjectsRootOption()`, `addModuleArgument()`,
  `addMrArgument()`, `addNoOpenOption()`, and — because `--version` genuinely
  carries two meanings — `addTargetCoreOption()` (the selector) and
  `addCoreFilterOption()` (the dashboard's row filter), each with one
  canonical wording. `PatchesCommand` keeps `--module` as an option because it
  filters a whole-registry scan rather than naming a subject, which is a
  different thing from the positional argument. `CommandSurfaceTest` fails if
  any of these descriptions drift apart again.

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
- **RESOLVED (task 9, 2026-08-03):** `MrContextResolver::requireModule()` is
  now public and is the only registry lookup: `resolve()` uses it, and
  `UpkeepCommand::requireModule()` delegates to it for `dev`, `env:path`,
  `exec`, `issue`, `patches` (`--module`) and `needs-work`. All six sites emit
  the chosen wording — the one that lists the registered modules — through the
  base's error path, so the raw `<error>` writelns are gone too.

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
- **RESOLVED (task 9, 2026-08-03):** `MrContextResolver::selectCoreVersion()`
  is public and reached through `UpkeepCommand::targetCore()`; the three
  verbatim copies in `DevCommand`, `EnvPathCommand` and `ExecCommand` are
  deleted. The message is the resolver's ("does not track core version ...
  Add it to core_versions in registry.yml"), which names the fix.

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
- **RESOLVED (task 9, 2026-08-03):** One validator,
  `UpkeepCommand::mrIid()`, taking the stricter rule (`/^\d+$/` **and**
  `>= 1`), used by `AbstractMrCommand`, `NeedsWorkCommand` and — for the first
  time — `IssueCommand`, so `upkeep issue widget abc` no longer silently
  becomes a request for `!0`. Covered by
  `IssueCommandTest::testANonPositiveIntegerMrArgumentIsRejectedBeforeAnyApiCall`
  over letters, 0, a negative, a decimal and the empty string.

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
- **RESOLVED (task 9, 2026-08-03):** (1) and (7) fall out of the BP-CMD-07
  factory and the injected `VolumeProbe`; the three divergent logging
  policies are replaced by the factory's two explicit sinks, so `exec` and
  `env:path` discard engine output deliberately and by one decision.
  (2) `Command\BrowserOpener::open()`. (3) `Command\ColumnTable::render()`,
  used by both wide tables. (4) `PatchesCommand::truncate()` deleted in favour
  of `DashboardRow::truncate()`. (5) `BaseArtifactsStatusCommand::formatBytes()`
  deleted in favour of `Maintenance\ByteFormat::human()`. (6) one
  `CheckResult::EXCERPT_BYTES` and one `CheckResult::outputExcerpt()`, used by
  `check` and `needs-work` (`CheckResultExcerptTest`). (8)
  `VolumeProbe::itemsForInventory()` (`VolumeProbeInventoryTest`).
  (9) already removed by task 8.

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
- **RESOLVED (task 9, 2026-08-03):** `Cockpit::RESULTS_DIR`,
  `Cockpit::DASHBOARD_CACHE_DIR`, `Cockpit::resultsPath()` and
  `Cockpit::dashboardCachePath()` added, matching the existing pattern; all
  six literal call sites now use them, and `grep -rn "'/results'" src/` is
  silent. The on-disk layout is unchanged, so existing cockpits are unaffected
  (asserted explicitly in `CockpitTest`).

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
- **RESOLVED (task 9, 2026-08-03):** Real guards:
  `DashboardRow::requireMergeRequest()`, `::requireProject()` and
  `::requireVerdict()` throw a `WorkflowException` naming the row state, which
  the base maps to exit 2 instead of a `TypeError` from inside the GitLab
  client. Every `\assert()` in `MergeCommand` and `DashboardRow` is gone
  (`grep -rn "\\assert(" src/` matches only a docblock). Covered by
  `DashboardRowGuardTest`, which is meaningful precisely because it does not
  depend on `zend.assertions`.

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
- **RESOLVED (task 9, 2026-08-03):** `DevCommand::buildAdapter()` and its
  unreachable `if ($adapter === null)` guard are both gone — the command now
  takes an `EngineAdapterFactory` whose `create()` is non-nullable. The
  PHPStan `return.unusedType` error for it no longer appears.

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
- **RESOLVED (task 9, 2026-08-03):** Renamed `--core` to `--version` and the
  stale comment is replaced by one stating the real reason the flag arrives
  intact (`VersionOptionInput` plus the `bin/upkeep` definition filter). The
  "no artifacts yet" hint now says `--version=N`. Operator-visible CLI change;
  task 19 updates `README.md`. Guarded by
  `CommandSurfaceTest::testBaseArtifactsBuildSelectsItsCoreVersionLikeEveryOtherCommand`.

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

**All three were decided by the maintainer on 2026-08-03 and recorded in
`DECISIONS.md`; task 9 implemented the decisions:**

1. **BP-CMD-05** → decided **against** the recommendation (D2): no exemption.
   `upkeep exec` collapses every non-zero child code to 1 and no longer passes
   the child's raw code through.
2. **BP-CMD-07** → decided **beyond** the recommendation (D1): the full DI
   refactor of reading (a), not the middle path (c). Nothing outside
   `bin/upkeep` and `src/Adapter/` names an engine, and
   `grep -ri "ddev" src/ --exclude-dir=Adapter` is silent.
3. **BP-CMD-03** → decided **as** recommended (D2): the contract extends to
   the whole CLI surface, with "no token" resolved to 2 uniformly.

Everything else in this record has a single defensible resolution and needs no
further input.
