<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\Environment;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Tests\Support\CliHarness;
use Upkeep\Adapter\PatchPromotion;
use Upkeep\Tests\Support\FakeEngineAdapter;
use Upkeep\Workflow\ExitCode;

/**
 * `patch:promote` — a patch contribution turned into a branch a merge request
 * can be opened from.
 *
 * Two things are load-bearing and both are about restraint. The commit must
 * name whoever posted the patch, because promoting moves their work into
 * history under whoever pushes it and the commit is the only durable record.
 * And nothing may leave the machine: publishing somebody else's work under
 * your own account is a step a human types.
 */
final class PatchPromoteCommandTest extends TestCase
{
    private const DIFF = "diff --git a/widget.module b/widget.module\n"
        . "--- a/widget.module\n+++ b/widget.module\n@@ -1 +1 @@\n-old\n+new\n";

    private ?CliHarness $cli = null;

    protected function tearDown(): void
    {
        $this->cli?->destroy();
    }

    private function cli(): CliHarness
    {
        if ($this->cli === null) {
            $this->cli = CliHarness::create('promote');
            $this->cli->registerModule('widget');
        }

        return $this->cli;
    }

    private static function environment(): Environment
    {
        return new Environment(
            moduleName: 'widget',
            coreMajor: '11',
            projectName: 'widget-11',
            projectPath: '/tmp/projects/widget-11',
            primaryUrl: 'https://widget-11.ddev.site',
            reused: false,
        );
    }

    /**
     * Routes the two requests promoting makes — the issue, then the account
     * behind the chosen patch's owner. `$owner` null omits the owner
     * reference; `$accountStatus` other than 200 makes the lookup fail.
     */
    private function withIssue(?int $owner = 3644742, int $accountStatus = 200): CliHarness
    {
        $file = [
            'fid' => '7000',
            'name' => '3597808-9-fix.patch',
            'url' => 'https://www.drupal.org/files/issues/3597808-9-fix.patch',
            'timestamp' => '1705400000',
        ];
        if ($owner !== null) {
            $file['owner'] = ['uri' => 'https://www.drupal.org/api-d7/user/' . $owner, 'id' => (string) $owner];
        }

        $issue = [
            'nid' => 3597808,
            'title' => 'Fix the widget on PHP 8.4',
            'url' => 'https://www.drupal.org/node/3597808',
            'field_issue_status' => '8',
            'field_project' => ['machine_name' => 'widget'],
            'field_issue_files' => [['file' => $file]],
        ];

        return $this->cli()->withDrupalOrg(new DrupalOrgClient(new MockHttpClient(
            static function (string $method, string $url) use ($issue, $accountStatus): MockResponse {
                if (str_contains($url, '/user/')) {
                    if ($accountStatus !== 200) {
                        return new MockResponse('', ['http_code' => $accountStatus]);
                    }

                    return new MockResponse(
                        json_encode([
                            'uid' => '3644742',
                            'name' => 'hebatelhayah',
                            'url' => 'https://www.drupal.org/u/hebatelhayah',
                        ], \JSON_THROW_ON_ERROR),
                        ['response_headers' => ['content-type' => 'application/json']],
                    );
                }

                return new MockResponse(
                    json_encode($issue, \JSON_THROW_ON_ERROR),
                    ['response_headers' => ['content-type' => 'application/json']],
                );
            },
        )));
    }

    private function withDownload(): CliHarness
    {
        return $this->cli()->withPatchDownloader(new MockHttpClient(
            static fn (): MockResponse => new MockResponse(self::DIFF),
        ));
    }

    // ----------------------------------------------------------- happy path

    public function testThePatchIsCommittedOntoTheIssueWorkBranchCreditedToItsAuthor(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue();
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:promote', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertCount(1, $engine->promotions);
        self::assertSame('3597808-9-fix.patch', $engine->promotions[0]['patch']);

        $message = $engine->promotions[0]['message'];
        self::assertStringStartsWith('Issue #3597808 by hebatelhayah: Fix the widget', $message);
        self::assertStringContainsString('Patch-author: hebatelhayah', $message);
        self::assertStringContainsString('not authoring it', $message);
    }

