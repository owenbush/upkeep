<?php

declare(strict_types=1);

namespace Upkeep\Tests\Integration;

use Upkeep\Adapter\CheckScript;

/**
 * What `ddev exec` does to a command, and what the site layout does to a
 * ruleset.
 *
 * These two facts broke the phpstan and phpcs checks twice in two days, and
 * the unit suite could not have seen either: it inspects the strings these
 * build and never executes them. Here they are executed.
 */
final class DdevExecContractTest extends IntegrationTestCase
{
    /**
     * The failure that shipped twice, asserted directly.
     *
     * `ddev exec` re-joins its arguments and hands the result to a shell that
     * expands the string before the container's shell runs it, so a variable
     * defined and used in the same command is expanded away while unset:
     * `ROOT=$(pwd) && … "$ROOT/x"` dies with `ROOT: unbound variable`, as an
     * earlier `MODULE=…` did. Nothing upkeep sends may rely on one.
     */
    public function testNoGeneratedCommandDiesOnAnUnboundVariable(): void
    {
        foreach (self::everyCommand() as $label => $commandLine) {
            [, $output] = $this->inContainer($commandLine);

            self::assertStringNotContainsString(
                'unbound variable',
                $output,
                $label . ' relies on a shell variable ddev exec expands away',
            );
        }
    }

    /**
     * @return array<string, string>
     */
    private static function everyCommand(): array
    {
        return [
            'phpstan probe' => CheckScript::configProbe(self::MODULE_PATH, 'phpstan.neon'),
            'phpcs probe' => CheckScript::configProbe(self::MODULE_PATH, 'phpcs.xml.dist'),
            'phpstan with the module config' => CheckScript::phpStan(self::MODULE_PATH, 'phpstan.neon'),
            'phpcs with the module ruleset' => CheckScript::phpCs(self::MODULE_PATH, 'phpcs.xml.dist'),
            'phpstan falling back' => CheckScript::phpStan(self::MODULE_PATH, null),
            'phpcs falling back' => CheckScript::phpCs(self::MODULE_PATH, null),
        ];
    }

    /**
     * The probe answers about the container's real filesystem.
     *
     * Its exit status is what decides which configuration the check is given,
     * so a probe that cannot see the module would silently send every module
     * down the fallback path.
     */
    public function testTheProbeSeesAConfigThatIsThereAndNotOneThatIsNot(): void
    {
        [$present] = $this->inContainer(CheckScript::configProbe(self::MODULE_PATH, 'phpcs.xml.dist'));
        self::assertSame(0, $present, 'the fixture module ships a phpcs.xml.dist');

        [$absent] = $this->inContainer(CheckScript::configProbe(self::MODULE_PATH, '.phpcs.xml'));
        self::assertNotSame(0, $absent, 'and does not ship a .phpcs.xml');
    }

    /**
     * The second failure: a ruleset referencing `./vendor/…`.
     *
     * CI runs phpcs from inside the module because in CI the module repo root
     * is where composer install put vendor/. Under ddev-drupal-contrib the
     * module is a checkout inside a site whose vendor/ is at the project root,
     * so a `cd` into the module breaks every vendor-relative reference —
     * observed as `Referenced sniff "./vendor/drupal/coder/coder_sniffer/
     * Drupal" does not exist` on a real module.
     *
     * The fixture's ruleset uses that exact long-path form, so this fails if
     * anything ever moves the working directory again.
     */
    public function testAVendorRelativeSniffPathResolvesFromWhereTheCheckRuns(): void
    {
        [, $output] = $this->inContainer(CheckScript::phpCs(self::MODULE_PATH, 'phpcs.xml.dist'));

        self::assertStringNotContainsString('Referenced sniff', $output);
        self::assertStringNotContainsString('No sniffs were registered', $output);
    }

    /**
     * And the module's own ruleset is the one in force, not a default that
     * happened to be lying in the working directory.
     *
     * The fixture carries a file that only the Drupal standard objects to, so
     * naming the sniff in the output is proof the module's ruleset was loaded
     * and applied.
     */
    public function testTheModulesOwnRulesetIsWhatActuallyRuns(): void
    {
        [$status, $output] = $this->inContainer(CheckScript::phpCs(self::MODULE_PATH, 'phpcs.xml.dist'));

        self::assertNotSame(0, $status, 'the fixture has a deliberate violation');
        self::assertStringContainsString('Drupal.', $output, 'a sniff only the module\'s ruleset enables');
    }

    /**
     * PHPStan runs, and against the module's own configuration.
     *
     * The fixture's phpstan.neon sets a level the file passes at, so a clean
     * exit is evidence the config was read — a default would have analysed
     * something else, or nothing.
     */
    public function testPhpStanRunsAgainstTheModulesOwnConfiguration(): void
    {
        [$status, $output] = $this->inContainer(CheckScript::phpStan(self::MODULE_PATH, 'phpstan.neon'));

        self::assertSame(0, $status, $output);
        self::assertStringNotContainsString('not found', $output);
    }

    /**
     * The fallback chain survives the trip too.
     *
     * The fixture already has a `phpstan.neon` at the project root, so the
     * guarded download is skipped and nothing here goes to the network — what
     * is being tested is that the braces, the `&&` chain and the `sed` all
     * arrive intact and the analysis still runs.
     */
    public function testTheFallbackChainExecutesWithoutFetchingAnything(): void
    {
        [$status, $output] = $this->inContainer(CheckScript::phpStan(self::MODULE_PATH, null));

        self::assertSame(0, $status, $output);
        self::assertStringNotContainsString('curl', $output, 'the root config was already there');
    }

    /**
     * Nothing a check does may leave a file in the module.
     *
     * That directory is a git checkout, and an untracked file in it makes the
     * next applyPatch or startWork refuse on a dirty working copy — a config
     * fallback turning the tool off.
     */
    public function testNoCheckWritesIntoTheModuleDirectory(): void
    {
        $before = $this->inContainer('ls -A ' . self::MODULE_PATH)[1];

        $this->inContainer(CheckScript::phpStan(self::MODULE_PATH, null));
        $this->inContainer(CheckScript::phpCs(self::MODULE_PATH, null));

        self::assertSame($before, $this->inContainer('ls -A ' . self::MODULE_PATH)[1]);
    }
}
