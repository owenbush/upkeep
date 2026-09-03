<?php

declare(strict_types=1);

namespace Upkeep\Tests\Dashboard;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Dashboard\LocalEvidence;
use Upkeep\Results\CachedResult;

/**
 * Local check evidence across the cores a branch supports.
 *
 * Core stopped being part of a row's identity and became part of its evidence,
 * which leaves one question: several cores can disagree, and the cell is one
 * string. Worst case wins and names the core it came from — a cell reading
 * `pass` because two of three cores were green would be worse than useless.
 */
final class LocalEvidenceTest extends TestCase
{
    private const HEAD = 'head1111';

    private static function passing(string $sha = self::HEAD): CachedResult
    {
        return new CachedResult($sha, new \DateTimeImmutable(), new CheckRunResult([
            new CheckResult(CheckType::PhpUnit, CheckStatus::Passed, 0, 'ok', 1.0),
        ]));
    }

    private static function failing(string $sha = self::HEAD): CachedResult
    {
        return new CachedResult($sha, new \DateTimeImmutable(), new CheckRunResult([
            new CheckResult(CheckType::PhpCs, CheckStatus::Failed, 1, 'bad spacing', 0.5),
        ]));
    }

    public function testGreenEverywhereNamesEveryCore(): void
    {
        $evidence = LocalEvidence::of(['10' => self::passing(), '11' => self::passing()], self::HEAD);

        self::assertSame('pass 10,11', $evidence->cell());
        self::assertTrue($evidence->allGreen());
    }

    /**
     * A failure outranks everything and the passing cores are *not* listed
     * beside it: the fact that matters is that something is broken and where,
     * and naming the greens next to it buries that.
     */
    public function testAFailureWinsAndNamesOnlyTheFailingCore(): void
    {
        $evidence = LocalEvidence::of(['10' => self::failing(), '11' => self::passing()], self::HEAD);

        self::assertSame('fail 10', $evidence->cell());
        self::assertFalse($evidence->allGreen());
        self::assertTrue($evidence->anyFailed());
    }

    /** Evidence against an older revision is stale, and stale is not a pass. */
    public function testStaleEvidenceOutranksAPass(): void
    {
        $evidence = LocalEvidence::of(['10' => self::passing('older'), '11' => self::passing()], self::HEAD);

        self::assertSame('stale 10', $evidence->cell());
        self::assertFalse($evidence->allGreen());
        self::assertTrue($evidence->anyStale());
    }

    /**
     * The gap that used to be invisible. Under the old model this was two
     * rows — one READY-AUTO, one not — and the fast lane saw the ready one.
     * One row cannot hide it: the pass is qualified by what was not checked.
     */
    public function testAPartialPassNamesWhatWasNotChecked(): void
    {
        $evidence = LocalEvidence::of(['10' => null, '11' => self::passing()], self::HEAD);

        self::assertSame('pass 11 · ? 10', $evidence->cell());
        self::assertFalse($evidence->allGreen(), 'an unchecked core denies the fast lane');
        self::assertTrue($evidence->anyUnchecked());
    }

    public function testNothingCheckedReadsAsTheDashItAlwaysDid(): void
    {
        $evidence = LocalEvidence::of(['10' => null, '11' => null], self::HEAD);

        self::assertSame('–', $evidence->cell());
        self::assertFalse($evidence->allGreen());
    }

    public function testNoApplicableCoresIsNeitherGreenNorAnything(): void
    {
        $evidence = LocalEvidence::none();

        self::assertSame('–', $evidence->cell());
        self::assertSame('–', $evidence->describe());
        self::assertSame([], $evidence->cores());
        self::assertFalse($evidence->allGreen());
    }

    /**
     * No revision to compare against makes everything stale rather than
     * green. The safe direction: an MR whose head SHA the API did not return
     * must not have its old evidence read as current.
     */
    public function testWithoutACurrentRevisionEverythingIsStale(): void
    {
        $evidence = LocalEvidence::of(['11' => self::passing()], null);

        self::assertSame('stale 11', $evidence->cell());
        self::assertFalse($evidence->allGreen());
    }

    /** The detail half of worst-case-plus-detail. */
    public function testVerboseListsEveryCoreWithItsOwnState(): void
    {
        $evidence = LocalEvidence::of(
            ['11' => self::passing(), '10' => self::failing(), '12' => null],
            self::HEAD,
        );

        self::assertSame('10:fail 11:pass 12:unchecked', $evidence->describe());
    }

    /**
     * Cores come back as ordered strings. PHP turns a numeric string array key
     * into an int — "10" => x is stored as 10 => x — so array_keys() hands
     * back list<int> unless it is cast. The same coercion has caught
     * ModuleSummary once already.
     */
    public function testCoresAreOrderedStringsDespitePhpsKeyCoercion(): void
    {
        $evidence = LocalEvidence::of(['11' => null, '9' => null, '10' => null], self::HEAD);

        self::assertSame(['9', '10', '11'], $evidence->cores());
    }
}
