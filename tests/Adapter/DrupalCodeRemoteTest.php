<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\DrupalCodeRemote;

/**
 * Fetch over anonymous HTTPS, push over SSH.
 *
 * The split exists so upkeep never handles a credential for git. Feeding the
 * PAT to git has no implementation that does not write it to disk or expose it
 * in process argv, and every one of those would be a second exception to the
 * rule that UPKEEP_GITLAB_TOKEN never reaches a child process.
 */
final class DrupalCodeRemoteTest extends TestCase
{
    public function testTheCloneUrlIsHttpsSoAnonymousReadNeedsNothing(): void
    {
        self::assertSame(
            'https://git.drupalcode.org/project/pathauto.git',
            DrupalCodeRemote::httpsUrl('project/pathauto'),
        );
    }

    public function testAnHttpsDrupalCodeRemoteBecomesItsSshForm(): void
    {
        self::assertSame(
            'git@git.drupalcode.org:project/pathauto.git',
            DrupalCodeRemote::sshPushUrl('https://git.drupalcode.org/project/pathauto.git'),
        );
    }

    public function testItRoundTripsWhateverTheCloneUrlWas(): void
    {
        $https = DrupalCodeRemote::httpsUrl('project/entity_type_access_conditions');

        self::assertSame(
            'git@git.drupalcode.org:project/entity_type_access_conditions.git',
            DrupalCodeRemote::sshPushUrl($https),
        );
    }

    /** Trailing whitespace: `git remote get-url` answers with a newline. */
    public function testSurroundingWhitespaceIsIgnored(): void
    {
        self::assertSame(
            'git@git.drupalcode.org:project/pathauto.git',
            DrupalCodeRemote::sshPushUrl("  https://git.drupalcode.org/project/pathauto.git\n"),
        );
    }

    /**
     * The refusal that matters. A remote somebody set deliberately — a fork, a
     * mirror, an already-SSH clone, another host entirely — is left exactly as
     * it is. Guessing at one is how a push lands somewhere nobody intended.
     *
     * @param string $remote a URL that is not ours to rewrite
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('remotesToLeaveAlone')]
    public function testARemoteThatIsNotOursIsLeftAlone(string $remote): void
    {
        self::assertNull(DrupalCodeRemote::sshPushUrl($remote));
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function remotesToLeaveAlone(): iterable
    {
        yield 'already SSH' => ['git@git.drupalcode.org:project/pathauto.git'];
        yield 'a fork on another host' => ['https://github.com/owenbush/pathauto.git'];
        yield 'a self-hosted GitLab' => ['https://gitlab.example.com/project/pathauto.git'];
        yield 'plain http' => ['http://git.drupalcode.org/project/pathauto.git'];
        yield 'empty' => [''];
        yield 'the base with no project' => ['https://git.drupalcode.org/'];
        yield 'the base with only a suffix' => ['https://git.drupalcode.org/.git'];
        // A lookalike host: the check is a prefix on the full base URL, so a
        // domain that merely contains ours does not match.
        yield 'a lookalike host' => ['https://git.drupalcode.org.evil.test/project/pathauto.git'];
    }
}
