<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\ManagedBranch;
use Upkeep\Adapter\MrCheckout;
use Upkeep\Adapter\PatchApplication;
use Upkeep\Adapter\PatchCheckout;

final class PatchCheckoutTest extends TestCase
{
    private static function patch(): PatchApplication
    {
        return new PatchApplication(3597808, '3597808-9-fix.patch', '/tmp/cache/3597808/3597808-9-fix.patch');
    }

    /**
     * Keyed by issue rather than by file: re-applying a re-roll from the same
     * issue replaces the branch instead of leaving one branch per attempt.
     */
    public function testTheBranchIsNamedForTheIssue(): void
    {
        self::assertSame('patch-3597808', self::patch()->branchName());
        self::assertSame('patch-3597808', ManagedBranch::forPatch(3597808));
    }

    /**
     * -p1 because drupal.org patches are diffs cut at the repository root;
     * --index so the commit that follows needs no separate `git add` and a
     * partial application cannot leave staged and unstaged halves disagreeing.
     */
    public function testApplyArgsStageWhatTheyApply(): void
    {
        self::assertSame(
            ['apply', '--index', '-p1', '/tmp/p.patch'],
            PatchCheckout::applyArgs('/tmp/p.patch'),
        );
        self::assertSame(
            ['apply', '--index', '-p1', '--3way', '/tmp/p.patch'],
            PatchCheckout::threeWayApplyArgs('/tmp/p.patch'),
        );
    }

    /**
     * The message names the file, not just the issue: an issue routinely
     * carries several re-rolls and the working copy should record which one
     * it holds.
     */
    public function testTheCommitMessageNamesTheFileAndTheIssue(): void
    {
        self::assertSame(
            'Apply 3597808-9-fix.patch (issue #3597808) [upkeep]',
            PatchCheckout::commitMessage(self::patch()),
        );
    }

    /**
     * The real `git apply --check -v` and `--stat` output for
     * entity_type_access_conditions issue #3597857 against 1.0.x: nine files,
     * eight of which apply. Kept verbatim because the parsing exists to read
     * exactly this, and a hand-tidied sample would not have caught that git
     * prints the failing file twice.
     */
    /** The failing file, long enough that git's two mentions of it need wrapping. */
    private const FAILING_FILE =
        'tests/modules/entity_type_access_conditions_test/entity_type_access_conditions_test.info.yml';

    /**
     * Real `git apply --check -v` output, reassembled line by line because the
     * paths git prints are longer than the line limit. Verbatim otherwise: the
     * parsing exists to read exactly this, and a tidied sample would not have
     * caught that git names the failing file twice.
     */
    private static function checkOutput(): string
    {
        return implode("\n", [
            'Checking patch composer.json...',
            'Checking patch entity_type_access_conditions.info.yml...',
            'Checking patch entity_type_access_conditions.module...',
            'Checking patch entity_type_access_conditions.services.yml...',
            'Checking patch src/Hook/EntityTypeAccessConditionsHooks.php...',
            'Checking patch ' . self::FAILING_FILE . '...',
            'error: while searching for:',
            "name: 'Entity Type Access Conditions Test'",
            'type: module',
            "description: 'Provides a test condition plugin for Entity Type Access Conditions module.'",
            'core_version_requirement: ^10 || ^11',
            'package: Testing',
            'hidden: true',
            '',
            '',
            'error: patch failed: ' . self::FAILING_FILE . ':1',
            'error: ' . self::FAILING_FILE . ': patch does not apply',
            'Checking patch tests/src/Kernel/MediaAccessConditionsTest.php...',
        ]);
    }

    private static function statOutput(): string
    {
        return implode("\n", [
            ' composer.json                                      |    4 +-',
            ' entity_type_access_conditions.info.yml             |    2 -',
            ' 9 files changed, 73 insertions(+), 14 deletions(-)',
        ]);
    }

    /**
     * A patch that no longer applies has told the maintainer something true
     * about the contribution, so it reads as a review finding — and names
     * *which* file is stale, because "one of these nine" is actionable and
     * "the patch failed" is not.
     */
    public function testAnUnappliablePatchNamesTheFileThatMovedOn(): void
    {
        $e = PatchCheckout::unappliableException(
            self::patch(),
            '1.0.x',
            self::statOutput(),
            self::checkOutput(),
        );

        self::assertInstanceOf(AdapterException::class, $e);
        $message = $e->getMessage();
        self::assertStringContainsString('3597808-9-fix.patch', $message);
        self::assertStringContainsString('"1.0.x"', $message);
        self::assertStringContainsString('It changes 9 file(s); 8 apply, 1 do not:', $message);
        self::assertStringContainsString('entity_type_access_conditions_test.info.yml', $message);
        self::assertStringContainsString('git looked for this and did not find it:', $message);
        self::assertStringContainsString("core_version_requirement: ^10 || ^11", $message);
        self::assertStringContainsString('Re-roll against "1.0.x"', $message);
    }

