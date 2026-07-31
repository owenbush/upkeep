<?php

declare(strict_types=1);

namespace Upkeep\Tests\Config;

use PHPUnit\Framework\TestCase;
use Upkeep\Config\BotPattern;

final class BotPatternTest extends TestCase
{
    public function testVerifiedDefaultsMatchThePhaseOneObservations(): void
    {
        $pattern = new BotPattern();

        self::assertSame('Project-Update-Bot', $pattern->authorUsername);
        self::assertSame(66574, $pattern->authorId);
        self::assertSame('project-update-bot-only', $pattern->sourceBranch);
        self::assertSame('Automated Project Update Bot fixes', $pattern->title);
    }

    public function testMatchesAuthorByUsername(): void
    {
        $pattern = new BotPattern();

        self::assertTrue($pattern->matchesAuthor('Project-Update-Bot', null));
    }

    public function testMatchesAuthorByIdEvenWhenUsernameChanged(): void
    {
        $pattern = new BotPattern();

        self::assertTrue($pattern->matchesAuthor('Renamed-Bot-Account', 66574));
    }

    public function testDoesNotMatchAnUnrelatedAuthor(): void
    {
        $pattern = new BotPattern();

        self::assertFalse($pattern->matchesAuthor('owenbush', 12345));
        self::assertFalse($pattern->matchesAuthor('owenbush', null));
    }

    public function testForCoreReturnsTheVerifiedPatternForEveryCurrentCoreVersion(): void
    {
        // The observed bot pattern is core-independent today; forCore() is
        // the single config point where a future per-core divergence lands.
        foreach (['10', '11', '12'] as $core) {
            $pattern = BotPattern::forCore($core);

            self::assertSame('Project-Update-Bot', $pattern->authorUsername);
            self::assertSame('project-update-bot-only', $pattern->sourceBranch);
        }
    }

    public function testFullMatchRequiresAuthorAndSourceBranch(): void
    {
        $pattern = new BotPattern();

        self::assertTrue($pattern->matches('Project-Update-Bot', 66574, 'project-update-bot-only'));
        // Right author, wrong branch: a human pushing from another branch under
        // the bot account name must not classify as a bot MR for the gate.
        self::assertFalse($pattern->matches('Project-Update-Bot', 66574, 'feature/other'));
        // Right branch, wrong author.
        self::assertFalse($pattern->matches('owenbush', 12345, 'project-update-bot-only'));
    }
}
