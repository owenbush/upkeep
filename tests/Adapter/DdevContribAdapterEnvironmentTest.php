<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\EngineAddOn;
use Upkeep\Adapter\EnvironmentMeta;

/**
 * ensureEnv() and teardown(): the provision / reuse / re-provision decision,
 * and the guards that stand between a stale environment and a developer's
 * unpushed work.
 */
final class DdevContribAdapterEnvironmentTest extends DdevAdapterTestCase
{
    public function testProvisioningSeedsWiresAndMarksTheEnvironmentComplete(): void
    {
        $runner = $this->engine();

        $environment = $this->adapter($runner)->ensureEnv(self::module(), self::CORE);

        self::assertSame(self::projectName(), $environment->projectName);
        self::assertSame($this->projectPath(), $environment->projectPath);
        self::assertSame(self::PRIMARY_URL, $environment->primaryUrl);
        self::assertFalse($environment->reused);

        // The add-on is installed at the pinned version, and re-adapted after,
        // because `add-on get` may clobber the config file.
        self::assertTrue($runner->issued(
            'ddev add-on get ' . EngineAddOn::NAME . ' --version ' . EngineAddOn::VERSION,
        ));
        self::assertStringContainsString(
            'DRUPAL_PROJECTS_PATH=' . EngineAddOn::PROJECTS_PATH,
            (string) file_get_contents($this->projectPath() . '/.ddev/' . EngineAddOn::CONFIG_FILENAME),
        );

        // The module is wired by path repository, pinned to the branch the
        // working copy actually has checked out.
        self::assertStringContainsString(
            '"url": "./module"',
            (string) file_get_contents($this->projectPath() . '/composer.json'),
        );
        self::assertTrue($runner->issued('composer require drupal/widget:1.0.x-dev'));

        // The completion marker is written last, and records what the
        // environment was provisioned for and from.
        $meta = EnvironmentMeta::fromYaml(
            (string) file_get_contents($this->projectPath() . '/' . EnvironmentMeta::FILENAME),
        );
        self::assertSame(self::MODULE, $meta->moduleName);
        self::assertSame(self::SEED_CORE_VERSION, $meta->seedCoreVersion);
        self::assertSame(EngineAddOn::VERSION, $meta->addOnVersion);
    }

