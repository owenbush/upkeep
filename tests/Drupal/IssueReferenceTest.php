<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\IssueReference;

final class IssueReferenceTest extends TestCase
{
    #[DataProvider('extractionCases')]
    public function testExtractsIssueNumberFromTitleBranchOrDescription(
        string $title,
        string $branch,
        ?string $description,
        ?int $expected,
    ): void {
        self::assertSame($expected, IssueReference::extract($title, $branch, $description));
    }

    /** @return iterable<string, array{string, string, ?string, ?int}> */
    public static function extractionCases(): iterable
    {
        yield 'title with Issue #NNN:' => [
            'Issue #3467675: Make URL field required by default',
            '3467675-make-url-required',
            null,
            3467675,
        ];

        yield 'title with lowercase issue' => [
            'issue #1234567: fix something',
            'other-branch',
            null,
            1234567,
        ];

        yield 'title without space before hash' => [
            'Issue#9999999: compact format',
            'main',
            null,
            9999999,
        ];

        yield 'branch name only (no title match)' => [
            'Automated Project Update Bot fixes',
            '3181657-test-only',
            null,
            3181657,
        ];

        yield 'no issue reference at all' => [
            'Automated Project Update Bot fixes',
            'project-update-bot-only',
            null,
            null,
        ];

        yield 'title takes precedence over branch' => [
            'Issue #1111111: fix',
            '2222222-something',
            null,
            1111111,
        ];

        yield 'branch with short number (< 4 digits) is ignored' => [
            'Some fix',
            '42-short-id',
            null,
            null,
        ];

        yield 'branch with exactly 4 digits matches' => [
            'Some fix',
            '1234-minimum',
            null,
            1234,
        ];

        yield 'description with Issue #NNN' => [
            'Some MR title',
            'feature-branch',
            'This is related to Issue #3467675 on drupal.org.',
            3467675,
        ];

        yield 'description with Relates to #NNN (bot MR format)' => [
            'Draft: Automated Project Update Bot fixes',
            'project-update-bot-only',
            'Relates to #3597857. This merge request was automatically created by the Project Update Bot.',
            3597857,
        ];

        yield 'description with relates to lowercase' => [
            'Some MR',
            'feature-branch',
            'relates to #1234567',
            1234567,
        ];

        yield 'description with drupal.org project issue URL' => [
            'Fix the thing',
            'fix-branch',
            "See https://www.drupal.org/project/widget/issues/3500000 for details.",
            3500000,
        ];

        yield 'description with drupal.org node URL' => [
            'Update handler',
            'update-handler',
            "Merge request for https://www.drupal.org/node/3600000",
            3600000,
        ];

        yield 'title wins over description' => [
            'Issue #1111111: the title',
            'main',
            'See https://www.drupal.org/node/2222222',
            1111111,
        ];

        yield 'description wins over branch' => [
            'No issue in title',
            '9999999-branch',
            'Issue #5555555: from description',
            5555555,
        ];

        yield 'description with no matches falls through to branch' => [
            'No issue in title',
            '7777777-branch',
            'Just some plain text with no references.',
            7777777,
        ];

        yield 'empty description is same as null' => [
            'No match',
            'plain-branch',
            '',
            null,
        ];
    }

    /**
     * The strict reading, which answers "does this MR carry the work for that
     * issue?" rather than "which issue is this MR about?".
     */
    #[DataProvider('owningExtractionCases')]
    public function testOwningExtractionAcceptsOnlyAuthorshipClaims(
        string $title,
        string $branch,
        ?string $description,
        ?int $expected,
    ): void {
        self::assertSame($expected, IssueReference::extractOwning($title, $branch, $description));
    }

    /** @return iterable<string, array{string, string, ?string, ?int}> */
    public static function owningExtractionCases(): iterable
    {
        // The regression: the Project Update Bot's MR, verbatim. It mentions
        // an issue it did not author, on a branch with no issue number, and
        // under the generous reading it used to claim that issue and suppress
        // every patch on it.
        yield 'a bot MR that merely relates to an issue claims nothing' => [
            'Draft: Automated Project Update Bot fixes',
            'project-update-bot-only',
            'Relates to #3597808. This merge request was automatically created by the Project Update Bot. '
                . 'It contains the changes from run 12-843071.',
            null,
        ];

        yield 'relates to, lowercase, still claims nothing' => [
            'Some MR',
            'feature-branch',
            'relates to #1234567',
            null,
        ];

        // Everything that does assert authorship keeps working.
        yield 'the Issue #NNN title convention' => [
            'Issue #3467675: Make URL field required by default',
            'unrelated-branch',
            null,
            3467675,
        ];

        yield 'an issue-fork branch name' => [
            'Automated Project Update Bot fixes',
            '3181657-test-only',
            null,
            3181657,
        ];

        yield 'Issue #NNN in the description' => [
            'Some MR title',
            'feature-branch',
            'This is related to Issue #3467675 on drupal.org.',
            3467675,
        ];

        yield 'a drupal.org issue URL in the description' => [
            'Fix the thing',
            'fix-branch',
            'See https://www.drupal.org/project/widget/issues/3500000 for details.',
            3500000,
        ];

        yield 'a drupal.org node URL in the description' => [
            'Update handler',
            'update-handler',
            'Merge request for https://www.drupal.org/node/3600000',
            3600000,
        ];

        // A description that both mentions one issue and links another: the
        // mention is skipped and the link — the authorship claim — wins,
        // rather than the mention short-circuiting the search as it does in
        // the generous reading.
        yield 'a link is preferred over a mention of a different issue' => [
            'No issue in title',
            'plain-branch',
            'Relates to #1111111. Fixes https://www.drupal.org/node/2222222',
            2222222,
        ];

        yield 'a mention falls through to the branch number' => [
            'No issue in title',
            '7777777-branch',
            'Relates to #1111111.',
            7777777,
        ];
    }

    /**
     * The two readings differ on exactly one thing, and pinning that keeps the
     * dashboard's generosity from being narrowed by a change made for the
     * patch report's benefit.
     */
    public function testTheGenerousReadingStillAcceptsAMention(): void
    {
        $title = 'Draft: Automated Project Update Bot fixes';
        $branch = 'project-update-bot-only';
        $description = 'Relates to #3597808. This merge request was automatically created by the bot.';

        self::assertSame(3597808, IssueReference::extract($title, $branch, $description));
        self::assertNull(IssueReference::extractOwning($title, $branch, $description));
    }

    public function testIssueUrl(): void
    {
        self::assertSame('https://www.drupal.org/node/3467675', IssueReference::issueUrl(3467675));
    }
}
