# Plan 02 — Self-Validation Report

**Executed**: 2026-08-03
**Branch**: `feature/2--full-test-coverage-and-code-review-remediation`
**HEAD**: `676bee0` (`docs: document the quality gates and every operator-visible change`)
**Environment**: Linux 6.12.95 aarch64, PHP 8.4.24 (cli) with PCOV 1.0.12, Composer 2.x
**Working tree at start**: clean

This report records the thirteen steps of the plan's "Self Validation" section,
in order, with the actual captured output of each. Nothing here is asserted
without evidence. Where a step could not be run, it is marked
**NOT VERIFIABLE** with the reason, not marked pass.

## Result summary

| # | Step | Result |
| --- | --- | --- |
| 1 | `composer install` in a clean checkout | **PASS** |
| 2 | 100% line coverage over `src/` | **PASS** |
| 3 | Coverage gate is live (fails when a test is removed) | **PASS** |
| 4 | PHPStan level max, zero errors, no baseline | **PASS** |
| 5 | phpcs PSR-12, zero errors and zero warnings | **PASS** |
| 6 | Adapter-boundary grep silent | **PASS** |
| 7 | Suppression budget, with justifications and total count | **PASS** — total **0** |
| 8 | `./bin/upkeep list` registers every command, exits 0 | **PASS** |
| 9 | Exit-code contract 0 / 1 / 2 from the shell | **PASS** |
| 10 | Sentinel token appears in no output and no written file | **PASS** |
| 11 | Missing credential reported with `describeSources()` wording | **PASS** |
| 12 | Suite passes with networking disabled and docker unreachable | **PASS** |
| 13 | GitHub Actions green on all three matrix legs | **NOT VERIFIABLE** here |

Twelve steps pass on captured evidence. Step 13 cannot be executed in this
environment and is reported as unverified rather than as a pass.

**One code defect was found during step 9 and is recorded in "Defects found"
below. Per the task constraints it was not fixed.**

---

## Step 1 — `composer install --no-interaction` in a clean checkout

Run in a fresh `git clone` of the repository at HEAD `676bee0`, in a scratch
directory outside the working tree, with `vendor/` and `composer.lock` absent.

```
$ git clone <repo> clean && cd clean && git checkout 676bee0
$ composer install --no-interaction
No composer.lock file present. Updating dependencies to latest instead of installing from lock file. See https://getcomposer.org/install for more information.
Loading composer repositories with package information
Updating dependencies
Lock file operations: 44 installs, 0 updates, 0 removals
  - Locking myclabs/deep-copy (1.13.4)
  - Locking nikic/php-parser (v5.8.0)
  ...
  - Locking phpstan/phpstan (2.2.7)
  - Locking phpunit/phpunit (11.5.56)
  - Locking squizlabs/php_codesniffer (4.0.1)
  ...
Generating autoload files
40 packages you are using are looking for funding.
EXIT=0
```

All seven direct requirements resolved, dev dependencies included:

```
$ composer show --direct
phpstan/phpstan           2.2.7   PHPStan - PHP Static Analysis Tool
phpunit/phpunit           11.5.56 The PHP Unit Testing framework.
squizlabs/php_codesniffer 4.0.1   PHP_CodeSniffer tokenizes PHP files and d...
symfony/console           8.1.2   Eases the creation of beautiful and testa...
symfony/http-client       8.1.3   Provides powerful methods to fetch HTTP r...
symfony/process           8.1.0   Executes commands in sub-processes
symfony/yaml              8.1.2   Loads and dumps YAML files

$ ls vendor/bin/
php-parse  phpcbf  phpcs  phpstan  phpstan.phar  phpunit  yaml-lint

$ composer validate --strict
./composer.json is valid
VALIDATE_EXIT=0
```

**PASS.** No unresolved dev dependencies.

*Observation, not a failure*: `composer.lock` is in `.gitignore`, so a clean
checkout has no lock file and `composer install` resolves to latest. This is
conventional for a library, but it means the three CI matrix legs each resolve
a different dependency set — see the risk note under step 13.

---

## Step 2 — `vendor/bin/phpunit --coverage-text`, 100% of lines for `src/`

```
$ vendor/bin/phpunit --coverage-text
PHPUnit 11.5.56 by Sebastian Bergmann and contributors.

Runtime:       PHP 8.4.24 with PCOV 1.0.12
Configuration: /home/owen.guest/github.com/owenbush/upkeep/phpunit.xml.dist

................................................................ 834 / 834 (100%)

Time: 00:04.006, Memory: 34.00 MB

OK (834 tests, 2353 assertions)

Generating code coverage report in HTML format ... done [00:00.232]

Code Coverage Report:
  2026-08-03 16:15:40

 Summary:
  Classes: 100.00% (110/110)
  Methods: 100.00% (470/470)
  Lines:   100.00% (4035/4035)
EXIT=0
```

Note that the coverage driver is present and reporting: the run prints
`with PCOV 1.0.12` and there is **no**
`WARNING: the 100.00% line-coverage threshold was NOT enforced` line, so the
threshold was genuinely applied rather than skipped for want of a driver.

### Per-namespace roll-up (derived from the captured per-class table)

```
Namespace                 Classes      Lines    Covered
Upkeep\Adapter                 23        890        890  100.00%
Upkeep\BaseArtifact             6        183        183  100.00%
Upkeep\Cockpit                  4        126        126  100.00%
Upkeep\Command                 24       1415       1415  100.00%
Upkeep\Config                   1          6          6  100.00%
Upkeep\Dashboard                5        170        170  100.00%
Upkeep\Drupal                   6        223        223  100.00%
Upkeep\Filesystem               2         79         79  100.00%
Upkeep\Gate                     2         28         28  100.00%
Upkeep\Gitlab                  19        417        417  100.00%
Upkeep\Maintenance             10        298        298  100.00%
Upkeep\Notes                    1         42         42  100.00%
Upkeep\Results                  2         87         87  100.00%
Upkeep\Security                 2         19         19  100.00%
Upkeep\Workflow                 3         52         52  100.00%
TOTAL                         110       4035       4035
```