    public function testProvisioningRefusesAnEnvironmentReportingTheWrongCoreMajor(): void
    {
        $runner = $this->engine(['drush status --field=drupal-version' => "10.3.1\n"]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('reports Drupal "10.3.1", expected major 11');

        $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
    }

    public function testProvisioningTearsDownThePartialEnvironmentAndRethrows(): void
    {
        $runner = $this->engine(['drush status --field=drupal-version' => "10.3.1\n"]);

        try {
            $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
            self::fail('Expected the core-major mismatch to abort provisioning.');
        } catch (AdapterException) {
            // The partial tree must not survive: the next ensure_env would
            // otherwise find a directory with no completion marker.
            self::assertDirectoryDoesNotExist($this->projectPath());
            self::assertTrue($runner->issued('ddev delete --omit-snapshot --yes ' . self::projectName()));
            self::assertTrue($this->loggedContaining('Provisioning failed'));
        }
    }

    /**
     * A cleanup that itself fails must not replace the real diagnosis: the
     * original failure is what the operator needs to see.
     */
    public function testAFailingCleanupIsLoggedButTheOriginalFailureIsRaised(): void
    {
        $runner = $this->engine([
            'drush status --field=drupal-version' => "10.3.1\n",
            'rm -rf' => null,
        ]);

        try {
            $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
            self::fail('Expected the core-major mismatch to abort provisioning.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('reports Drupal "10.3.1"', $e->getMessage());
            self::assertTrue($this->loggedContaining('Cleanup after failed provisioning also failed'));
        }
    }

    public function testProvisioningRefusesWhenTheEngineDoesNotReportTheProjectAfterwards(): void
    {
        $runner = $this->engine(['ddev describe' => null]);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('Engine does not report project ' . self::projectName());

        $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
    }

    public function testProvisioningRefusesWhenComposerMirroredTheModuleInsteadOfSymlinkingIt(): void
    {
        // The composer require "succeeds" but leaves no symlink — exactly what
        // a mirrored (copied) package install looks like on disk.
        $runner = $this->engine(['composer require drupal/widget' => '']);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('Module wiring violated the ownership constraint');

        $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
    }

    public function testProvisioningRefusesWhenTheSeededTreeHasNoComposerJson(): void
    {
        $runner = $this->engine(['cp -a' => '']);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('Cannot read the project composer.json');

        $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
    }

    public function testProvisioningRefusesWhenTheAddOnInstalledNoConfig(): void
    {
        $runner = $this->engine(['add-on get ' . EngineAddOn::NAME => '']);

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('Cannot read the engine add-on config');

        $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
    }

    public function testProvisioningRefusesWhenTheProjectsRootCannotBeCreated(): void
    {
        // A regular file where the projects root should be: it is not a
        // directory, and it cannot be made into one.
        $blocked = $this->root . '/blocked';
        file_put_contents($blocked, 'not a directory');

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('Could not create projects root');

        $this->adapter($this->engine(), $blocked)->ensureEnv(self::module(), self::CORE);
    }

    public function testProvisioningCreatesTheProjectsRootWhenItIsAbsent(): void
    {
        $fresh = $this->root . '/fresh-projects';

        $environment = $this->adapter($this->engine(), $fresh)->ensureEnv(self::module(), self::CORE);

        self::assertSame($fresh . '/' . self::projectName(), $environment->projectPath);
        self::assertDirectoryExists($fresh);
    }

    public function testMissingBaseArtifactsAreRefusedBeforeAnythingIsProvisioned(): void
    {
        $runner = $this->engine();
        unlink($this->artifactsDir . '/' . self::CORE . '/meta.yml');

        try {
            $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
            self::fail('Expected missing base artifacts to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('No base artifacts for Drupal 11', $e->getMessage());
            self::assertStringContainsString('base-artifacts:build --core=11', $e->getMessage());
            self::assertSame([], $runner->invocations);
        }
    }

    public function testUnreadableBaseArtifactMetaIsRefusedWithItsCause(): void
    {
        file_put_contents($this->artifactsDir . '/' . self::CORE . '/meta.yml', "core_version: 11.1.0\n");

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('Base artifact meta for Drupal 11 is unreadable');

        $this->adapter($this->engine())->ensureEnv(self::module(), self::CORE);
    }

    public function testAHealthyEnvironmentIsReusedAndStampedAsUsed(): void
    {
        $this->writeEnvironmentMeta();
        $runner = $this->engine();

        $environment = $this->adapter($runner)->ensureEnv(self::module(), self::CORE);

        self::assertTrue($environment->reused);
        self::assertSame(self::PRIMARY_URL, $environment->primaryUrl);
        self::assertFalse($runner->issued('ddev config'), 'A reused environment must not be re-provisioned.');

        // last_used_at is what `prune --older-than` filters on; without the
        // stamp, age is time-since-creation and prune deletes live work.
        $meta = EnvironmentMeta::fromYaml(
            (string) file_get_contents($this->projectPath() . '/' . EnvironmentMeta::FILENAME),
        );
        self::assertNotNull($meta->lastUsedAt);
    }

    public function testAStoppedEnvironmentIsStartedBeforeReuse(): void
    {
        $this->writeEnvironmentMeta();
        $started = false;

        // Once `ddev start` has run, describe reports it running again.
        $runner = new ScriptedCommandRunner(
            function (array $command) use (&$started): string {
                $line = implode(' ', $command);
                if (str_contains($line, 'ddev start')) {
                    $started = true;

                    return '';
                }
                if (str_contains($line, 'ddev describe')) {
                    return self::describeJson($started ? 'running' : 'stopped');
                }

                return '';
            },
        );

        $environment = $this->adapter($runner)->ensureEnv(self::module(), self::CORE);

        self::assertTrue($environment->reused);
        self::assertTrue($started);
        self::assertTrue($this->loggedContaining('is stopped — starting it'));
    }

    public function testAnEnvironmentThatDoesNotComeBackAfterStartIsRefused(): void
    {
        $this->writeEnvironmentMeta();
        $describes = 0;
        $runner = new ScriptedCommandRunner(static function (array $command) use (&$describes): ?string {
            if (str_contains(implode(' ', $command), 'ddev describe')) {
                ++$describes;

                // Healthy for the staleness probe, stopped for the reuse read,
                // then gone once the start has been attempted.
                return $describes <= 2 ? self::describeJson('stopped') : null;
            }

            return '';
        });

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('did not come back after start');

        $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
    }

    public function testAnEnvironmentLostBetweenTheHealthCheckAndReuseIsRefused(): void
    {
        $this->writeEnvironmentMeta();
        $describes = 0;
        $runner = new ScriptedCommandRunner(static function (array $command) use (&$describes): ?string {
            if (str_contains(implode(' ', $command), 'ddev describe')) {
                ++$describes;

                return $describes === 1 ? self::describeJson('running') : null;
            }

            return '';
        });

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('Engine lost project ' . self::projectName() . ' between health check and reuse');

        $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
    }

    /**
     * The completion marker is written as the last provisioning step, so its
     * absence means an interrupted provision — never a reusable environment.
     */
    public function testAnEnvironmentWithNoCompletionMarkerIsReprovisioned(): void
    {
        mkdir($this->projectPath(), 0o700, true);
        $runner = $this->engine();

        $environment = $this->adapter($runner)->ensureEnv(self::module(), self::CORE);

        self::assertFalse($environment->reused);
        self::assertTrue($this->loggedContaining('a previous provision did not finish'));
        self::assertTrue($runner->issued('ddev config'));
    }

    public function testAnUnreadableCompletionMarkerIsReprovisioned(): void
    {
        mkdir($this->projectPath(), 0o700, true);
        file_put_contents($this->projectPath() . '/' . EnvironmentMeta::FILENAME, "- not\n- a mapping\n");

        $environment = $this->adapter($this->engine())->ensureEnv(self::module(), self::CORE);

        self::assertFalse($environment->reused);
        self::assertTrue($this->loggedContaining('Environment meta is unreadable'));
    }

    public function testAnEnvironmentTheEngineNoLongerKnowsIsReprovisioned(): void
    {
        $this->writeEnvironmentMeta();
        $seen = 0;
        $runner = new ScriptedCommandRunner(function (array $command, ?string $cwd) use (&$seen): ?string {
            if (str_contains(implode(' ', $command), 'ddev describe')) {
                ++$seen;

                // Unknown to the engine on the health probe; known again once
                // the environment has been rebuilt.
                return $seen === 1 ? null : self::describeJson('running');
            }

            return ($this->engine())->tryRun($command, $cwd);
        });

        $environment = $this->adapter($runner)->ensureEnv(self::module(), self::CORE);

        self::assertFalse($environment->reused);
        self::assertTrue($this->loggedContaining('The engine no longer reports the project'));
    }

    public function testAddOnSkewMakesAnEnvironmentStale(): void
    {
        $this->writeEnvironmentMeta(['addon_version' => '1.0.0']);

        $environment = $this->adapter($this->engine())->ensureEnv(self::module(), self::CORE);

        self::assertFalse($environment->reused);
        self::assertTrue($this->loggedContaining('Engine add-on skew'));
    }

    /**
     * The one thing re-provisioning must never do is destroy work that only
     * exists in the developer's working copy.
     */
    public function testAStaleEnvironmentWithLocalWorkIsRefusedRatherThanRebuilt(): void
    {
        $this->writeEnvironmentMeta(['addon_version' => '1.0.0']);
        mkdir($this->projectPath() . '/module', 0o700, true);

        $runner = $this->engine([
            'status --porcelain' => " M src/Widget.php\n",
            'symbolic-ref --short HEAD' => "feature/my-fix\n",
        ]);

        try {
            $this->adapter($runner)->ensureEnv(self::module(), self::CORE);
            self::fail('Expected re-provisioning to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('has local work', $e->getMessage());
            self::assertStringContainsString('Unstaged changes to tracked files', $e->getMessage());
            self::assertFalse($runner->issued('rm -rf'), 'Nothing may be deleted when local work is present.');
        }
    }

    public function testTeardownDeletesTheEngineProjectBeforeTheTree(): void
    {
        $this->writeEnvironmentMeta();
        $runner = $this->engine();

        $this->adapter($runner)->teardown(self::module(), self::CORE);

        $lines = $runner->commandLines();
        $delete = array_search('ddev delete --omit-snapshot --yes ' . self::projectName(), $lines, true);
        $remove = array_search('rm -rf ' . $this->projectPath(), $lines, true);
        self::assertIsInt($delete);
        self::assertIsInt($remove);
        self::assertLessThan($remove, $delete, 'Containers and volumes must go before the tree.');
        self::assertDirectoryDoesNotExist($this->projectPath());
    }

    public function testTeardownContinuesWithTreeRemovalWhenTheEngineDeleteFails(): void
    {
        $this->writeEnvironmentMeta();
        $runner = $this->engine(['ddev delete' => null]);

        $this->adapter($runner)->teardown(self::module(), self::CORE);

        self::assertTrue($this->loggedContaining('Engine delete reported a failure'));
        self::assertDirectoryDoesNotExist($this->projectPath());
    }

    public function testTeardownOfSomethingThatDoesNotExistIsANoOp(): void
    {
        $runner = $this->engine(['ddev describe' => null]);

        $this->adapter($runner)->teardown(self::module(), self::CORE);

        self::assertTrue($this->loggedContaining('does not exist — nothing to tear down'));
        self::assertFalse($runner->issued('ddev delete'));
    }

    public function testTeardownRefusesToDestroyLocalWork(): void
    {
        $this->writeEnvironmentMeta();
        mkdir($this->projectPath() . '/module', 0o700, true);
        $runner = $this->engine([
            'status --porcelain' => "?? notes.txt\n",
            'symbolic-ref --short HEAD' => "1.0.x\n",
        ]);

        try {
            $this->adapter($runner)->teardown(self::module(), self::CORE);
            self::fail('Expected teardown to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('Refusing to tear down', $e->getMessage());
            self::assertStringContainsString('Untracked files not in .gitignore', $e->getMessage());
            self::assertDirectoryExists($this->projectPath());
        }
    }
}
