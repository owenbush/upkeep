<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Cockpit\Module;
use Upkeep\Dashboard\DashboardRow;
use Upkeep\Dashboard\LocalEvidence;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Dashboard\RowFactory;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Patches\Contribution;
use Upkeep\Patches\PatchRevision;
use Upkeep\Results\CachedResult;
use Upkeep\Results\ResultKey;
use Upkeep\Results\ResultsCache;

/**
 * Patch contributions as dashboard rows.
 *
 * The load-bearing property is negative: a row carrying only patches must
 * never look mergeable. The fast-lane path partitions on isReadyAuto(), and a
 * patch is not something upkeep can merge at all — so such a row carries no
 * gate verdict, and its check evidence lives in a namespace the gate never
 * reads.
 *
 * A patch is no longer a *kind* of row. An issue with a patch in comment 4 and
 * a merge request in comment 9 is one piece of work that arrived twice, so it
 * is one row with both columns filled — where it used to be two rows and a
 * filter to suppress the duplicate.
 */
final class PatchRowTest extends TestCase
{
    private string $resultsDir;

    protected function setUp(): void
    {
        $this->resultsDir = sys_get_temp_dir() . '/upkeep-patchrow-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->resultsDir));
    }

    private static function patch(string $name, string $url, int $timestamp = 1705400000): IssueFile
    {
        return new IssueFile($name, $url, 2048, $timestamp);
    }

    /** @param list<IssueFile> $files */
    private static function issue(int $nid, array $files, IssueStatus $status = IssueStatus::NeedsReview): Issue
    {
        return new Issue(
            nid: $nid,
            title: 'Automated Drupal 12 compatibility fixes',
            status: $status,
            url: 'https://www.drupal.org/node/' . $nid,
            project: 'widget',
            priority: 200,
            version: '1.0.x-dev',
            component: 'Code',
            category: 'Task',
            files: $files,
        );
    }

    private static function mr(int $iid, string $title, string $branch, ?string $base, ?string $head): MergeRequest
    {
        return new MergeRequest(
            iid: $iid,
            title: $title,
            state: 'opened',
            authorUsername: 'alice',
            authorId: 100,
            sourceBranch: $branch,
            targetBranch: '1.0.x',
            draft: false,
            detailedMergeStatus: null,
            headSha: $head,
            webUrl: 'https://git.drupalcode.org/project/widget/-/merge_requests/' . $iid,
            diffBaseSha: $base,
            diffHeadSha: $head,
        );
    }

    /**
     * @param list<Issue>        $patchIssues
     * @param list<MergeRequest> $mrs
     */
    private static function snapshot(array $patchIssues, array $mrs = []): ModuleSnapshot
    {
        return new ModuleSnapshot(
            new \DateTimeImmutable('2026-08-01T10:00:00+00:00'),
            [
                'id' => 42,
                'path_with_namespace' => 'project/widget',
                'path' => 'widget',
                'default_branch' => '1.0.x',
            ],
            array_map(static fn (MergeRequest $mr): array => $mr->toApiArray(), $mrs),
            [],
            array_map(static fn (Issue $i): array => $i->toApiArray(), $patchIssues),
        );
    }

    private static function module(string ...$cores): Module
    {
        return new Module('widget', 'project/widget', $cores === [] ? ['11'] : array_values($cores));
    }

    private function factory(): RowFactory
    {
        return new RowFactory(new ResultsCache($this->resultsDir));
    }

    /**
     * A row for an issue whose work is patches only: no project, no open merge
     * request, and so no gate verdict.
     *
     * @param list<string> $cores
     */
    private static function patchRow(
        Contribution $contribution,
        ?CachedResult $local,
        array $cores = ['11'],
    ): DashboardRow {
        $byCore = [];
        foreach ($cores as $core) {
            $byCore[$core] = $local;
        }

        return DashboardRow::forIssue(
            'widget',
            '1.0.x',
            $contribution,
            null,
            null,
            LocalEvidence::of($byCore, $contribution->currentRevision()),
            null,
        );
    }