### Full per-class table as emitted

```
Upkeep\Adapter\CapturedProcess              Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Adapter\CheckResult                  Methods: 100.00% ( 6/ 6)   Lines: 100.00% ( 19/ 19)
Upkeep\Adapter\CheckRunResult               Methods: 100.00% ( 3/ 3)   Lines: 100.00% (  6/  6)
Upkeep\Adapter\CheckStatus                  Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Adapter\DdevContribAdapter           Methods: 100.00% (26/26)   Lines: 100.00% (422/422)
Upkeep\Adapter\DdevContribAdapterFactory    Methods: 100.00% ( 2/ 2)   Lines: 100.00% (  7/  7)
Upkeep\Adapter\EngineAddOn                  Methods: 100.00% ( 1/ 1)   Lines: 100.00% ( 15/ 15)
Upkeep\Adapter\EngineDescription            Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 14/ 14)
Upkeep\Adapter\Environment                  Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Adapter\EnvironmentMeta              Methods: 100.00% ( 8/ 8)   Lines: 100.00% ( 81/ 81)
Upkeep\Adapter\FixtureAddOn                 Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  2/  2)
Upkeep\Adapter\ModuleWiring                 Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 23/ 23)
Upkeep\Adapter\MountablePath                Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 32/ 32)
Upkeep\Adapter\MrCheckout                   Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 25/ 25)
Upkeep\Adapter\ProcessRunner                Methods: 100.00% ( 9/ 9)   Lines: 100.00% ( 57/ 57)
Upkeep\Adapter\ProjectName                  Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 13/ 13)
Upkeep\Adapter\ProjectsRoot                 Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 16/ 16)
Upkeep\Adapter\ServeResult                  Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Adapter\ShellArgument                Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Adapter\SnapshotLayout               Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  5/  5)
Upkeep\Adapter\ThrowawaySite                Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 36/ 36)
Upkeep\Adapter\VolumeProbe                  Methods: 100.00% ( 6/ 6)   Lines: 100.00% ( 48/ 48)
Upkeep\Adapter\WorkingCopyStatus            Methods: 100.00% ( 6/ 6)   Lines: 100.00% ( 64/ 64)
Upkeep\BaseArtifact\ArtifactLayout          Methods: 100.00% ( 8/ 8)   Lines: 100.00% ( 26/ 26)
Upkeep\BaseArtifact\ArtifactMeta            Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 46/ 46)
Upkeep\BaseArtifact\ArtifactRecord          Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\BaseArtifact\ArtifactScanner         Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 42/ 42)
Upkeep\BaseArtifact\BaseArtifactBuilder     Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 57/ 57)
Upkeep\BaseArtifact\ComposerLock            Methods: 100.00% ( 1/ 1)   Lines: 100.00% ( 11/ 11)
Upkeep\Cockpit\Cockpit                      Methods: 100.00% ( 9/ 9)   Lines: 100.00% ( 25/ 25)
Upkeep\Cockpit\Module                       Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Cockpit\ModuleRegistry               Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 72/ 72)
Upkeep\Cockpit\RegistryEditor               Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 28/ 28)
Upkeep\Command\AbstractMrCommand            Methods: 100.00% ( 6/ 6)   Lines: 100.00% ( 42/ 42)
Upkeep\Command\ApiProbeCommand              Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 53/ 53)
Upkeep\Command\BaseArtifactsBuildCommand    Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 57/ 57)
Upkeep\Command\BaseArtifactsStatusCommand   Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 23/ 23)
Upkeep\Command\BrowserOpener                Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  4/  4)
Upkeep\Command\CheckCommand                 Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 69/ 69)
Upkeep\Command\ColumnTable                  Methods: 100.00% ( 1/ 1)   Lines: 100.00% ( 19/ 19)
Upkeep\Command\DashboardCommand             Methods: 100.00% ( 9/ 9)   Lines: 100.00% (154/154)
Upkeep\Command\DevCommand                   Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 29/ 29)
Upkeep\Command\EnvPathCommand               Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 27/ 27)
Upkeep\Command\ExecCommand                  Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 36/ 36)
Upkeep\Command\InitCommand                  Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 35/ 35)
Upkeep\Command\IssueCommand                 Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 56/ 56)
Upkeep\Command\MergeCommand                 Methods: 100.00% ( 8/ 8)   Lines: 100.00% (159/159)
Upkeep\Command\ModulesAddCommand            Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 87/ 87)
Upkeep\Command\ModulesCommand               Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 14/ 14)
Upkeep\Command\NeedsWorkCommand             Methods: 100.00% ( 4/ 4)   Lines: 100.00% (105/105)
Upkeep\Command\NotesCommand                 Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 37/ 37)
Upkeep\Command\PatchesCommand               Methods: 100.00% ( 9/ 9)   Lines: 100.00% (112/112)
Upkeep\Command\PruneCommand                 Methods: 100.00% ( 4/ 4)   Lines: 100.00% (119/119)
Upkeep\Command\ReviewCommand                Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 21/ 21)
Upkeep\Command\StatusCommand                Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 64/ 64)
Upkeep\Command\UpkeepCommand                Methods: 100.00% (19/19)   Lines: 100.00% ( 90/ 90)
Upkeep\Command\VersionOptionInput           Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  3/  3)
Upkeep\Config\BotPattern                    Methods: 100.00% ( 4/ 4)   Lines: 100.00% (  6/  6)
Upkeep\Dashboard\DashboardCache             Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 27/ 27)
Upkeep\Dashboard\DashboardRow               Methods: 100.00% (14/14)   Lines: 100.00% ( 43/ 43)
Upkeep\Dashboard\ModuleSnapshot             Methods: 100.00% ( 9/ 9)   Lines: 100.00% ( 51/ 51)
Upkeep\Dashboard\RowAssembler               Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 24/ 24)
Upkeep\Dashboard\RowFactory                 Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 25/ 25)
Upkeep\Drupal\ApiPayload                    Methods: 100.00% ( 7/ 7)   Lines: 100.00% ( 14/ 14)
Upkeep\Drupal\DrupalOrgClient               Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 53/ 53)
Upkeep\Drupal\Issue                         Methods: 100.00% ( 9/ 9)   Lines: 100.00% ( 84/ 84)
Upkeep\Drupal\IssueFile                     Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 26/ 26)
Upkeep\Drupal\IssueReference                Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 19/ 19)
Upkeep\Drupal\IssueStatus                   Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 27/ 27)
Upkeep\Filesystem\FileWriter                Methods: 100.00% ( 6/ 6)   Lines: 100.00% ( 42/ 42)
Upkeep\Filesystem\PathGuard                 Methods: 100.00% ( 4/ 4)   Lines: 100.00% ( 37/ 37)
Upkeep\Gate\FastLaneGate                    Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 25/ 25)
Upkeep\Gate\GateVerdict                     Methods: 100.00% ( 2/ 2)   Lines: 100.00% (  3/  3)
Upkeep\Gitlab\ApiFailure                    Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Gitlab\ApiPayload                    Methods: 100.00% ( 8/ 8)   Lines: 100.00% ( 19/ 19)
Upkeep\Gitlab\EndpointClosed                Methods: 100.00% ( 3/ 3)   Lines: 100.00% (  7/  7)
Upkeep\Gitlab\GitlabClient                  Methods: 100.00% (17/17)   Lines: 100.00% (176/176)
Upkeep\Gitlab\GitlabClientFactory           Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 20/ 20)
Upkeep\Gitlab\MalformedResponse             Methods: 100.00% ( 3/ 3)   Lines: 100.00% (  3/  3)
Upkeep\Gitlab\MergeRequest                  Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 36/ 36)
Upkeep\Gitlab\MergeRequestList              Methods: 100.00% ( 6/ 6)   Lines: 100.00% (  6/  6)
Upkeep\Gitlab\NotFound                      Methods: 100.00% ( 3/ 3)   Lines: 100.00% (  7/  7)
Upkeep\Gitlab\Pipeline                      Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 16/ 16)
Upkeep\Gitlab\PipelineStatus                Methods: 100.00% ( 2/ 2)   Lines: 100.00% (  2/  2)
Upkeep\Gitlab\Project                       Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 16/ 16)
Upkeep\Gitlab\RateLimited                   Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 12/ 12)
Upkeep\Gitlab\RequestRejected               Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 12/ 12)
Upkeep\Gitlab\ResourceMissing               Methods: 100.00% ( 3/ 3)   Lines: 100.00% (  7/  7)
Upkeep\Gitlab\Tag                           Methods: 100.00% ( 2/ 2)   Lines: 100.00% ( 14/ 14)
Upkeep\Gitlab\TokenResolver                 Methods: 100.00% ( 9/ 9)   Lines: 100.00% ( 48/ 48)
Upkeep\Gitlab\TransportError                Methods: 100.00% ( 3/ 3)   Lines: 100.00% (  3/  3)
Upkeep\Gitlab\Unauthorized                  Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 12/ 12)
Upkeep\Maintenance\ByteFormat               Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  7/  7)
Upkeep\Maintenance\Category                 Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  7/  7)
Upkeep\Maintenance\DiskUsage                Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  7/  7)
Upkeep\Maintenance\Duration                 Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  6/  6)
Upkeep\Maintenance\InventoryItem            Methods: 100.00% ( 2/ 2)   Lines: 100.00% (  2/  2)
Upkeep\Maintenance\InventoryScanner         Methods: 100.00% (14/14)   Lines: 100.00% (128/128)
Upkeep\Maintenance\PruneExecutor            Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 72/ 72)
Upkeep\Maintenance\PruneOutcome             Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Maintenance\PruneScope               Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  6/  6)
Upkeep\Maintenance\PruneSelector            Methods: 100.00% ( 8/ 8)   Lines: 100.00% ( 62/ 62)
Upkeep\Notes\NotesGenerator                 Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 42/ 42)
Upkeep\Results\CachedResult                 Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Results\ResultsCache                 Methods: 100.00% ( 7/ 7)   Lines: 100.00% ( 86/ 86)
Upkeep\Security\CredentialEnvironment       Methods: 100.00% ( 2/ 2)   Lines: 100.00% (  7/  7)
Upkeep\Security\SecretRedactor              Methods: 100.00% ( 3/ 3)   Lines: 100.00% ( 12/ 12)
Upkeep\Workflow\ExitCode                    Methods: 100.00% ( 2/ 2)   Lines: 100.00% (  6/  6)
Upkeep\Workflow\MrContext                   Methods: 100.00% ( 1/ 1)   Lines: 100.00% (  1/  1)
Upkeep\Workflow\MrContextResolver           Methods: 100.00% ( 5/ 5)   Lines: 100.00% ( 45/ 45)
```

