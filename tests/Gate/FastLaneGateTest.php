<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gate;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Config\BotPattern;
use Upkeep\Gate\FastLaneGate;
use Upkeep\Gate\GateStatus;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Pipeline;
use Upkeep\Gitlab\PipelineStatus;
use Upkeep\Results\CachedResult;

/**
 * Full truth table for the fast-lane gate — the highest-consequence pure
 * logic in the tool. READY-AUTO must require EVERY condition; each single
 * deviation must deny it with a machine-readable reason.
 */
final class FastLaneGateTest extends TestCase
{
    private const HEAD_SHA = 'abc123def456abc123def456abc123def456abcd';

    /**
     * @param array{
     *     iid?: int,
     *     title?: string,
     *     state?: string,
     *     authorUsername?: string,
     *     authorId?: int|null,
     *     sourceBranch?: string,
     *     targetBranch?: string,
     *     draft?: bool,
     *     detailedMergeStatus?: string|null,
     *     headSha?: string|null,
     *     webUrl?: string,
     *     headPipeline?: Pipeline|null,
     * } $overrides
     */
    private function mr(array $overrides = []): MergeRequest
    {
        $defaults = [
            'iid' => 2,
            'title' => 'Automated Project Update Bot fixes',
            'state' => 'opened',
            'authorUsername' => 'Project-Update-Bot',
            'authorId' => 66574,
            'sourceBranch' => 'project-update-bot-only',
            'targetBranch' => '1.x',
            'draft' => false,
            'detailedMergeStatus' => 'mergeable',
            'headSha' => self::HEAD_SHA,
            'webUrl' => 'https://git.drupalcode.org/project/x/-/merge_requests/2',
            'headPipeline' => $this->pipeline(PipelineStatus::Success),
        ];
        $values = $overrides + $defaults;

        return new MergeRequest(...$values);
    }

    private function pipeline(PipelineStatus $status, ?string $raw = null): Pipeline
    {
        return new Pipeline(1, $status, $raw ?? $status->value, self::HEAD_SHA, 'https://example.org/pipeline/1');
    }

    /**
     * @param list<CheckResult> $checks
     */
    private function local(array $checks = [], string $sha = self::HEAD_SHA): CachedResult
    {
        if ($checks === []) {
            $checks = [new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'OK', 1.0)];
        }