    /**
     * The invariant the whole design rests on. A row with no gate verdict is
     * not ready-auto, and cannot be made so by any evidence.
     */
    public function testAPatchRowIsNeverReadyAuto(): void
    {
        $contribution = new Contribution('widget', self::issue(3597808, [
            self::patch('3597808-9.patch', 'https://www.drupal.org/files/issues/3597808-9.patch'),
        ]));

        $green = new CachedResult(
            PatchRevision::of('https://www.drupal.org/files/issues/3597808-9.patch'),
            new \DateTimeImmutable(),
            new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'ok', 1.0)]),
        );

        $row = self::patchRow($contribution, $green);

        self::assertFalse($row->isReadyAuto(), 'A patch is not something upkeep can merge.');
        self::assertNull($row->verdict);
        self::assertNull($row->mergeRequest);
        self::assertNull($row->project);
        self::assertSame('pass 11', $row->localCell());
    }

    /**
     * A patch has no pipeline — drupal.org runs CI on branches, not on
     * attachments — so the CI cell says so rather than inventing a state.
     */
    public function testAPatchRowRendersWithNoCiAndTheContributionAsItsStatus(): void
    {
        $contribution = new Contribution('widget', self::issue(3597808, [
            self::patch('3597808-9.patch', 'https://example.test/a.patch'),
            self::patch('3597808-4.patch', 'https://example.test/b.patch'),
        ]));

        $cells = self::patchRow($contribution, null)->toTableCells();

        self::assertSame('widget', $cells[0]);
        self::assertSame('3597808 review', $cells[1], 'the issue, and what drupal.org says about it');
        self::assertSame('1.0.x', $cells[2], 'the module branch, not a core version');
        self::assertSame('–', $cells[4], 'no merge request');
        self::assertSame('2', $cells[5], 'two patch files');
        self::assertSame('–', $cells[6], 'no pipeline');
        self::assertSame('–', $cells[7], 'never checked');
        self::assertSame('2 patches', $cells[8]);
        self::assertStringContainsString('upkeep patch:check', $cells[9], 'and what to run about it');
    }

    public function testThePatchRowStatusNamesAnEmptyMergeRequestBesideIt(): void
    {
        $contribution = new Contribution(
            'widget',
            self::issue(3597808, [self::patch('3597808-9.patch', 'https://example.test/a.patch')]),
            [self::mr(1, 'Issue #3597808: bot', '3597808-bot', 'same', 'same')],
        );

        self::assertSame('PATCH 1 patch, !1 empty', $contribution->dashboardStatus());
    }

    /**
     * Evidence is current only when it names the patch that is newest *now*.
     * A re-roll is a new upload at a new URL, so a verdict recorded against
     * the previous one reads as stale rather than as a green light for code
     * nobody checked.
     */
    public function testAReRollMakesAnEarlierVerdictStaleRatherThanGreen(): void
    {
        $old = 'https://www.drupal.org/files/issues/2026-06-11/bot.patch';
        $new = 'https://www.drupal.org/files/issues/2026-07-11/bot.patch';

        $checkedTheOldOne = new CachedResult(
            PatchRevision::of($old),
            new \DateTimeImmutable(),
            new CheckRunResult([new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'ok', 1.0)]),
        );

        // Same filename, later upload — exactly what the update bot produces.
        $contribution = new Contribution('widget', self::issue(3597808, [
            self::patch('bot.patch', $new, 1783803674),
            self::patch('bot.patch', $old, 1781212289),
        ]));

        self::assertSame('stale 11', self::patchRow($contribution, $checkedTheOldOne)->localCell());
    }

    public function testAnIssueWithNoPatchAtAllHasNoRevisionForEvidenceToMatch(): void
    {
        $contribution = new Contribution('widget', self::issue(3597808, []));

        self::assertNull($contribution->currentRevision());
        self::assertSame('PATCH nothing attached', $contribution->dashboardStatus());

        $orphanEvidence = new CachedResult('a1b2c3d', new \DateTimeImmutable(), new CheckRunResult([]));
        self::assertSame('stale 11', self::patchRow($contribution, $orphanEvidence)->localCell());
    }

    // --------------------------------------------------------- via the factory

    /**
     * Rows the way every consumer builds them: from one snapshot.
     *
     * @return list<DashboardRow>
     */
    private function rows(Module $module, ModuleSnapshot $snapshot, ?string $versionFilter = null): array
    {
        return $this->factory()->rows(
            $module,
            $snapshot->project(),
            $snapshot->mergeRequests(),
            $versionFilter,
            [],
            $snapshot,
        );
    }

    /**
     * One row per patch-carrying issue, and the tracked cores are no longer a
     * multiplier — they are the cores the row's evidence covers.
     *
     * This is the halving. Two issues across two tracked cores used to be four
     * rows saying two things.
     */
    public function testEachPatchIssueIsOneRowWhateverCoresTheModuleTracks(): void
    {
        $snapshot = self::snapshot([
            self::issue(3597808, [self::patch('a.patch', 'https://example.test/a.patch')]),
            self::issue(3501234, [self::patch('b.patch', 'https://example.test/b.patch')]),
        ]);

        $rows = $this->rows(self::module('10', '11'), $snapshot);

        self::assertCount(2, $rows);
        self::assertSame(
            [[3501234, '1.0.x'], [3597808, '1.0.x']],
            array_map(static fn (DashboardRow $r): array => [$r->issueNid, $r->branch], $rows),
            'ordered by issue nid; the branch is the only remaining multiplier',
        );
        self::assertSame(['10', '11'], $rows[0]->local->cores(), 'both tracked cores, as evidence');
    }

    /**
     * The duplication the old model created and then filtered out. An issue
     * with a patch *and* an open merge request is one piece of work that
     * arrived twice, so it is one row carrying both — not an MR row plus a
     * patch row that a covered-by-a-merge-request filter has to suppress.
     */
    public function testAnIssueWithBothAPatchAndAMergeRequestIsASingleRow(): void
    {
        $snapshot = self::snapshot(
            [self::issue(3467675, [self::patch('a.patch', 'https://example.test/a.patch')])],
            [self::mr(7, 'Issue #3467675: real work', '3467675-fix', 'base', 'head')],
        );

        $rows = $this->rows(self::module('11'), $snapshot);

        self::assertCount(1, $rows);
        self::assertSame(3467675, $rows[0]->issueNid);
        self::assertSame(7, $rows[0]->mergeRequest?->iid, 'the merge request is what the row acts on');
        self::assertSame('1', $rows[0]->patchCell(), 'and the patch is a column, not a second row');
    }

    /**
     * An issue nobody has contributed to belongs in `upkeep issues`, where
     * being unclaimed is the point. On the dashboard it would be 29 of
     * pathauto's 93 open issues saying nothing.
     */
    public function testAnIssueWithNothingOpenAndNoPatchGetsNoRowAtAll(): void
    {
        $snapshot = self::snapshot([self::issue(3597808, [])]);

        self::assertSame([], $this->rows(self::module('11'), $snapshot));
    }

    /**
     * An empty MR covers nothing, so the patch beside it is still the story —
     * the regression the whole patch surface exists for. One row now says both
     * things at once: an empty merge request, and a patch nobody has applied.
     */
    public function testAnEmptyMergeRequestAndAPatchAreOneRowSayingBoth(): void
    {
        $snapshot = self::snapshot(
            [self::issue(3597808, [self::patch('a.patch', 'https://example.test/a.patch')])],
            [self::mr(1, 'Issue #3597808: bot', '3597808-bot', 'same', 'same')],
        );

        $rows = $this->rows(self::module('11'), $snapshot);

        self::assertCount(1, $rows);
        self::assertStringContainsString('empty', $rows[0]->mergeRequestCell());
        self::assertSame('1', $rows[0]->patchCell());
    }

    /**
     * Patch evidence lives in its own namespace, so an MR result and a patch
     * result for numerically equal identities cannot be read for each other.
     */
    public function testPatchEvidenceIsReadFromThePatchNamespaceOnly(): void
    {
        $url = 'https://example.test/a.patch';
        $cache = new ResultsCache($this->resultsDir);
        $red = new CheckRunResult([new CheckResult(CheckType::PhpCs, CheckStatus::Failed, 1, 'bad', 0.1)]);

        // Same number, different subject: an MR !3597808 and issue #3597808.
        $cache->store('widget', ResultKey::mergeRequest(3597808), '11', str_repeat('a', 40), $red);
        $cache->store('widget', ResultKey::patch(3597808), '11', PatchRevision::of($url), $red);

        $rows = $this->rows(
            self::module('11'),
            self::snapshot([self::issue(3597808, [self::patch('a.patch', $url)])]),
        );

        self::assertSame('fail 11', $rows[0]->localCell());

        $asMr = $cache->latest('widget', ResultKey::mergeRequest(3597808), '11');
        $asPatch = $cache->latest('widget', ResultKey::patch(3597808), '11');
        self::assertNotNull($asMr);
        self::assertNotNull($asPatch);
        self::assertNotSame(
            $asMr->sha,
            $asPatch->sha,
            'The two identities must not resolve to the same entry.',
        );
    }

    public function testAVersionFilterThatMatchesNothingEmitsNoRows(): void
    {
        $snapshot = self::snapshot([self::issue(3597808, [self::patch('a.patch', 'https://example.test/a.patch')])]);

        self::assertSame([], $this->rows(self::module('11'), $snapshot, '9'));
    }

    /**
     * A snapshot cached before patch rows existed carries no patch issues, so
     * the dashboard shows what that release showed until the next --refresh —
     * rather than going to the network from a command whose whole promise is
     * that a cached run costs nothing.
     */
    public function testASnapshotFromBeforePatchRowsContributesNone(): void
    {
        $legacy = ModuleSnapshot::fromJson(json_encode([
            'fetched_at' => '2026-07-30T10:00:00+00:00',
            'project' => ['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget'],
            'merge_requests' => [],
            'issues' => [],
        ], \JSON_THROW_ON_ERROR));

        self::assertNotNull($legacy);
        self::assertSame([], $legacy->patchIssues());
        self::assertSame([], $this->rows(self::module('11'), $legacy));
    }

    public function testPatchIssuesSurviveTheSnapshotRoundTrip(): void
    {
        $original = self::snapshot([
            self::issue(3597808, [self::patch('a.patch', 'https://example.test/a.patch')], IssueStatus::Rtbc),
        ]);

        $restored = ModuleSnapshot::fromJson($original->toJson());

        self::assertNotNull($restored);
        self::assertCount(1, $restored->patchIssues());
        self::assertSame(3597808, $restored->patchIssues()[0]->nid);
        self::assertSame(IssueStatus::Rtbc, $restored->patchIssues()[0]->status);
        self::assertSame(1, $restored->patchIssues()[0]->patchCount());
    }

    /** A malformed patch-issues entry is dropped, not fatal. */
    public function testUnusablePatchIssueEntriesAreDropped(): void
    {
        $restored = ModuleSnapshot::fromJson(json_encode([
            'fetched_at' => '2026-07-30T10:00:00+00:00',
            'project' => ['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget'],
            'merge_requests' => [],
            'issues' => [],
            'patch_issues' => ['not-an-object', ['nid' => 1, 'field_issue_status' => '8']],
        ], \JSON_THROW_ON_ERROR));

        self::assertNotNull($restored);
        self::assertCount(1, $restored->patchIssues());
    }

    public function testANonArrayPatchIssuesKeyReadsAsNone(): void
    {
        $restored = ModuleSnapshot::fromJson(json_encode([
            'fetched_at' => '2026-07-30T10:00:00+00:00',
            'project' => ['id' => 42, 'path_with_namespace' => 'project/widget', 'path' => 'widget'],
            'merge_requests' => [],
            'issues' => [],
            'patch_issues' => 'nonsense',
        ], \JSON_THROW_ON_ERROR));

        self::assertNotNull($restored);
        self::assertSame([], $restored->patchIssues());
    }
}