**PASS.** 110/110 classes, 470/470 methods, 4035/4035 lines.

---

## Step 3 — Proving the coverage gate is live

`tests/Security/SecretRedactorTest.php` (10 assertion-bearing statements) was
moved out of the test tree, and the suite re-run unchanged.

```
$ mv tests/Security/SecretRedactorTest.php <scratch>/SecretRedactorTest.php.bak
$ vendor/bin/phpunit
PHPUnit 11.5.56 by Sebastian Bergmann and contributors.

Runtime:       PHP 8.4.24 with PCOV 1.0.12
Configuration: /home/owen.guest/github.com/owenbush/upkeep/phpunit.xml.dist

................................................................ 827 / 827 (100%)

Time: 00:04.037, Memory: 34.00 MB

OK (827 tests, 2343 assertions)

Generating code coverage report in HTML format ... done [00:00.214]

FAILURE: line coverage 99.95% (4033/4035 lines) is below the required minimum of 100.00%.
EXIT=1
```

This is the distinction the step exists to prove: PHPUnit itself reports
**`OK (827 tests, 2343 assertions)`** — no test failed — and the process still
exits **1**, solely because line coverage fell to 99.95%. The gate is live, not
merely reported.

Restored and re-verified:

```
$ mv <scratch>/SecretRedactorTest.php.bak tests/Security/SecretRedactorTest.php
restored
$ git status --porcelain
(no output)
$ vendor/bin/phpunit
OK (834 tests, 2353 assertions)
EXIT=0
```

**PASS.** File restored; tree clean; suite green.

