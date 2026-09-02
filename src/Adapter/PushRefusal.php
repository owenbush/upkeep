<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\Drupal\IssueReference;

/**
 * Why a push was refused, and what to do about it.
 *
 * There are two refusals and they need opposite answers, which is the whole
 * reason this is not one message:
 *
 *   - **Authentication** — the server does not know who you are. Your SSH key
 *     is missing, wrong, or not loaded in the agent.
 *   - **Authorization** — the server knows exactly who you are and will not
 *     let you write here. On drupal.org this is the ordinary state of a fresh
 *     issue fork: creating it does not grant you push access, and there is a
 *     separate button on the issue page that does.
 *
 * They read almost alike in git's output and the fix for one is useless for
 * the other. Telling somebody with an authorization problem to check their SSH
 * key sends them to debug something that already works — which is exactly what
 * this class exists to stop, having done it once.
 *
 * git's own text is always kept above the guidance. It suggests a password,
 * which GitLab will never accept, but hiding what actually happened is worse
 * than including an unhelpful line.
 */
final readonly class PushRefusal
{
    /** GitLab's wording when the key is fine and the account is not allowed. */
    private const AUTHORIZATION = '/not allowed to push|pre-receive hook declined|insufficient permission/i';

    /** ...and when it does not know who is asking at all. */
    private const AUTHENTICATION = '/Access denied|Authentication failed|Permission denied|publickey/i';

    public static function explain(IssueBranch $branch, GitRemote $remote, string $output): string
    {
        $preamble = sprintf(
            "Pushing \"%s\" to %s was refused:\n%s",
            $branch->name,
            $remote->url,
            trim($output),
        );

        // Order matters: an authorization refusal can carry the word
        // "denied" too, and it is the more specific diagnosis.
        if (preg_match(self::AUTHORIZATION, $output) === 1) {
            return $preamble . "\n\n" . self::authorizationHelp($branch->issueNid);
        }

        if (preg_match(self::AUTHENTICATION, $output) === 1) {
            return $preamble . "\n\n" . self::authenticationHelp($remote);
        }

        return $preamble;
    }

    /**
     * The guidance for "GitLab knows you and says no".
     *
     * Creating an issue fork and being allowed to push to it are two separate
     * grants on drupal.org, and the second is a button most people meet only
     * when a push has already failed.
     */
    public static function authorizationHelp(int $issueNid): string
    {
        return sprintf(
            "Your SSH key worked — GitLab knows who you are and will not let you write to this fork.\n"
            . "On drupal.org, creating an issue fork does not grant push access to it; that is a separate\n"
            . "button on the issue.\n\n"
            . "  1. Open %s\n"
            . "  2. In the merge-request section, click the button granting push access to the issue fork\n"
            . "     (\"Get push access\", beside the fork it names)\n"
            . "  3. Re-run the publish\n\n"
            . 'If the fork is somebody else\'s and you are not a maintainer, the access is theirs to give.',
            IssueReference::issueUrl($issueNid),
        );
    }

    /** The guidance for "GitLab does not know who you are". */
    private static function authenticationHelp(GitRemote $remote): string
    {
        return sprintf(
            "upkeep pushes over SSH and never hands git a password or a token, so this is your SSH key.\n"
            . "  - Add one at %s\n"
            . "  - Check it works:  ssh -T %s\n"
            . '  - Make sure the agent has it:  ssh-add -l',
            DrupalCodeRemote::SSH_KEY_URL,
            DrupalCodeRemote::sshHostOf($remote->url),
        );
    }
}
