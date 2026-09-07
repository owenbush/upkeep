<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Command\AbstractMrCommand;
use Upkeep\Adapter\Environment;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Tests\Support\ReadOnlyMrFixtureCommand;
use Upkeep\Tests\Support\StubEngineAdapterFactory;
use Upkeep\Tests\Support\WritingMrFixtureCommand;
use Upkeep\Workflow\ExitCode;

/**
 * Which merge-request commands need a credential, and which only look.
 *
 * drupalcode serves a public project's merge requests, refs, forks and raw
 * files anonymously, so requiring a token to *read* was a restriction upkeep
 * imposed rather than one GitLab does. `check` and `review` now say so and
 * carry on; anything that writes still refuses up front with the token
 * guidance rather than discovering the problem four frames down.
 *
 * Tested through a fixture command rather than the real ones, for a reason
 * worth stating: a real read-only command that no longer short-circuits goes
 * on to *make the request*, which would put a live call to git.drupalcode.org
 * in the offline suite. Both paths are exercised here up to the point the
 * client is built and no further — an invalid merge-request number is refused
 * immediately after, so nothing reaches the network.
 */
final class MrCredentialSeamTest extends TestCase
{
    private string $cockpit;

    protected function setUp(): void
    {
        $this->cockpit = sys_get_temp_dir() . '/upkeep-mr-seam-' . bin2hex(random_bytes(4));
        mkdir($this->cockpit, 0o755, true);
        file_put_contents(
            $this->cockpit . '/registry.yml',
            "modules:\n  widget:\n    project: project/widget\n    core_versions: [\"11\"]\n",
        );
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cockpit));
    }

    /** No engine is reached: the seam under test is earlier than that. */
    private static function engines(): StubEngineAdapterFactory
    {
        return new StubEngineAdapterFactory(FakeEngineAdapter::withEnvironment(new Environment(
            'widget',
            '11',
            'upkeep-widget-d11',
            sys_get_temp_dir() . '/upkeep-mr-seam-unused',
            'https://localhost',
            true,
        )));
    }

    private function execute(AbstractMrCommand $command): CommandTester
    {
        $tester = new CommandTester($command);
        // `0` is not a merge request, and UpkeepCommand::mrIid() refuses it —
        // *after* the client has been built, which is the seam under test and
        // the last thing that happens before anything would be fetched.
        $tester->execute(['module' => 'widget', 'mr' => '0', '--cockpit' => $this->cockpit]);

        return $tester;
    }

    /**
     * A command that only reads gets a client without a credential, and the
     * degraded mode is stated once rather than assumed.
     */
    public function testAReadOnlyCommandIsHandedAnAnonymousClientAndSaysSo(): void
    {
        $tester = $this->execute(new ReadOnlyMrFixtureCommand(self::engines()));

        self::assertSame(ExitCode::INFRASTRUCTURE, $tester->getStatusCode(), 'refused on the iid, not the token');
        self::assertStringContainsString('anonymously', $tester->getDisplay());
        self::assertStringNotContainsString('No GitLab token found', $tester->getDisplay());
    }

    /**
     * A command that writes refuses before it starts.
     *
     * It could technically proceed — the client refuses writes structurally —
     * but discovering "no credential" after provisioning an environment or
     * halfway through a merge prompt is a worse answer than saying it first.
     */
    public function testAWritingCommandStillRefusesWithoutACredential(): void
    {
        $tester = $this->execute(new WritingMrFixtureCommand(self::engines()));

        self::assertSame(ExitCode::INFRASTRUCTURE, $tester->getStatusCode());
        self::assertStringContainsString('No GitLab token found', $tester->getDisplay());
    }

    /**
     * The default is strict, so a command that writes and forgets to declare
     * itself gets the safe answer. The other way round would hand a write an
     * anonymous client.
     */
    public function testTheDefaultIsToRequireACredential(): void
    {
        self::assertFalse((new WritingMrFixtureCommand(self::engines()))->declaresReadsOnly());
    }
}