---

## Step 4 — PHPStan level max, zero errors, no baseline

```
$ vendor/bin/phpstan analyse --no-progress
Note: Using configuration file /home/owen.guest/github.com/owenbush/upkeep/phpstan.neon.dist.

 [OK] No errors

PHPSTAN_EXIT=0
```

Configuration in force, and the baseline search:

```
$ cat phpstan.neon.dist
parameters:
    level: max
    paths:
        - src
        - tests

$ find . -name 'phpstan-baseline.neon' -not -path './vendor/*'
(no output)
```

**PASS.** Level max over `src/` and `tests/`, zero errors, no baseline file
anywhere in the repository.

---

## Step 5 — phpcs, zero errors and zero warnings

```
$ vendor/bin/phpcs
............................................................  60 / 222 (27%)
............................................................ 120 / 222 (54%)
............................................................ 180 / 222 (81%)
..........................................                   222 / 222 (100%)


Time: 2.81 secs; Memory: 16MB
PHPCS_EXIT=0
```

222 files checked, no summary table emitted (phpcs prints one only when there
are findings), exit 0. Scope confirmed from `phpcs.xml.dist`: `src`, `bin`,
`tests`, plus `bin/upkeep` explicitly (it carries no `.php` extension),
`<rule ref="PSR12"/>`.

**PASS.** Zero errors, zero warnings.

---

## Step 6 — Adapter-boundary grep

```
$ grep -r "ddev" src/ --exclude-dir=Adapter
GREP_EXIT=1        # 1 = no match

$ grep -ri "ddev" src/ --exclude-dir=Adapter
GREP_I_EXIT=1      # 1 = no match
```

Both the plan's case-sensitive form and the stricter case-insensitive form
documented in `CLAUDE.md` (where the `-i` is called out as load-bearing)
produce no output.

**PASS.**

---

## Step 7 — Suppression budget and total count

```
$ grep -rn "codeCoverageIgnore\|phpstan-ignore\|phpcs:ignore" src/
GREP_EXIT=1
count: 0

$ grep -rn "codeCoverageIgnore\|phpstan-ignore\|phpcs:ignore\|phpcs:disable" src/ tests/ bin/
count: 0
```

**Total suppression count: 0.** Not one `@codeCoverageIgnore`,
`@phpstan-ignore`, `phpcs:ignore` or `phpcs:disable` exists in `src/`, `tests/`
or `bin/`, and no `phpstan-baseline.neon` exists (step 4). The
"every hit carries an adjacent justification" requirement is therefore
vacuously satisfied — there are no hits to justify.

This is the honest reading of success criterion 9: 100% coverage and PHPStan
max were reached by fixing or restructuring code, never by suppressing a tool.

**PASS.**

---

## Step 8 — `./bin/upkeep list`

```
$ ./bin/upkeep list
Upkeep dev

Usage:
  command [options] [arguments]

Options:
  -h, --help            Display help for the given command. ...
      --silent          Do not output any message
  -q, --quiet           Only errors are displayed. ...
      --ansi|--no-ansi  Force (or disable --no-ansi) ANSI output
  -n, --no-interaction  Do not ask any interactive question
  -v|vv|vvv, --verbose  Increase the verbosity of messages ...

Available commands:
  check                  Run one merge request through the full isolated check flow and cache the per-check results.
  completion             Dump the shell completion script
  dashboard              Show every open MR across registered modules and core versions ...
  dev                    Prepare an environment for active development ...
  exec                   Run a command in a module's environment directory.
  help                   Display help for a command
  init                   Scaffold a new cockpit ...
  issue                  Show and open the drupal.org issue linked to a merge request.
  list                   List commands
  merge                  Fast-lane merge: prompt per READY-AUTO merge request ...
  modules                List the modules registered in the cockpit module registry.
  needs-work             Post local check results as a comment on the merge request.
  notes                  Draft paste-ready Markdown release notes ...
  patches                Show drupal.org issues with patches but no merge request.
  prune                  Reclaim disposable state ...
  review                 Apply a merge request to a running site and print its browsable URL.
  status                 Report cockpit state ...
 api
  api:probe              Probe the git.drupalcode.org GitLab API for a module ...
 base-artifacts
  base-artifacts:build   Build the canonical per-core-version base artifacts ...
  base-artifacts:status  List which core versions have base artifacts ...
 env
  env:path               Print the absolute path of a module's environment directory.
 modules
  modules:add            Register maintained modules from your git.drupalcode.org project memberships (interactive opt-in).

LIST_EXIT=0
```

All **19** upkeep commands register (`check`, `dashboard`, `dev`, `exec`,
`init`, `issue`, `merge`, `modules`, `needs-work`, `notes`, `patches`, `prune`,
`review`, `status`, `api:probe`, `base-artifacts:build`,
`base-artifacts:status`, `env:path`, `modules:add`), matching the "all 19
commands" figure in `BEHAVIOUR-CHANGES.md`, plus Symfony's own `completion`,
`help` and `list`. The application-level `-V/--version` is absent from the
Options block, as `CLAUDE.md` requires (it is deliberately removed so
`--version` can only ever mean the target-core selector).

**PASS.** Exit 0.

---

## Step 9 — The exit-code contract, checked from the shell

`$?` was read directly after each invocation; no code was inferred from output
text.

A **throwaway cockpit** was created at `$HOME/upkeep-validation-cockpit`. The
real `~/.upkeep/` and `~/.config/upkeep/` were never touched — neither exists
on this machine, and neither was created (verified after cleanup, below).

Setup (which itself exercises the 0 leg):

```
$ ./bin/upkeep init "$HOME/upkeep-validation-cockpit"
 [OK] Cockpit created at "/home/owen.guest/upkeep-validation-cockpit":
      registry.yml, base-artifacts/, fixtures/, projects/.
EXIT=0
```

Registry seeded with one module (`widget`, `project/widget`, core `["11"]`) and
a fake provisioned environment at
`<cockpit>/projects/upkeep-widget-d11/.upkeep-env.yml`.

### 0 — the command did what was asked

