<?php

declare(strict_types=1);

namespace Upkeep\Tests\Integration;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Security\SecretRedactor;

/**
 * Tests that cross the container boundary.
 *
 * Everything else in this repository is fast, offline and holds src/ to 100%
 * line coverage. These are the opposite by necessity: they need docker and
 * ddev, they take minutes, and they exist because the unit suite is
 * structurally unable to see the class of failure that has cost the most.
 *
 * Three bugs shipped past a fully green suite in two days, all of them at this
 * boundary and none of them visible to a test that only inspects a string:
 *
 *   1. `MODULE: unbound variable` — a multi-line script, then a variable of
 *      our own. `ddev exec` re-joins its arguments and hands the result to a
 *      shell that expands the string before the container's shell runs it, so
 *      neither survives.
 *   2. `Referenced sniff "./vendor/drupal/coder/…" does not exist` — a `cd`
 *      into the module copied from CI, where the module repo root is where
 *      composer put vendor/. Under ddev-drupal-contrib it is not.
 *   3. A module's own `require-dev` never installed, because composer does not
 *      install a path dependency's dev requirements.
 *
 * Every one of those is a fact about an environment, not about a string. So
 * these run the real commands, in a real container, and read what comes back.
 *
 * They are skipped rather than failed when there is no project to run against,
 * so a contributor without docker still gets the four gates.
 */
abstract class IntegrationTestCase extends TestCase
{
    /** Set by the workflow (or by hand) to a started ddev project. */
    public const PROJECT_ENV = 'UPKEEP_DDEV_PROJECT';

    /**
     * The module path exactly as Adapter\DdevContribAdapter builds it.
     *
     * Duplicated deliberately: if the adapter's own helper changed shape, a
     * test that borrowed it would follow the change and still pass. This is
     * the string the container has to understand.
     */
    protected const MODULE_PATH = '"$DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH"/\'widget\'';

    protected string $project;

    protected function setUp(): void
    {
        $project = getenv(self::PROJECT_ENV);
        if (!\is_string($project) || $project === '' || !is_dir($project)) {
            self::markTestSkipped(sprintf(
                'No ddev project to run against. Set %s to a started project '
                . '(tests/Integration/fixture/setup.sh builds one).',
                self::PROJECT_ENV,
            ));
        }

        $this->project = $project;
    }

    /**
     * Runs a command in the fixture project, as the adapter would.
     *
     * The real ProcessRunner, not a scripted one — the whole point is that
     * nothing here is simulated.
     *
     * @param list<string> $command
     *
     * @return array{int, string} exit status and combined output
     */
    protected function execute(array $command, int $timeout = 300): array
    {
        $captured = (new ProcessRunner(static function (): void {
        }, new SecretRedactor()))->capture($command, $this->project, $timeout);

        return [$captured->exitCode ?? -1, $captured->output];
    }

    /**
     * Runs a command line inside the container, through `ddev exec` — the
     * exact path the adapter uses, including whatever ddev does to the
     * arguments on the way.
     *
     * @return array{int, string}
     */
    protected function inContainer(string $commandLine, int $timeout = 300): array
    {
        return $this->execute(['ddev', 'exec', 'bash', '-c', $commandLine], $timeout);
    }
}