    public function testTheFailingFileIsNamedOnceEvenThoughGitPrintsItTwice(): void
    {
        self::assertSame(
            ['tests/modules/entity_type_access_conditions_test/entity_type_access_conditions_test.info.yml'],
            PatchCheckout::failedFiles(self::checkOutput()),
        );
    }

    public function testTheTouchedFileCountComesFromTheStatSummary(): void
    {
        self::assertSame(9, PatchCheckout::touchedFiles(self::statOutput()));
        self::assertSame(1, PatchCheckout::touchedFiles(' x | 1 +
 1 file changed, 1 insertion(+)'));
        self::assertSame(0, PatchCheckout::touchedFiles('nothing git would ever print'));
    }

    public function testTheSearchedContextIsTheBlockGitCouldNotFind(): void
    {
        $context = PatchCheckout::searchedContext(self::checkOutput());

        self::assertNotNull($context);
        self::assertStringStartsWith("name: 'Entity Type Access Conditions Test'", $context);
        self::assertStringNotContainsString('error:', $context);
    }

    /**
     * A patch git refused without naming a file — it ran out of something
     * else, or the failure was not per-hunk. The report still says what the
     * patch wanted to change rather than dropping to a bare "it failed".
     */
    public function testAFailureWithNoNamedFileStillReportsTheScopeOfThePatch(): void
    {
        $e = PatchCheckout::unappliableException(
            self::patch(),
            '1.0.x',
            self::statOutput(),
            "error: unrecognized input\n",
        );

        self::assertStringContainsString('It changes 9 file(s).', $e->getMessage());
        self::assertStringNotContainsString('apply, ', $e->getMessage());
        self::assertStringContainsString('Re-roll it against "1.0.x"', $e->getMessage());
    }

    /** Output with nothing to parse degrades to a report without those parts. */
    public function testAFailureGitSaidNothingUsableAboutStillReportsWhatItCan(): void
    {
        $e = PatchCheckout::unappliableException(self::patch(), '1.0.x', '', '');

        self::assertNull(PatchCheckout::searchedContext(''));
        self::assertSame([], PatchCheckout::failedFiles(''));
        self::assertStringContainsString('does not apply to "1.0.x"', $e->getMessage());
        self::assertStringContainsString('Re-roll it against "1.0.x"', $e->getMessage());
    }

    /**
     * The rung that matters in practice. drupal.org patches are generated
     * against an export whose files may carry a trailing blank line the
     * repository does not, so a hunk header promises seven context lines for a
     * six-line file — and git refuses all nine files over one of them. -C1
     * requires one line of context instead of three, without loosening what
     * has to match.
     */
    public function testReducedContextIsTheThirdRungAndOnlyLoosensContext(): void
    {
        self::assertSame(
            ['apply', '--index', '-p1', '-C1', '/tmp/p.patch'],
            PatchCheckout::reducedContextApplyArgs('/tmp/p.patch'),
        );
        self::assertSame(['apply', '--check', '-v', '-p1', '/tmp/p.patch'], PatchCheckout::checkArgs('/tmp/p.patch'));
        self::assertSame(['apply', '--stat', '-p1', '/tmp/p.patch'], PatchCheckout::statArgs('/tmp/p.patch'));
    }

    /**
     * Both apply paths create a branch, and neither branch is ever a valid
     * *base*. Resolving a base of patch-3597808 would silently test the next
     * contribution stacked on the previous one, so the two prefixes are
     * recognised together.
     */
    public function testAPatchBranchIsNeverMistakenForABase(): void
    {
        self::assertTrue(ManagedBranch::isManaged('patch-3597808'));
        self::assertTrue(ManagedBranch::isManaged('mr-7'));
        self::assertFalse(ManagedBranch::isManaged('1.0.x'));
        self::assertFalse(ManagedBranch::isManaged('patch-things-up'));
        self::assertFalse(ManagedBranch::isManaged('feature/mr-7'));

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessageMatches('/patch-3597808/');
        MrCheckout::resolveBaseBranch('patch-3597808', null);
    }

    public function testARecordedBaseStillWinsFromAPatchBranch(): void
    {
        self::assertSame('1.0.x', MrCheckout::resolveBaseBranch('patch-3597808', '1.0.x'));
    }
}