```
$ upkeep modules --cockpit=$CK                          EXIT=0
$ upkeep status --cockpit=$CK                           EXIT=0
$ upkeep exec widget --cockpit=$CK -- true              EXIT=0
$ upkeep env:path widget --cockpit=$CK                  EXIT=0
    /home/owen.guest/upkeep-validation-cockpit/projects/upkeep-widget-d11
```

### 1 — the work it supervised failed

```
$ upkeep exec widget --cockpit=$CK -- false             EXIT=1
$ upkeep exec widget --cockpit=$CK -- sh -c 'exit 42'   EXIT=1
```

The second is the `BEHAVIOUR-CHANGES.md` item 1 collapse: a child exiting **42**
yields **1**, not 42, so a child's 2 can never masquerade as an upkeep
infrastructure failure.

The other documented 1-producing paths — a red check and a GitLab-refused merge
— require docker and a live credential, so they were not driven from the shell
here. They are asserted hermetically in `tests/Command/ExitCodeContractTest.php`:

```
tests/Command/ExitCodeContractTest.php:90:  assertSame(ExitCode::FAILED, $cli->run('check', 'widget', '5'), ...)
tests/Command/ExitCodeContractTest.php:179: assertSame(ExitCode::FAILED, $cli->run('merge', '--fast-lane'), ...)
tests/Command/ExitCodeContractTest.php:273: assertSame(ExitCode::FAILED, $exit, sprintf('child exit %d', $childCode))
```

### 2 — upkeep could not do the job

```
$ upkeep exec nosuchmod --cockpit=$CK -- true                                   EXIT=2
 [ERROR] Module "nosuchmod" is not registered in the cockpit. Registered modules: widget.

$ upkeep exec widget --cockpit=$CK --version=12 -- true                          EXIT=2
 [ERROR] Module "widget" does not track core version "12". Its registry entry tracks: 11.
         Add it to core_versions in registry.yml to check against it.

$ upkeep status --cockpit=$HOME/definitely-not-a-cockpit                         EXIT=2
 [ERROR] Module registry not found at ".../registry.yml". Run "upkeep init" to create
         a cockpit, or point --cockpit / UPKEEP_COCKPIT at an existing one.

$ upkeep issue widget abc --cockpit=$CK                                          EXIT=2
 [ERROR] The <mr> argument must be a merge request IID (a positive integer), got "abc".

$ upkeep exec widget --cockpit=$CK --projects-root=/tmp -- true                  EXIT=2
 [ERROR] Refusing the projects root "/tmp" (resolves to "/tmp"): it is outside your home
         directory "/home/owen.guest". ... Point --projects-root or $UPKEEP_PROJECTS_ROOT
         at a path under "/home/owen.guest".

$ upkeep exec widget --cockpit=$CK --projects-root=$HOME/<empty> -- true          EXIT=2
 [ERROR] No provisioned environment for widget on Drupal 11. Run `upkeep check`
         or `upkeep review` to create one.
```

These confirm `BEHAVIOUR-CHANGES.md` items 3, 7 and 12 as shipped.

### The documented Symfony nuance, re-verified

`BEHAVIOUR-CHANGES.md` item 7's correction states that console-level parse
failures are handled by Symfony's `Application` *before*
`UpkeepCommand::execute()` runs, so they exit **1**, while upkeep's own
validation exits **2**. Confirmed exactly:

```
$ upkeep nosuchcommand                                       EXIT=1
$ upkeep issue                       # missing arguments      EXIT=1
$ upkeep base-artifacts:build --core=11    # removed option   EXIT=1
$ upkeep init --cockpit=...          # wrong flag shape       EXIT=1

$ upkeep base-artifacts:build --cockpit=$CK   # upkeep's own validation
 [ERROR] The --version option is required (e.g. --version=11).
                                                              EXIT=2
```

**PASS.** All three legs of the contract confirmed from `$?`, and the
documented exit-1 nuance holds as written.

---

## Step 10 — Sentinel token leak check

Sentinel: `glpat-SENTINEL-DO-NOT-LEAK-8f3a91c2`, exported as
`UPKEEP_GITLAB_TOKEN` for every invocation below.

### 10a. The child process does not inherit the variable at all

```
$ UPKEEP_GITLAB_TOKEN=<sentinel> upkeep exec widget --cockpit=$CK -- sh -c 'env'
$ grep -c 'SENTINEL'            <child env dump>    0
$ grep -c 'UPKEEP_GITLAB_TOKEN' <child env dump>    0
$ grep '^UPKEEP' <child env dump>                   (none)
```

`Security\CredentialEnvironment::scrubbed()` removes the variable rather than
blanking it: the child sees no such name at all. `BEHAVIOUR-CHANGES.md` item 15
confirmed.

### 10b. GitLab command paths with the sentinel set

Every GitLab-touching command was run with the sentinel exported. The token is
bogus, so git.drupalcode.org answers 401 — which is exactly the error path of
interest. Output of all of `check`, `review`, `api:probe`, `dashboard`,
`dashboard --refresh`, `notes`, `needs-work`, `patches`, `merge`, `issue`,
`dev`, `status --disk`, `prune`, `base-artifacts:status` was captured and
grepped:

```
$ grep -n 'SENTINEL\|glpat-' <combined output of all runs>
GREP_EXIT=1        # 1 = not found
```

Representative message — it names the *sources* and never the value:

```
 [ERROR] Cannot resolve the GitLab project for module "widget" (project/widget):
         GitLab rejected the credential (HTTP 401): the token is missing, invalid,
         expired, or lacks the required scope. Re-check the token configured in:
         env var UPKEEP_GITLAB_TOKEN, config file /home/.../.config/upkeep/drupal-pat.
         (The token is never printed or logged.)
```

### 10c. Forced subprocess failure with the sentinel *in the child's own output*

The weaker test is a child that never sees the token. The real threat is a
child that emits token material by another route (a remote URL, a config dump)
into output that upkeep logs, renders, and caches. A shim `ddev` was placed
first on `PATH` that prints the sentinel and exits 128, then a teardown was
driven through it:

```
$ upkeep prune --trees --yes
...
Deleting engine project upkeep-widget-d11 (containers + volumes) ...
  fatal: could not read from remote repository https://oauth2:[REDACTED]@git.drupalcode.org/project/widget.git
  env dump: UPKEEP_GITLAB_TOKEN=<unset-in-child>
Engine delete reported a failure (project may not be registered) — continuing with tree removal.
...
$ grep -n 'SENTINEL\|glpat-' <output>
GREP_EXIT=1        # 1 = clean
```

Both layers visible in one line: the child reports the variable as
`<unset-in-child>` (layer 1, `CredentialEnvironment`), and the value it printed
from elsewhere is masked to `[REDACTED]` (layer 2, `SecretRedactor`).

The `ProcessRunner::run()` failure path — the one the plan singled out, because
it interpolates `getCommandLine()` **and** the full combined child output into
an `AdapterException` message shown to the operator — was driven with a failing
`composer` shim:

```
$ upkeep base-artifacts:build --version=11 --scratch-dir=$HOME/upkeep-validation-scratch
Resolving drupal/recommended-project:^11 into .../base-artifacts/11/tree ...
  Composer failed: remote https://oauth2:[REDACTED]@git.drupalcode.org rejected
  child UPKEEP_GITLAB_TOKEN=<unset-in-child>
Build failed — removing partial artifact set at .../base-artifacts/11

 [ERROR] Command failed (1): 'composer' 'create-project' 'drupal/recommended-project:^11'
         '.../base-artifacts/11/tree' '--no-interaction'
         Composer failed: remote https://oauth2:[REDACTED]@git.drupalcode.org rejected
         child UPKEEP_GITLAB_TOKEN=<unset-in-child>
EXIT=2

$ grep -n 'SENTINEL\|glpat-' <output>
GREP_EXIT=1        # 1 = clean
```

### 10d. The persistence sink — files written under `<cockpit>/results/`

This is the sink the plan flags as easiest to forget, because it survives the
process. The GitLab 401 prevented `check` from reaching the cache, so the
sink was driven directly through the same production classes — a real
`ProcessRunner::capture()` of a child that prints the sentinel, into a real
`Results\ResultsCache::store()` under the throwaway cockpit:

```
LOG:   auth: https://oauth2:[REDACTED]@git.drupalcode.org failed
LOG:   inherited=<unset-in-child>
stored

$ find <cockpit>/results -exec ls -ld {} \;
drwx------ 3 owen owen 4096 .../results
drwx------ 3 owen owen 4096 .../results/widget
drwx------ 3 owen owen 4096 .../results/widget/7
drwx------ 2 owen owen 4096 .../results/widget/7/11
-rw------- 1 owen owen  408 .../results/widget/7/11/aaaa...aaaa.json

$ cat .../results/widget/7/11/aaaa...aaaa.json
{
    "sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "recorded_at": "2026-08-03T16:21:08+00:00",
    "results": [
        {
            "type": "phpunit",
            "status": "failed",
            "exit_code": 1,
            "output": "auth: https://oauth2:[REDACTED]@git.drupalcode.org failed\ninherited=<unset-in-child>\n",
            "duration_seconds": 0.0020999908447265625
        }
    ]
}

$ grep -rn 'SENTINEL\|glpat-' <cockpit>/results
GREP_EXIT=1        # 1 = clean
```

The cached JSON carries `[REDACTED]`, not the token. The `0700` directories and
`0600` file confirm `BEHAVIOUR-CHANGES.md` item 11 as shipped.

### 10e. Whole-cockpit sweep

```
$ grep -rn 'SENTINEL\|glpat-' $HOME/upkeep-validation-cockpit
GREP_EXIT=1        # 1 = not found anywhere in the cockpit
```

**PASS.** The sentinel appears in no rendered output, no exception message, no
log line, no cached result file, and not in any child process's environment.

*Design note, not a defect*: `Results\ResultsCache::store()` performs no
redaction of its own — it relies on `CheckResult::$output` having already been
redacted at the single `ProcessRunner` boundary, which is what
`ProcessRunner`'s own docblock commits to ("everything leaving this class …
including the combined output in `CapturedProcess`, which callers persist").
The single-boundary design is defensible and was verified end to end above; it
is recorded here only so the dependency is explicit.

---

## Step 11 — Missing credential reported via `describeSources()`

`UPKEEP_GITLAB_TOKEN` unset (via `env -u`, so the variable is absent rather
than empty), and `~/.config/upkeep/drupal-pat` confirmed not to exist on this
machine.

```
$ upkeep api:probe widget                                                   EXIT=2
$ upkeep notes widget                                                       EXIT=2
$ upkeep issue widget 1 --no-open                                           EXIT=2
$ upkeep needs-work widget 1                                                EXIT=2
$ upkeep merge --fast-lane                                                  EXIT=2
$ upkeep modules:add                                                        EXIT=2
$ upkeep dashboard                                                          EXIT=2
```

All seven print exactly the `describeSources()` wording
(`env var %s, config file %s`), wrapped by SymfonyStyle:

```
 [ERROR] No GitLab token found. Configure one of: env var UPKEEP_GITLAB_TOKEN,
         config file /home/owen.guest/.config/upkeep/drupal-pat. (The token is
         never printed or logged.)
```

The documented degraded-mode exception (`BEHAVIOUR-CHANGES.md` item 6) behaves
as specified — a warning, not an error, and exit **0**:

```
$ upkeep patches --module=widget
 [WARNING] No GitLab token found. Configure one of: env var UPKEEP_GITLAB_TOKEN,
           config file /home/owen.guest/.config/upkeep/drupal-pat. (The token is
           never printed or logged.)
 ! [NOTE] No dashboard cache and no GitLab token — showing all matching issues
 !        without cross-referencing MRs.
 [OK] No orphan issues found — all Needs Review / RTBC issues have corresponding MRs.
EXIT=0
```

Token-material sweep over all of it:

```
$ grep -n 'glpat-\|SENTINEL' <combined output>
GREP_EXIT=1        # 1 = not found
```

