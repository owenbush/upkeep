<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Cockpit\ModuleRegistry;
use Upkeep\Command\ModulesAddCommand;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Workflow\ExitCode;

final class ModulesAddCommandTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-cockpit-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o755, true);
        file_put_contents($this->cockpit . '/registry.yml', <<<YAML
        modules:
          conditions_helper:
            project: project/conditions_helper
            core_versions: ["11"]
        YAML);
    }

    protected function tearDown(): void
    {
        @unlink($this->cockpit . '/registry.yml');
        @rmdir($this->cockpit);
    }

    /** Membership across two pages; one sandbox project and one already registered. */
    private function client(): GitlabClient
    {
        $pages = [
            new MockResponse(json_encode([
                [
                    'id' => 1,
                    'path' => 'conditions_helper',
                    'path_with_namespace' => 'project/conditions_helper',
                    'name' => 'Conditions Helper',
                    'web_url' => 'https://git.drupalcode.org/project/conditions_helper',
                ],
                [
                    'id' => 2,
                    'path' => 'token_or',
                    'path_with_namespace' => 'project/token_or',
                    'name' => 'Token OR',
                    'web_url' => 'https://git.drupalcode.org/project/token_or',
                ],
                [
                    'id' => 3,
                    'path' => 'playground',
                    'path_with_namespace' => 'sandbox/playground',
                    'name' => 'Playground',
                    'web_url' => 'https://git.drupalcode.org/sandbox/playground',
                ],
                [
                    'id' => 4,
                    'path' => 'field_helper',
                    'path_with_namespace' => 'project/field_helper',
                    'name' => 'Field Helper',
                    'web_url' => 'https://git.drupalcode.org/project/field_helper',
                ],
            ], \JSON_THROW_ON_ERROR)),
            new MockResponse(json_encode([], \JSON_THROW_ON_ERROR)),
        ];

        return new GitlabClient(new MockHttpClient(function () use (&$pages) {
            return array_shift($pages);
        }), 'token');
    }

    public function testInteractiveMultiSelectRegistersOnlyChosenProjects(): void
    {
        $tester = new CommandTester(new ModulesAddCommand($this->client()));
        // Choice list must exclude the already-registered module and the
        // sandbox namespace, leaving token_or and field_helper.
        $tester->setInputs(['token_or']);
        $exit = $tester->execute(['--cockpit' => $this->cockpit, '--core-versions' => '10,11']);

        self::assertSame(0, $exit, $tester->getDisplay());
        $display = $tester->getDisplay();
        self::assertStringNotContainsString(
            'conditions_helper',
            $tester->getDisplay(true) ? substr($display, (int) strpos($display, '?')) : $display,
        );
        self::assertStringNotContainsString('playground', $display);

        $modules = ModuleRegistry::fromFile($this->cockpit . '/registry.yml')->modules();
        self::assertSame(['conditions_helper', 'token_or'], array_keys($modules));
        self::assertSame('project/token_or', $modules['token_or']->project);
        self::assertSame(['10', '11'], $modules['token_or']->coreVersions);
    }

    public function testExplicitModuleArgumentsRegisterWithoutPrompting(): void
    {
        $tester = new CommandTester(new ModulesAddCommand($this->client()));
        $exit = $tester->execute([
            'modules' => ['token_or', 'field_helper'],
            '--cockpit' => $this->cockpit,
        ], ['interactive' => false]);

        self::assertSame(0, $exit, $tester->getDisplay());
        $modules = ModuleRegistry::fromFile($this->cockpit . '/registry.yml')->modules();
        self::assertSame(['conditions_helper', 'token_or', 'field_helper'], array_keys($modules));
        self::assertSame(['11'], $modules['token_or']->coreVersions, 'default --core-versions is 11');
    }

    public function testUnknownExplicitModuleFailsWithoutWriting(): void
    {
        $before = file_get_contents($this->cockpit . '/registry.yml');
        $tester = new CommandTester(new ModulesAddCommand($this->client()));
        $exit = $tester->execute([
            'modules' => ['not_one_of_mine'],
            '--cockpit' => $this->cockpit,
        ], ['interactive' => false]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('not_one_of_mine', $tester->getDisplay());
        self::assertSame($before, file_get_contents($this->cockpit . '/registry.yml'));
    }

    public function testNonInteractiveWithoutArgumentsExplainsAndFails(): void
    {
        $tester = new CommandTester(new ModulesAddCommand($this->client()));
        $exit = $tester->execute(['--cockpit' => $this->cockpit], ['interactive' => false]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('non-interactive', $tester->getDisplay());
    }

    public function testEverythingAlreadyRegisteredIsACleanNoOp(): void
    {
        file_put_contents($this->cockpit . '/registry.yml', <<<YAML
        modules:
          conditions_helper:
            project: project/conditions_helper
            core_versions: ["11"]
          token_or:
            project: project/token_or
            core_versions: ["11"]
          field_helper:
            project: project/field_helper
            core_versions: ["11"]
        YAML);

        $tester = new CommandTester(new ModulesAddCommand($this->client()));
        $exit = $tester->execute(['--cockpit' => $this->cockpit]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('already registered', $tester->getDisplay());
    }

    /**
     * Naming a module that is already registered is a no-op, not an error and
     * not a duplicate entry: it is a membership of yours, so it is not
     * "unknown", but there is nothing left to add. The registry comes out
     * byte-identical and the run says so — re-running the same command is
     * safe, which is the property a maintainer actually relies on.
     */
    public function testNamingAnAlreadyRegisteredModuleLeavesTheRegistryUntouched(): void
    {
        $before = file_get_contents($this->cockpit . '/registry.yml');

        $tester = new CommandTester(new ModulesAddCommand($this->client()));
        $exit = $tester->execute([
            'modules' => ['conditions_helper'],
            '--cockpit' => $this->cockpit,
        ], ['interactive' => false]);

        self::assertSame(0, $exit, $tester->getDisplay());
        self::assertStringContainsString('Nothing selected; registry unchanged.', $tester->getDisplay());
        self::assertSame($before, file_get_contents($this->cockpit . '/registry.yml'));
    }

    /**
     * A registry entry with no core versions could never be checked against
     * anything, so an empty `--core-versions` is refused before it is written
     * rather than producing entries nothing can act on.
     */
    public function testAnEmptyCoreVersionsListIsRefusedBeforeAnythingIsWritten(): void
    {
        $before = file_get_contents($this->cockpit . '/registry.yml');

        $tester = new CommandTester(new ModulesAddCommand($this->client()));
        $exit = $tester->execute([
            'modules' => ['token_or'],
            '--cockpit' => $this->cockpit,
            '--core-versions' => ' , ',
        ], ['interactive' => false]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('at least one core major', $tester->getDisplay());
        self::assertSame($before, file_get_contents($this->cockpit . '/registry.yml'));
    }

    /**
     * Regression: the no-token branch referenced two constants that do not
     * exist on TokenResolver, so first-run without a token died with a fatal
     * Error instead of printing guidance.
     */
    public function testMissingTokenExplainsTheSourcesInsteadOfFatallyErroring(): void
    {
        $tester = new CommandTester(new ModulesAddCommand(
            null,
            new TokenResolver('UPKEEP_TEST_ABSENT_TOKEN', '/nonexistent/upkeep/drupal-pat'),
        ));
        $exit = $tester->execute(['--cockpit' => $this->cockpit], ['interactive' => false]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        $display = $tester->getDisplay();
        self::assertStringContainsString('No GitLab token found', $display);
        self::assertStringContainsString('UPKEEP_TEST_ABSENT_TOKEN', $display);
        self::assertStringContainsString('/nonexistent/upkeep/drupal-pat', $display);
    }

    /**
     * Regression: ApiFailure exposes a public promoted $message property, not
     * a message() method; calling it turned any GitLab failure into a fatal.
     */
    public function testApiFailureIsReportedInsteadOfFatallyErroring(): void
    {
        $client = new GitlabClient(
            new MockHttpClient(new MockResponse('{"message":"Internal Server Error"}', ['http_code' => 500])),
            'token',
        );

        $tester = new CommandTester(new ModulesAddCommand($client));
        $exit = $tester->execute(['--cockpit' => $this->cockpit], ['interactive' => false]);

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('Could not list your project memberships', $tester->getDisplay());
    }
}
