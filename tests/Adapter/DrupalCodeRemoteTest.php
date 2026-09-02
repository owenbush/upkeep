<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\DrupalCodeRemote;

/**
 * Fetch over anonymous HTTPS; push over SSH, to a URL GitLab supplied.
 *
 * The split exists so upkeep never handles a credential for git. Feeding it
 * the PAT has no implementation that does not write the token to disk or
 * expose it in process argv, and each would be a second exception to the rule
 * that UPKEEP_GITLAB_TOKEN never reaches a child process.
 *
 * What is pinned here is the small part upkeep still spells itself. Push URLs
 * are *not* constructed — they come from `ssh_url_to_repo` — because
 * git.drupalcode.org serves the web and the API while the SSH remote it
 * advertises is git.drupal.org. Assembling one from the host you cloned from
 * produces an address that does not answer, which is exactly the bug this
 * class was written with and had to have taken back out.
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

    /**
     * The host named in a "your SSH key is not working" message, read off the
     * remote rather than hardcoded — telling somebody to `ssh -T` a host that
     * was never involved sends them to debug the wrong thing.
     *
     * @param string $url  a remote as GitLab reports it
     * @param string $host what to tell the operator to test
     */
    #[DataProvider('remotesAndTheirHosts')]
    public function testTheSshHostIsTakenFromTheRemote(string $url, string $host): void
    {
        self::assertSame($host, DrupalCodeRemote::sshHostOf($url));
    }

    /**
     * @return iterable<string, array{string, string}>
     */
    public static function remotesAndTheirHosts(): iterable
    {
        // What git.drupalcode.org actually advertises — a different host from
        // the one serving the API, which is the whole reason for this method.
        yield 'a canonical project' => [
            'git@git.drupal.org:project/pathauto.git',
            'git@git.drupal.org',
        ];
        yield 'an issue fork' => [
            'git@git.drupal.org:issue/entity_type_access_conditions-3597857.git',
            'git@git.drupal.org',
        ];
        yield 'trailing newline from git' => [
            "git@git.drupal.org:project/pathauto.git\n",
            'git@git.drupal.org',
        ];
        yield 'somebody else entirely' => [
            'git@github.com:owenbush/pathauto.git',
            'git@github.com',
        ];
    }

    /**
     * A URL with no SSH shape at all falls back to naming drupal.org's host,
     * because the message it feeds is still about drupal.org. It is guidance
     * in an error, not a value anything acts on.
     */
    #[DataProvider('urlsWithNoSshHost')]
    public function testAUrlWithNoSshHostFallsBackRatherThanReturningNonsense(string $url): void
    {
        self::assertSame('git@git.drupal.org', DrupalCodeRemote::sshHostOf($url));
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function urlsWithNoSshHost(): iterable
    {
        yield 'https' => ['https://git.drupalcode.org/project/pathauto.git'];
        yield 'empty' => [''];
        yield 'whitespace' => ["  \n"];
        yield 'a bare path' => ['/srv/git/pathauto.git'];
    }
}