The seven exit-2 results also confirm `BEHAVIOUR-CHANGES.md` item 6 ("no token
now exits 2, not 1") for `api:probe`, `dashboard`, `merge`, `notes`, `issue`,
`needs-work` and `modules:add`.

**PASS.**

---

## Step 12 — Hermeticity: no network, docker unreachable

### The known pitfall, diagnosed rather than reported as a failure

`unshare -rn` maps the caller to uid 0 inside the namespace, and root bypasses
filesystem permission checks — so the tests that deliberately create unreadable
directories cannot observe the refusal they assert. This was reproduced
deliberately so the diagnosis is on the record rather than assumed:

```
$ unshare -rn -- sh -c 'echo "uid = $(id -u)"; vendor/bin/phpunit --no-coverage'
uid inside unshare -rn = 0
...
13) Upkeep\Tests\Results\ResultsCacheTest::testAFailedStoreIsReportedRatherThanLettingTheCallerClaimResultsWereCached
Failed asserting that exception of type "Upkeep\Filesystem\FilesystemException" is thrown.

14) Upkeep\Tests\Results\ResultsCacheTest::testAnUnreadableResultsDirectoryIsReportedRatherThanReadAsNeverChecked
Failed asserting that exception of type "Upkeep\Filesystem\FilesystemException" is thrown.

FAILURES!
Tests: 834, Assertions: 2339, Failures: 14.
```

Exactly the 14 permission-assertion failures, `uid = 0`, and **not one of them
network-related**. This is an artefact of the isolation technique, not a
hermeticity defect. (The same run also demonstrates the documented
`--no-coverage` limitation working as `CLAUDE.md` describes: `WARNING: the
100.00% line-coverage threshold was NOT enforced: no coverage data was
collected.`)

### The real run — uid preserved

`unshare --user --map-current-user --net` keeps the caller's uid, so the
permission assertions remain meaningful. Docker was made unreachable two ways
at once: `DOCKER_HOST` pointed at a non-existent socket, and `docker`, `ddev`,
`docker-compose` and `colima` were shadowed on `PATH` by shims that log every
invocation and fail with a daemon-down message.

```
$ unshare --user --map-current-user --net -- env \
    PATH=<shims>:$PATH DOCKER_HOST=unix:///nonexistent/docker.sock \
    sh -c '...; vendor/bin/phpunit'

uid=501
docker check:
  docker_exit=1
net check:
  curl_exit=7            # Could not connect to server
=== suite ===
PHPUnit 11.5.56 by Sebastian Bergmann and contributors.

Runtime:       PHP 8.4.24 with PCOV 1.0.12
Configuration: /home/owen.guest/github.com/owenbush/upkeep/phpunit.xml.dist

................................................................ 834 / 834 (100%)

Time: 00:04.015, Memory: 34.00 MB

OK (834 tests, 2353 assertions)

Generating code coverage report in HTML format ... done [00:00.217]
EXIT=0
```

The shim invocation log is the positive evidence that the suite never reached
for an engine at all:

```
$ cat engine-invocations.log
2026-08-03T10:21:55-06:00 INVOKED: docker info
(lines: 1)
```

The single entry is the pre-flight `docker info` from the harness itself. The
834 tests logged **zero** engine invocations.

**PASS.** Full suite green, coverage threshold enforced, uid preserved,
networking down (`curl` exit 7), docker unreachable, no engine binary touched.

---

## Step 13 — GitHub Actions green on all three matrix legs

### NOT VERIFIABLE in this environment

Two independent reasons, both checked rather than assumed:

1. **The commit has never been pushed.** The branch does not exist on the
   remote, so there is no run to observe:

   ```
   $ git rev-parse --abbrev-ref HEAD
   feature/2--full-test-coverage-and-code-review-remediation
   $ git rev-parse HEAD
   676bee01b63de64073e8b1c440a83f3cc557b3d6
   $ git ls-remote origin 'refs/heads/feature/2*'
   (no output — branch absent from origin)
   ```

   `gh run list` confirms no run exists for this branch or this SHA. The most
   recent runs are all from before this plan's work:

   ```
   completed  success  Merge pull request #3 ...            CI  main                      push          30779401029
   completed  success  Fix/symfony php compat               CI  fix/symfony-php-compat    pull_request  30779357313
   completed  success  Add dev command and working-copy ... CI  feature/dev-workflow      pull_request  30779241224
   ...
   ```

   Every one of those predates task 018, so none of them exercised the blocking
   gates. They are not evidence for this plan.

2. **Only one PHP version is installed here.** `php -v` reports 8.4.24 and
   `/usr/bin/php8.4` is the only versioned binary present, so the 8.2 and 8.3
   legs cannot be run locally either.

Pushing was out of scope for this task (the task forbids committing), so no run
was triggered. **No CI run was observed, and none is claimed.**

### What was done instead: every workflow step run locally on PHP 8.4.24

Each `run:` line from `.github/workflows/ci.yml`, in workflow order, with its
real exit code:

```
PHP: 8.4.24  |  versioned binaries present: /usr/bin/php /usr/bin/php.default /usr/bin/php8.4

### Validate composer.json        : composer validate --strict          exit=0
### Install dependencies          : composer install --no-interaction --no-progress   exit=0
### Lint (PSR-12)                 : composer lint                       exit=0
### Static analysis (PHPStan max) : composer analyse                    exit=0
### Run PHPUnit (with coverage)   : vendor/bin/phpunit --coverage-text   exit=0
### Smoke-test the binary         : ./bin/upkeep list                   exit=0
```

All six workflow commands pass on the 8.4 leg. The workflow's step names make
the four gates legible (`Lint (PSR-12)`, `Static analysis (PHPStan max)`,
`Run PHPUnit (with coverage)`, `Smoke-test the binary`), and
`coverage: pcov` is set on `shivammathur/setup-php`, so a coverage driver will
be present on all three legs.

### Risk flagged for the unverified legs

`composer.lock` is gitignored, so CI resolves dependencies afresh on each leg —
and the resolved set differs by PHP version. Simulating a PHP 8.2 platform
against this `composer.json`:

```
  - Locking symfony/console (v7.4.15)
  - Locking symfony/http-client (v7.4.15)
  - Locking symfony/process (v7.4.13)
  - Locking symfony/yaml (v7.4.15)
  - Downgrading symfony/console (v8.1.2 => v7.4.15)
  ...
```

Local validation therefore ran against **Symfony 8.1.x**, while the 8.2 and 8.3
CI legs will run against **Symfony 7.4.x** — a combination not exercised here.
This is not a defect, but it means the 8.4 evidence above does not transfer to
the other two legs, and step 13 remains genuinely open until a real run exists.

**NOT VERIFIABLE.**

---

## Defects found

Recorded, not fixed, per the task's constraints.

### D1 — `upkeep dev` / `ensureEnv` tells the operator to use a removed option

**`src/Adapter/DdevContribAdapter.php:813`**

```php
'No base artifacts for Drupal %s (missing %s). Run `upkeep base-artifacts:build --core=%s` first.',
```

Observed during step 9:

```
$ upkeep dev widget
 [ERROR] No base artifacts for Drupal 11 (missing .../base-artifacts/11/meta.yml).
         Run `upkeep base-artifacts:build --core=11` first.
EXIT=2
```

`--core` was renamed to `--version` by this plan (`BEHAVIOUR-CHANGES.md` item
2). The option no longer exists:

```
$ upkeep base-artifacts:build --help | grep -E '\-\-core|\-\-version'
      --version=VERSION          Drupal core major version to build artifacts for (e.g. 11)

$ upkeep base-artifacts:build --core=11
  The "--core" option does not exist.
EXIT=1
```

So an operator who follows upkeep's own remediation hint gets a usage error.
This is a user-facing regression introduced by the rename, in the one place the
rename was not propagated.

**Aggravating factor**: the stale wording is *pinned by a test* —

```
tests/Adapter/DdevContribAdapterEnvironmentTest.php:178
    self::assertStringContainsString('base-artifacts:build --core=11', $e->getMessage());
```

— so the suite actively defends the wrong message, and fixing the string will
fail that assertion until it is updated too.

**Why no gate caught it**: the line is 100% covered, PSR-12 clean, and
PHPStan-max clean. It is a string, and no tool in this plan checks that a
string names an option that exists. Worth noting as a limit of the gates rather
than a hole in them. `README.md`, `CLAUDE.md` and `docs/` are all clean of
`--core=` (checked), so this is the only surviving instance.

*(`--core-versions` on `modules:add` is a different, still-extant option and is
not affected.)*

---

## Restoration and final state

Every deliberate breakage and manipulation was reverted.

| Step | What was changed | Reverted |
| --- | --- | --- |
| 3 | `tests/Security/SecretRedactorTest.php` moved out of the tree | Moved back; suite re-run green |
| 9 | Throwaway cockpit + fake environment created under `$HOME` | `rm -rf`'d |
| 10 | `UPKEEP_GITLAB_TOKEN` sentinel; `UPKEEP_COCKPIT`; `PATH` shims for `ddev`/`composer` | Shims confined to a scratch dir and never on the repo's `PATH`; env vars unset |
| 10 | Results/scratch state written under the throwaway cockpit | Removed with the cockpit |
| 12 | `PATH` shims for `docker`/`ddev`/`docker-compose`/`colima`, `DOCKER_HOST` | Confined to the `unshare` subshell only |
| 13 | `config.platform.php` temporarily set in the *scratch clone's* `composer.json` | `git checkout composer.json` in the scratch clone; the working tree was never touched |

```
$ ls -d $HOME/upkeep-validation*
(all removed)

$ ls -ld $HOME/.upkeep $HOME/.config/upkeep
ls: cannot access '/home/owen.guest/.upkeep': No such file or directory
ls: cannot access '/home/owen.guest/.config/upkeep': No such file or directory
                       # never existed, never created — no real cockpit touched

$ echo "UPKEEP_GITLAB_TOKEN=[${UPKEEP_GITLAB_TOKEN-<unset>}] UPKEEP_COCKPIT=[${UPKEEP_COCKPIT-<unset>}]"
UPKEEP_GITLAB_TOKEN=[<unset>] UPKEEP_COCKPIT=[<unset>]

$ git status --porcelain
(no output)

$ git stash list
(empty)

$ vendor/bin/phpunit
OK (834 tests, 2353 assertions)
PHPUNIT_EXIT=0
```

`git status --porcelain` is empty apart from this report file, which is added
after this run. The suite is green with the coverage threshold enforced.

**One side effect worth naming**: during step 10 a `prune --trees --yes` ran
against the throwaway cockpit *before* the `ddev` shim was installed, so it
invoked the machine's real `ddev`/`docker` to delete engine project
`upkeep-widget-d11` (which did not exist) and pulled the
`ddev/ddev-utilities:latest` image in the process. No pre-existing project,
volume or container was affected; the only residue is that one cached docker
image.

---

## Success criteria, against the evidence above

| # | Criterion | Status |
| --- | --- | --- |
| 1 | 100% line coverage, enforced | **Met** — step 2 (100.00%, 4035/4035) and step 3 (gate fails at 99.95%) |
| 2 | PHPStan max, zero errors, no baseline | **Met** — step 4 |
| 3 | phpcs PSR-12 clean over `src/`, `bin/`, `tests/` | **Met** — step 5 |
| 4 | CI passes on 8.2, 8.3, 8.4 with all four gates | **Not verified here** — step 13; all commands pass on 8.4 |
| 5 | Every review finding recorded with its resolution | **Met** — `review-findings.md` (55 findings), out of this task's scope to re-audit |
| 6 | Suite runs offline, no docker, no token, in seconds | **Met** — step 12 (4.0s, zero engine invocations) |
| 7 | Adapter-boundary grep silent | **Met** — step 6, case-sensitive and case-insensitive |
| 8 | CLI-level tests assert 0/1/2 and both resolution precedences | **Met** — step 9 from the shell; `ExitCodeContractTest`, `ResolutionPrecedenceTest` in-suite |
| 9 | Every suppression justified; total count reported | **Met** — step 7; total is **0** |
