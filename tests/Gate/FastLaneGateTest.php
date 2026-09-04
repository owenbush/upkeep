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
use Upkeep\Dashboard\LocalEvidence;
use Upkeep\Gate\GateVerdict;
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

    /**
     * Classify one merge request with the same cached result on every
     * applicable core.
     *
     * The gate no longer takes a core and a result: a row spans every core its
     * branch supports, so it takes the list and the evidence across it. Most
     * cases below are about one core and say the same thing they always did;
     * the ones that are about *disagreement* pass their own byCore map.
     *
     * @param list<string> $cores
     */
    private function verdict(
        MergeRequest $mr,
        ?CachedResult $local,
        array $cores = ['11'],
        ?FastLaneGate $gate = null,
    ): GateVerdict {
        $byCore = [];
        foreach ($cores as $core) {
            $byCore[$core] = $local;
        }

        return ($gate ?? new FastLaneGate())->classify($mr, $cores, LocalEvidence::of($byCore, $mr->headSha));
    }

    public function testBotMrWithGreenCiAndFreshGreenLocalChecksIsReadyAuto(): void
    {
        $verdict = $this->verdict($this->mr(), $this->local());

        self::assertSame(GateStatus::ReadyAuto, $verdict->status);
        self::assertSame([], $verdict->reasons);
    }

    public function testNonBotAuthorAllGreenRoutesToReviewWithNotBotAuthorReason(): void
    {
        $mr = $this->mr(['authorUsername' => 'owenbush', 'authorId' => 12345]);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['not-bot-author'], $verdict->reasons);
    }

    public function testBotAuthorFromForeignSourceBranchIsNotBotForTheGate(): void
    {
        // A push from any other branch — even under the bot account — must
        // not classify as a bot MR (BotPattern::matches contract).
        $mr = $this->mr(['sourceBranch' => 'feature/sneaky']);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['not-bot-author'], $verdict->reasons);
    }

    public function testBotMrWithRedCiIsBlockedWithCiRedReason(): void
    {
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Failed)]);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Blocked, $verdict->status);
        self::assertSame(['ci-red'], $verdict->reasons);
    }

    public function testAbsentHeadPipelineDeniesReadyAutoWithCiMissingReason(): void
    {
        $mr = $this->mr(['headPipeline' => null]);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['ci-missing'], $verdict->reasons);
    }

    public function testRunningPipelineDeniesReadyAutoWithCiNotGreenReason(): void
    {
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Running)]);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['ci-not-green:running'], $verdict->reasons);
    }

    public function testUnknownPipelineStatusDeniesReadyAutoAndSurfacesTheRawStatus(): void
    {
        // A future GitLab status string maps to PipelineStatus::Unknown; the
        // raw API value must still be visible in the reason.
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Unknown, 'some_future_status')]);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['ci-not-green:some_future_status'], $verdict->reasons);
    }

    public function testCanceledPipelineIsNotGreenNotBlocked(): void
    {
        // Only an actual Failed pipeline blocks; canceled/skipped are
        // non-green unknowns that route to review.
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Canceled)]);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['ci-not-green:canceled'], $verdict->reasons);
    }

    public function testDraftFlagDeniesReadyAutoWithDraftReason(): void
    {
        $mr = $this->mr(['draft' => true, 'title' => 'Draft: Automated Project Update Bot fixes']);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['draft'], $verdict->reasons);
    }

    public function testDetailedMergeStatusDraftStatusDeniesReadyAutoEvenWithoutDraftFlag(): void
    {
        $mr = $this->mr(['draft' => false, 'detailedMergeStatus' => 'draft_status']);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['draft'], $verdict->reasons);
    }

    public function testDraftFlagAndDraftStatusTogetherYieldOneDraftReason(): void
    {
        $mr = $this->mr(['draft' => true, 'detailedMergeStatus' => 'draft_status']);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(['draft'], $verdict->reasons);
    }

    public function testAbsentLocalResultsDenyReadyAutoWithLocalMissingReason(): void
    {
        $verdict = $this->verdict($this->mr(), null);

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-missing'], $verdict->reasons);
    }

    public function testLocalResultForAnOlderHeadShaIsStaleAndDeniesReadyAuto(): void
    {
        $stale = $this->local(sha: 'older-sha-0000000000000000000000000000000');

        $verdict = $this->verdict($this->mr(), $stale);

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-stale'], $verdict->reasons);
    }

    public function testUnknownMrHeadShaMakesAnyLocalResultUnverifiableHenceStale(): void
    {
        // Freshness cannot be confirmed without the MR's current head SHA;
        // the conservative gate treats unverifiable evidence as stale.
        $mr = $this->mr(['headSha' => null]);

        $verdict = $this->verdict($mr, $this->local());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-stale'], $verdict->reasons);
    }

    public function testFailedLocalCheckRoutesToReviewNamingTheFailingCheck(): void
    {
        $local = $this->local([
            new CheckResult(CheckType::PhpStan, CheckStatus::Passed, 0, 'OK', 1.0),
            new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 2, 'FAILURES!', 3.0),
        ]);

        $verdict = $this->verdict($this->mr(), $local);

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-failed:phpunit'], $verdict->reasons);
    }

    public function testEveryFailingLocalCheckIsNamedNotJustTheFirst(): void
    {
        $local = $this->local([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 2, 'FAILURES!', 3.0),
            new CheckResult(CheckType::PhpStan, CheckStatus::Failed, 1, 'errors', 2.0),
        ]);

        $verdict = $this->verdict($this->mr(), $local);

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

        $verdict = $this->verdict($this->mr(), $local);

        self::assertSame(GateStatus::ReadyAuto, $verdict->status);
        self::assertSame([], $verdict->reasons);
    }

    public function testRedCiWithFailedLocalCheckIsBlockedAndCarriesAllReasons(): void
    {
        // BLOCKED (ci-red) takes precedence over REVIEW, and every deny
        // reason stays visible.
        $mr = $this->mr(['headPipeline' => $this->pipeline(PipelineStatus::Failed)]);
        $local = $this->local([new CheckResult(CheckType::PhpUnit, CheckStatus::Failed, 2, 'FAILURES!', 3.0)]);

        $verdict = $this->verdict($mr, $local);

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
        $blocked = $this->verdict(
            $this->mr(['authorUsername' => 'owenbush', 'authorId' => 1, 'draft' => true, 'headPipeline' => $red]),
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
            $verdict = $this->verdict($this->mr($overrides), $this->local());
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

        $exact = $this->verdict($this->mr(), $this->local(sha: self::HEAD_SHA), ['11'], $gate);
        $prefix = $this->verdict($this->mr(), $this->local(sha: substr(self::HEAD_SHA, 0, 12)), ['11'], $gate);
        $extended = $this->verdict($this->mr(), $this->local(sha: self::HEAD_SHA . 'ff'), ['11'], $gate);

        self::assertSame(GateStatus::ReadyAuto, $exact->status);
        self::assertSame(['local-stale'], $prefix->reasons);
        self::assertSame(['local-stale'], $extended->reasons);
    }

    public function testDraftBotMrWithNoPipelineAndNoLocalResultsAccumulatesAllThreeDenials(): void
    {
        // The live field_visibility_conditions !2 shape: draft, no head
        // pipeline, nothing cached — each denial is independently visible.
        $mr = $this->mr(['draft' => true, 'headPipeline' => null]);

        $verdict = $this->verdict($mr, null);

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

        $forD12 = $this->verdict($mr, $this->local(), ['12'], $gate);
        $forD11 = $this->verdict($mr, $this->local(), ['11'], $gate);

        self::assertSame(GateStatus::ReadyAuto, $forD12->status);
        self::assertSame(GateStatus::Review, $forD11->status);
        self::assertSame(['not-bot-author'], $forD11->reasons);
    }

    public function testDefaultPatternProviderIsBotPatternForCore(): void
    {
        // With no explicit provider the gate consults BotPattern::forCore(),
        // the one config point where per-core pattern differences live.
        $verdict = $this->verdict($this->mr(), $this->local(), ['12']);

        self::assertSame(GateStatus::ReadyAuto, $verdict->status);
    }

    // ------------------------------------------------- evidence across cores

    /**
     * The behaviour change the row model forced, and the reason it is worth
     * making. A merge request green on 11 and never checked on 10 used to be
     * *two* rows — one READY-AUTO, one not — and the fast lane offered the
     * ready one. The evidence supporting that merge covered half the cores the
     * branch claims, and nothing anywhere said so.
     *
     * One row cannot hide it: a core that applies and has no fresh pass denies
     * the fast lane.
     */
    public function testAPassOnOneCoreAndNothingOnAnotherIsNoLongerReadyAuto(): void
    {
        $mr = $this->mr();
        $verdict = (new FastLaneGate())->classify($mr, ['10', '11'], LocalEvidence::of(
            ['10' => null, '11' => $this->local()],
            $mr->headSha,
        ));

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['local-missing'], $verdict->reasons);
    }

    /** Green on every applicable core is the only thing that is green. */
    public function testGreenOnEveryApplicableCoreIsReadyAuto(): void
    {
        $mr = $this->mr();
        $verdict = (new FastLaneGate())->classify($mr, ['10', '11'], LocalEvidence::of(
            ['10' => $this->local(), '11' => $this->local()],
            $mr->headSha,
        ));

        self::assertSame(GateStatus::ReadyAuto, $verdict->status);
    }

    /**
     * A check that fails on one core is named once, not once per core. The
     * reason feeds Guidance's "phpcs failed" phrase, and "2 checks failed"
     * for one broken check on two cores would be a lie about the branch.
     */
    public function testTheSameFailingCheckOnTwoCoresIsOneReason(): void
    {
        $mr = $this->mr();
        $failing = $this->local([new CheckResult(CheckType::PhpCs, CheckStatus::Failed, 1, 'spacing', 0.5)]);

        $verdict = (new FastLaneGate())->classify($mr, ['10', '11'], LocalEvidence::of(
            ['10' => $failing, '11' => $failing],
            $mr->headSha,
        ));

        self::assertSame(['local-failed:phpcs'], $verdict->reasons);
    }

    /**
     * Missing and stale evidence on different cores are different facts and
     * both are reported: one core has never been checked, another was checked
     * against a commit that is no longer the head.
     */
    public function testMissingAndStaleOnDifferentCoresAreBothReported(): void
    {
        $mr = $this->mr();
        $verdict = (new FastLaneGate())->classify($mr, ['10', '11', '12'], LocalEvidence::of(
            [
                '10' => null,
                '11' => $this->local(sha: 'older-sha-000000000000000000000000000000'),
                '12' => $this->local(),
            ],
            $mr->headSha,
        ));

        self::assertSame(['local-missing', 'local-stale'], $verdict->reasons);
    }

    /**
     * A row with no applicable cores at all is not a row to merge. It should
     * not arise — RowFactory returns no rows when the core list is empty — but
     * the gate is the thing that must never say yes on absent evidence, so it
     * refuses on its own rather than trusting its caller.
     */
    public function testNoApplicableCoresDeniesRatherThanPassingVacuously(): void
    {
        $verdict = (new FastLaneGate())->classify($this->mr(), [], LocalEvidence::none());

        self::assertSame(GateStatus::Review, $verdict->status);
        self::assertSame(['not-bot-author', 'local-missing'], $verdict->reasons);
    }
}
