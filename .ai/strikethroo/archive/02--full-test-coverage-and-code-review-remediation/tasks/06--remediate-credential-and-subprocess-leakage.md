---
id: 6
group: "review-and-remediation"
dependencies: [5]
status: "completed"
created: 2026-08-02
skills:
  - php
  - security
complexity_score: 6
complexity_notes: "Security-critical and cross-cutting between src/Gitlab and src/Adapter, but the surface is narrow and the findings are already enumerated by task 5."
---
# Remediate credential handling and subprocess output leakage

## Objective

Guarantee that GitLab token material cannot reach logs, printed output,
persisted files, or exception messages by any path, including failure paths,
and resolve the credential-file permission finding.

## Skills Required

`php` for the implementation across `src/Gitlab/` and `src/Adapter/`;
`security` for correct redaction design and threat reasoning.

## Acceptance Criteria

- [ ] Every credential-handling and subprocess-leakage finding assigned to this task in `review-findings.md` is resolved, and the record is updated with each resolution.
- [ ] Token material is redacted at the reporting boundary in `Adapter\ProcessRunner`, covering both the `getCommandLine()` interpolation and the combined child output in the `AdapterException` message, and covering the streaming log closure.
- [ ] A test asserts that a subprocess failure whose command line and output both contain a sentinel token value produces an exception message and log stream containing **neither** the sentinel.
- [ ] A test asserts that `Gitlab\TokenResolver::describeSources()` output contains the env var name and config file path but no token material.
- [ ] The credential-file permission decision from task 5 is implemented (warn, refuse, or documented no-op) and covered by a test.
- [ ] `vendor/bin/phpunit` passes.
- [ ] `grep -r "ddev" src/ --exclude-dir=Adapter` returns nothing.
- [ ] The suite still runs with no network, no docker, and no GitLab token.

Use your internal Todo tool to track these and keep on track.

## Technical Requirements

- `Adapter\ProcessRunner` is `final readonly` and takes a `\Closure(string): void`
  log callback. Its three entry points — `run()`, `tryRun()`, and `capture()` —
  all stream child output through that closure, and `run()` additionally builds
  a failure message from `getExitCode()`, `getCommandLine()`, and the trimmed
  combined output.
- `Gitlab\TokenResolver` resolves from the `UPKEEP_GITLAB_TOKEN` env var, then
  `$XDG_CONFIG_HOME/upkeep/drupal-pat` or `~/.config/upkeep/drupal-pat`.
- No backwards-compatibility constraint applies: signatures, constructors, and
  the exception message format may all change.

## Input Dependencies

- Task 5: the findings record, specifically the credential and subprocess
  findings with their proposed resolutions.

## Output Artifacts

- Redaction implemented in the process-reporting boundary.
- Credential-file permission handling.
- Tests proving no leakage, which tasks 13 and 14 build on for coverage.
- Updated `review-findings.md` with resolutions recorded.

## Implementation Notes

<details>
<summary>Detailed implementation guidance</summary>

1. Read `review-findings.md` first and work only the findings assigned to this
   task. Do not expand scope beyond them.

2. The redaction design should be centralised, not scattered. The cleanest
   approach is a single redactor that knows the set of secret values currently
   in play and scrubs them from any string headed for a log, an exception
   message, or a file. Inject it into `ProcessRunner` rather than reaching for
   a global — the class is `final readonly` and already takes its log closure
   by constructor injection, so follow that pattern.

3. Apply redaction at **every** exit from `ProcessRunner`, not just the
   `run()` failure path: the log closure in `start()` and in `capture()`
   streams raw child output line by line, and `capture()` returns the full
   combined output inside `CapturedProcess`, which is later persisted as a
   cached result. All three are sinks.

4. For the test, use a recognisable sentinel (for example
   `SENTINEL-TOKEN-VALUE`), arrange a failing process whose argv and output
   both contain it, and assert the sentinel appears in neither the exception
   message nor anything the log closure received. This is the test that
   validation step 10 of the plan mirrors at the CLI level.

5. Implement whichever credential-file permission decision task 5 recorded. If
   the decision was to warn on a group- or world-readable
   `~/.config/upkeep/drupal-pat`, make the warning message itself
   token-free — the irony of leaking a token while warning about its file
   permissions is a real risk. Test with a fixture file whose mode you set
   explicitly.

6. No-BC applies: change signatures freely if that produces a better design.
   You do not need to preserve the current `AdapterException` message format.

7. Keep the suite hermetic. Nothing added here may require docker, the
   network, or a real token.

</details>
