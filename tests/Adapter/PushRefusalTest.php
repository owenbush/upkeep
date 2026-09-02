<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\GitRemote;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\PushRefusal;

/**
 * Telling "we do not know you" apart from "we know you and no".
 *
 * They read almost alike in git's output and the fix for one is useless for
 * the other. On drupal.org the second is the ordinary state of a fresh issue
 * fork — creating one does not grant push access to it, and the grant is a
 * separate button on the issue page — so it is the case a maintainer meets
 * most, and it was the case the first version of this message got wrong by
 * sending people to debug an SSH key that already worked.
 */
final class PushRefusalTest extends TestCase
{
    private static function branch(): IssueBranch
    {
        return IssueBranch::named(3597857, '3597857-automated-drupal-12-compatibility-fixes');
    }

    private static function remote(): GitRemote
    {
        return GitRemote::issueFork(
            3597857,
            'git@git.drupal.org:issue/entity_type_access_conditions-3597857.git',
        );
    }

    private static function explain(string $output): string
    {
        return PushRefusal::explain(self::branch(), self::remote(), $output);
    }

    /** GitLab's actual wording when the key is fine and the account is not. */
    public function testAnAuthorizationRefusalPointsAtThePushAccessButton(): void
    {
        $message = self::explain(
            "remote: GitLab: You are not allowed to push code to this project.\n"
            . "! [remote rejected] 3597857-fix -> 3597857-fix (pre-receive hook declined)\n",
        );

        self::assertStringContainsString('Your SSH key worked', $message);
        self::assertStringContainsString('does not grant push access', $message);
        self::assertStringContainsString('https://www.drupal.org/node/3597857', $message);
        // The one thing it must not do: send them to debug a working key.
        self::assertStringNotContainsString('ssh-add -l', $message);
        self::assertStringNotContainsString('Add one at', $message);
    }

    public function testAnAuthenticationRefusalPointsAtTheSshKey(): void
    {
        $message = self::explain(
            "remote: HTTP Basic: Access denied.\nfatal: Authentication failed for 'https://...'\n",
        );

        self::assertStringContainsString('this is your SSH key', $message);
        self::assertStringContainsString('ssh -T git@git.drupal.org', $message);
        self::assertStringContainsString('ssh-add -l', $message);
        self::assertStringNotContainsString('push access', $message);
    }

    public function testPermissionDeniedPublickeyIsAuthenticationNotAuthorization(): void
    {
        $message = self::explain("git@git.drupal.org: Permission denied (publickey).\n");

        self::assertStringContainsString('this is your SSH key', $message);
    }

    /**
     * The ordering that matters. GitLab's authorization refusal can carry
     * "denied" too, and it is the more specific diagnosis — matching the
     * authentication pattern first would misroute the commonest case.
     */
    public function testAnAuthorizationRefusalWinsWhenBothWordingsAppear(): void
    {
        $message = self::explain(
            "remote: GitLab: You are not allowed to push code to this project.\n"
            . "remote: Access denied.\n",
        );

        self::assertStringContainsString('does not grant push access', $message);
        self::assertStringNotContainsString('ssh-add -l', $message);
    }

    /** Anything else is reported as itself, with no guessed diagnosis. */
    public function testAnUnrecognisedRefusalGetsNoGuidanceItCannotSupport(): void
    {
        $message = self::explain("! [rejected] main -> main (fetch first)\n");

        self::assertStringContainsString('fetch first', $message);
        self::assertStringNotContainsString('SSH key', $message);
        self::assertStringNotContainsString('push access', $message);
    }

    /**
     * git's own output is always kept. It suggests a password, which GitLab
     * will never accept — but hiding what actually happened is worse than
     * including a line that does not help.
     */
    public function testGitsOwnOutputIsAlwaysKept(): void
    {
        $outputs = [
            'remote: GitLab: You are not allowed to push code to this project.',
            'fatal: Authentication failed',
            'something nobody has classified',
        ];

        foreach ($outputs as $output) {
            self::assertStringContainsString($output, self::explain($output));
        }
    }

    /** The branch and the destination, so a scrollback says what failed. */
    public function testItNamesTheBranchAndTheRemote(): void
    {
        $message = self::explain('anything');

        self::assertStringContainsString('3597857-automated-drupal-12-compatibility-fixes', $message);
        self::assertStringContainsString(
            'git@git.drupal.org:issue/entity_type_access_conditions-3597857.git',
            $message,
        );
    }
}