        return new CachedResult($sha, new \DateTimeImmutable('2026-07-30T00:00:00+00:00'), new CheckRunResult($checks));
    }

    public function testBotMrWithGreenCiAndFreshGreenLocalChecksIsReadyAuto(): void
    {
        $verdict = (new FastLaneGate())->classify($this->mr(), '11', $this->local());

        self::assertSame(GateStatus::ReadyAuto, $verdict->status);
        self::assertSame([], $verdict->reasons);
    }

    public function testNonBotAuthorAllGreenRoutesToReviewWithNotBotAuthorReason(): void
    {
        $mr = $this->mr(['authorUsername' => 'owenbush', 'authorId' => 12345]);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['not-bot-author'], $verdict->reasons);
    }

    public function testBotAuthorFromForeignSourceBranchIsNotBotForTheGate(): void
    {
        // A push from any other branch — even under the bot account — must
        // not classify as a bot MR (BotPattern::matches contract).
        $mr = $this->mr(['sourceBranch' => 'feature/sneaky']);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['not-bot-author'], $verdict->reasons);
    }

    public function testBotMrWithRedCiIsBlockedWithCiRedReason(): void
    {
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Failed)]);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Blocked, $verdict->status);
        self::assertSame(['ci-red'], $verdict->reasons);
    }

    public function testAbsentHeadPipelineDeniesReadyAutoWithCiMissingReason(): void
    {
        $mr = $this->mr(['headPipeline' => null]);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['ci-missing'], $verdict->reasons);
    }

    public function testRunningPipelineDeniesReadyAutoWithCiNotGreenReason(): void
    {
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Running)]);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['ci-not-green:running'], $verdict->reasons);
    }

    public function testUnknownPipelineStatusDeniesReadyAutoAndSurfacesTheRawStatus(): void
    {
        // A future GitLab status string maps to PipelineStatus::Unknown; the
        // raw API value must still be visible in the reason.
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Unknown, 'some_future_status')]);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['ci-not-green:some_future_status'], $verdict->reasons);
    }

    public function testCanceledPipelineIsNotGreenNotBlocked(): void
    {
        // Only an actual Failed pipeline blocks; canceled/skipped are
        // non-green unknowns that route to review.
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Canceled)]);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['ci-not-green:canceled'], $verdict->reasons);
    }

    public function testDraftFlagDeniesReadyAutoWithDraftReason(): void
    {
        $mr = $this->mr(['draft' => true, 'title' => 'Draft: Automated Project Update Bot fixes']);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['draft'], $verdict->reasons);
    }

    public function testDetailedMergeStatusDraftStatusDeniesReadyAutoEvenWithoutDraftFlag(): void
    {
        $mr = $this->mr(['draft' => false, 'detailedMergeStatus' => 'draft_status']);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['draft'], $verdict->reasons);
    }

    public function testDraftFlagAndDraftStatusTogetherYieldOneDraftReason(): void
    {
        $mr = $this->mr(['draft' => true, 'detailedMergeStatus' => 'draft_status']);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(['draft'], $verdict->reasons);
    }

    public function testAbsentLocalResultsDenyReadyAutoWithLocalMissingReason(): void
    {
        $verdict = (new FastLaneGate())->classify($this->mr(), '11', null);

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-missing'], $verdict->reasons);
    }

    public function testLocalResultForAnOlderHeadShaIsStaleAndDeniesReadyAuto(): void
    {
        $stale = $this->local(sha: 'older-sha-0000000000000000000000000000000');

        $verdict = (new FastLaneGate())->classify($this->mr(), '11', $stale);

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-stale'], $verdict->reasons);
    }

    public function testUnknownMrHeadShaMakesAnyLocalResultUnverifiableHenceStale(): void
    {
        // Freshness cannot be confirmed without the MR's current head SHA;
        // the conservative gate treats unverifiable evidence as stale.
        $mr = $this->mr(['headSha' => null]);

        $verdict = (new FastLaneGate())->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-stale'], $verdict->reasons);
    }

    public function testFailedLocalCheckRoutesToReviewNamingTheFailingCheck(): void
    {
        $local = $this->local([
            new CheckResult(CheckType::PhpStan, CheckStatus::Passed, 0, 'OK', 1.0),
            new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 2, 'FAILURES!', 3.0),
        ]);

        $verdict = (new FastLaneGate())->classify($this->mr(), '11', $local);

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-failed:phpunit'], $verdict->reasons);
    }

    public function testEveryFailingLocalCheckIsNamedNotJustTheFirst(): void
    {
        $local = $this->local([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 2, 'FAILURES!', 3.0),
            new CheckResult(CheckType::PhpStan, CheckStatus::Failed, 1, 'errors', 2.0),
        ]);

        $verdict = (new FastLaneGate())->classify($this->mr(), '11', $local);

        self::assertSame(['local-failed:phpunit', 'local-failed:phpstan'], $verdict->reasons);
    }

    public function testNoTestsAndUnavailableChecksAreVisibleNonFailuresAndDoNotDeny(): void
    {
        // CheckStatus semantics: only Failed blocks; NoTests/Unavailable are
        // honest non-failures and must not disqualify a bot MR.
        $local = $this->local([
            new CheckResult(CheckType::PhpUnit, CheckStatus::NoTests, 0, 'No tests executed', 1.0),
            new CheckResult(CheckType::Deprecation, CheckStatus::Unavailable, null, 'not provided for this core', 0.0),
        ]);

        $verdict = (new FastLaneGate())->classify($this->mr(), '11', $local);

        self::assertSame(GateStatus::ReadyAuto, $verdict->status);
        self::assertSame([], $verdict->reasons);
    }

    public function testRedCiWithFailedLocalCheckIsBlockedAndCarriesAllReasons(): void
    {
        // BLOCKED (ci-red) takes precedence over REVIEW, and every deny
        // reason stays visible.
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Failed)]);
        $local = $this->local([new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 2, 'FAILURES!', 3.0)]);

        $verdict = (new FastLaneGate())->classify($mr, '11', $local);

        self::assertSame(GateStatus::Blocked, $verdict->status);
        self::assertSame(['ci-red', 'local-failed:phpunit'], $verdict->reasons);
    }

    /**
     * The BLOCKED/REVIEW boundary. A red pipeline is the only fact that
     * escalates past REVIEW, and it does so independently of everything else:
     * whichever other denials are present, and however many, the status must
     * still be BLOCKED — the two outcomes route differently for the operator,
     * so the boundary cannot depend on reason ordering or count.
     */
    public function testRedCiBlocksWhateverElseDeniedTheRowAndNothingElseEverBlocks(): void
    {
        $red = $this->pipeline(PipelineStatus::Failed);
        $blocked = (new FastLaneGate())->classify(
            $this->mr(['authorUsername' => 'owenbush', 'authorId' => 1, 'draft' => true, 'headPipeline' => $red]),
            '11',
            null,
        );

        self::assertSame(GateStatus::Blocked, $blocked->status);
        self::assertSame(['not-bot-author', 'draft', 'ci-red', 'local-missing'], $blocked->reasons);
        self::assertSame('BLOCKED not-bot-author, draft, ci-red, local-missing', $blocked->describe());

        // Every other single deviation, alone or combined, stops at REVIEW.
        foreach (
            [
            'no pipeline' => ['headPipeline' => null],
            'unfinished pipeline' => ['headPipeline' => $this->pipeline(PipelineStatus::Running)],
            'draft' => ['draft' => true],
            'human author' => ['authorUsername' => 'owenbush', 'authorId' => 1],
            ] as $label => $overrides
        ) {
            $verdict = (new FastLaneGate())->classify($this->mr($overrides), '11', $this->local());
            self::assertSame(GateStatus::Review, $verdict->status, $label);
        }
    }

    /**
     * The READY-AUTO/REVIEW boundary on local evidence: freshness is an exact
     * SHA identity, never a prefix or a substring. A result recorded against a
     * head whose SHA merely starts the same way is evidence about a different
     * commit.
     */
    public function testLocalEvidenceMustMatchTheHeadShaExactlyAndNotMerelyByPrefix(): void
    {
        $gate = new FastLaneGate();

        $exact = $gate->classify($this->mr(), '11', $this->local(sha: self::HEAD_SHA));
        $prefix = $gate->classify($this->mr(), '11', $this->local(sha: substr(self::HEAD_SHA, 0, 12)));
        $extended = $gate->classify($this->mr(), '11', $this->local(sha: self::HEAD_SHA . 'ff'));

        self::assertSame(GateStatus::ReadyAuto, $exact->status);
        self::assertSame(['local-stale'], $prefix->reasons);
        self::assertSame(['local-stale'], $extended->reasons);
    }

    public function testDraftBotMrWithNoPipelineAndNoLocalResultsAccumulatesAllThreeDenials(): void
    {
        // The live field_visibility_conditions !2 shape: draft, no head
        // pipeline, nothing cached — each denial is independently visible.
        $mr = $this->mr(['draft' => true, 'headPipeline' => null]);

        $verdict = (new FastLaneGate())->classify($mr, '11', null);

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['draft', 'ci-missing', 'local-missing'], $verdict->reasons);
    }

    public function testBotPatternIsParameterizedByTargetCoreVersionNotHardCoded(): void
    {
        // A hypothetical future transition where the bot uses a dedicated
        // branch per core: the SAME MR must classify differently depending on
        // the target core version of the row.
        $gate = new FastLaneGate(static fn (string $core): BotPattern => new BotPattern(
            sourceBranch: $core === '12' ? 'project-update-bot-d12' : 'project-update-bot-only',
        ));
        $mr = $this->mr(['sourceBranch' => 'project-update-bot-d12']);

        $forD12 = $gate->classify($mr, '12', $this->local());
        $forD11 = $gate->classify($mr, '11', $this->local());

        self::assertSame(GateStatus::ReadyAuto, $forD12->status);
        self::assertSame(GateStatus::Review, $forD11->status);
        self::assertSame(['not-bot-author'], $forD11->reasons);
    }

    public function testDefaultPatternProviderIsBotPatternForCore(): void
    {
        // With no explicit provider the gate consults BotPattern::forCore(),
        // the one config point where per-core pattern differences live.
        $verdict = (new FastLaneGate())->classify($this->mr(), '12', $this->local());

        self::assertSame(GateStatus::ReadyAuto, $verdict->status);
    }
}
