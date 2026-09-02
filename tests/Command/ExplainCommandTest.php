<?php

declare(strict_types=1);

namespace Upkeep\Tests\Command;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Tester\CommandTester;
use Upkeep\Command\ExplainCommand;
use Upkeep\Command\Glossary;
use Upkeep\Workflow\ExitCode;

/**
 * The answer to "what does that word mean?", which until now was "read the
 * source".
 *
 * `patch↑` is the case that motivated it: printed on dashboard rows, defined in
 * one comment inside DashboardCommand, and mentioned nowhere a user would look.
 */
final class ExplainCommandTest extends TestCase
{
    private function explain(string ...$args): CommandTester
    {
        $tester = new CommandTester(new ExplainCommand());
        $tester->execute($args === [] ? [] : ['term' => $args[0]]);

        return $tester;
    }

    public function testItExplainsTheSymbolNobodyCouldLookUp(): void
    {
        $display = $this->explain('patch↑')->getDisplay();

        self::assertStringContainsString('newer than the merge request', $display);
        self::assertStringContainsString('dashboard ISSUE', $display, 'and where it appears');
    }

    /** Bare, it is the whole legend — the point being to see them together. */
    public function testWithNoArgumentItListsEveryTerm(): void
    {
        $display = $this->explain()->getDisplay();

        foreach (['patch↑', 'unclaimed', 'stale', 'empty MR', 'READY-AUTO', 'RTBC'] as $term) {
            self::assertStringContainsString($term, $display, $term . ' should be listed');
        }
    }

    /** A half-remembered word still finds its definition. */
    public function testItMatchesPartiallyAndOnMeaningNotOnlyOnName(): void
    {
        self::assertStringContainsString('empty MR', $this->explain('empty')->getDisplay());
        // "pipeline" appears in no term's name, only in what CI failed means.
        self::assertStringContainsString('CI failed', $this->explain('pipeline')->getDisplay());
    }

    /**
     * Not knowing a word is an answer, not a failure — a script asking about an
     * unknown term wants to read that rather than handle an error.
     */
    public function testAnUnknownTermIsReportedNotFailed(): void
    {
        $tester = $this->explain('bananas');

        self::assertSame(ExitCode::OK, $tester->getStatusCode());
        self::assertStringContainsString('Nothing in upkeep\'s output is called', $tester->getDisplay());
        self::assertStringContainsString('lists every term', $tester->getDisplay());
    }

    /**
     * Every term the dashboard can actually print should be findable. These
     * are the ones a maintainer meets first, and the gap this command closes.
     */
    public function testTheVocabularyTheDashboardEmitsIsAllDefined(): void
    {
        $defined = array_keys(Glossary::terms());

        $emittedTerms = [
            'ready to merge', 'needs a check', 'checks are stale', 'needs your review',
            'CI failed', 'draft', 'empty MR', 'unclaimed', 'patch↑', 'stale',
        ];
        foreach ($emittedTerms as $emitted) {
            self::assertContains($emitted, $defined, $emitted . ' is printed but not defined');
        }
    }

    /** Every entry says what it means *and* where it is shown. */
    public function testEveryEntryIsAMeaningAndAPlace(): void
    {
        foreach (Glossary::terms() as $term => $entry) {
            self::assertCount(2, $entry, $term);
            self::assertNotSame('', trim($entry[0]), $term . ' has no meaning');
            self::assertNotSame('', trim($entry[1]), $term . ' has no location');
        }
    }

    /** It needs no cockpit — being confused is not a thing to be blocked on. */
    public function testItWorksWithNoCockpitAtAll(): void
    {
        $previous = getcwd();
        self::assertIsString($previous);
        chdir(sys_get_temp_dir());

        try {
            self::assertSame(ExitCode::OK, $this->explain('stale')->getStatusCode());
        } finally {
            chdir($previous);
        }
    }
}