    /**
     * `--partial` turns a patch that will not apply from a dead end into the
     * start of a re-roll.
     *
     * Reported from a real run: a patch cut against a release tarball can
     * never apply to a git checkout, so "re-roll it" is advice with nowhere to
     * go. What the maintainer actually wants is the work on a branch with the
     * conflicts marked — which is what the failed apply was already doing.
     *
     * Exit 1, not 0: the patch did not apply, and a script treating this as
     * success would go on to publish half of somebody's work.
     */
    public function testPartialPromotionReportsWhatLandedAndWhatIsLeft(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $engine->partialPromotion = PatchPromotion::partial(['widget.module'], ['widget.info.yml']);
        $this->withIssue();
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:promote', 'widget', '3597808', '--version=11', '--partial');

        self::assertSame(ExitCode::FAILED, $exit, $cli->display());
        self::assertTrue($engine->promotions[0]['partial'], 'the flag reaches the adapter');

        $out = $cli->display();
        self::assertStringContainsString('did not apply cleanly', $out);
        self::assertStringContainsString('widget.module', $out);
        self::assertStringContainsString('widget.info.yml.rej', $out);
        self::assertStringContainsString('Nothing is committed', $out);
        self::assertStringContainsString('upkeep publish widget 3597808', $out);
    }

    /** Without the flag, an unappliable patch refuses exactly as before. */
    public function testPromotionIsNotPartialUnlessAskedFor(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue();
        $this->withDownload();

        $this->cli()->withEngine($engine)->run('patch:promote', 'widget', '3597808', '--version=11');

        self::assertFalse($engine->promotions[0]['partial']);
    }

    /**
     * The drupal.org convention, which is also what makes the merge request
     * `publish` opens pair with its issue by the ordinary rule.
     */
    public function testItLandsOnTheDrupalOrgWorkBranchName(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue();
        $this->withDownload();

        $this->cli()->withEngine($engine)->run('patch:promote', 'widget', '3597808', '--version=11');

        self::assertSame('3597808-fix-the-widget-on-php-8-4', $engine->promotions[0]['branch']);
    }

    public function testAnExplicitBranchIsUsedInstead(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue();
        $this->withDownload();

        $this->cli()->withEngine($engine)->run(
            'patch:promote',
            'widget',
            '3597808',
            '--version=11',
            '--branch=my-own-name',
        );

        self::assertSame('my-own-name', $engine->promotions[0]['branch']);
    }

    // --------------------------------------------------------- the restraint

    /**
     * Promoting is local. The step that puts somebody else's work on
     * drupal.org under your account is one a human types, so the command names
     * it rather than doing it.
     */
    public function testNothingIsPushedAndTheNextCommandIsNamed(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue();
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $cli->run('patch:promote', 'widget', '3597808', '--version=11');

        self::assertSame([], $engine->pushedBranches);
        self::assertStringContainsString('Nothing has been pushed', $cli->display());
        self::assertStringContainsString('upkeep publish widget 3597808', $cli->display());
    }

    // -------------------------------------------------------- attribution

    /**
     * The case that must never be silent: no author to name. The patch is
     * still somebody's work, so the promotion proceeds — but a commit that
     * simply omitted the name would read exactly like one that never had a
     * name to omit, so the operator is told.
     */
    public function testAnUnattributablePatchIsPromotedButSaidOutLoud(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue(owner: null);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:promote', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::OK, $exit, $cli->display());
        self::assertStringContainsString('no readable account', $cli->display());
        $message = $engine->promotions[0]['message'];
        self::assertStringStartsWith('Issue #3597808: Fix the widget', $message);
        self::assertStringNotContainsString('Patch-author:', $message);
        self::assertStringContainsString("patch author's work", $message);
    }

    /** An account that exists but cannot be read right now is the same story. */
    public function testAnAccountLookupFailureDoesNotInventAnAuthor(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue(accountStatus: 503);
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        self::assertSame(ExitCode::OK, $cli->run('patch:promote', 'widget', '3597808', '--version=11'));
        self::assertStringContainsString('no readable account', $cli->display());
        self::assertStringNotContainsString('Patch-author:', $engine->promotions[0]['message']);
    }

    public function testThePromoterIsRecordedWhenTheEnvironmentNamesOne(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $this->withIssue();
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine)->setEnv('UPKEEP_PROMOTER', 'owenbush');

        $cli->run('patch:promote', 'widget', '3597808', '--version=11');

        self::assertStringContainsString('Promoted-by: owenbush', $engine->promotions[0]['message']);
    }

    // ------------------------------------------------------------- failure

    /**
     * A patch that does not apply produced no branch, so it is a 2 — upkeep
     * could not do the job — and not a 1, which would claim a verdict on the
     * contribution that nothing here ran.
     */
    public function testAPatchThatDoesNotApplyExitsTwo(): void
    {
        $engine = FakeEngineAdapter::withEnvironment(self::environment());
        $engine->promoteFailure = new AdapterException('does not apply onto 2.x — needs a re-roll');
        $this->withIssue();
        $this->withDownload();
        $cli = $this->cli()->withEngine($engine);

        $exit = $cli->run('patch:promote', 'widget', '3597808', '--version=11');

        self::assertSame(ExitCode::INFRASTRUCTURE, $exit);
        self::assertStringContainsString('needs a re-roll', $cli->display());
    }
}
