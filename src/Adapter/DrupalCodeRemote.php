<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The git.drupalcode.org remote, in its two forms.
 *
 * Cloning happens over HTTPS because anonymous read needs no credentials and
 * every public contrib project answers it — checking a merge request, applying
 * a patch and running the suite all work with no key and no account, and that
 * must stay true.
 *
 * Pushing is SSH, to a URL **GitLab itself supplies** (`ssh_url_to_repo`) and
 * never one assembled here. That is not fussiness: git.drupalcode.org serves
 * the web and the API, and the SSH remote it advertises is on git.drupal.org.
 * A URL built by swapping the scheme on the host you fetched from points
 * somewhere that does not answer.
 *
 * SSH rather than the PAT because **upkeep never handles a credential for
 * git**. Every way of feeding git a token writes it to disk (`.git/config`, a
 * credential store) or exposes it in process argv where `ps` can read it, and
 * each would be a second exception to the rule that `UPKEEP_GITLAB_TOKEN`
 * never reaches a child process. The operator's agent answers instead.
 */
final readonly class DrupalCodeRemote
{
    public const HTTPS_BASE = 'https://git.drupalcode.org/';

    /** Only a fallback for messages; real URLs come from the API. */
    private const DEFAULT_SSH_HOST = 'git@git.drupal.org';

    /** Where drupal.org takes SSH keys, named in the failure that needs it. */
    public const SSH_KEY_URL = 'https://git.drupalcode.org/-/user_settings/ssh_keys';


    public static function httpsUrl(string $project): string
    {
        return self::HTTPS_BASE . $project . '.git';
    }

    /**
     * The host part of an SSH remote, for telling somebody what to try
     * `ssh -T` against.
     *
     * Worth extracting rather than hardcoding: git.drupalcode.org serves the
     * web and the API, but the SSH remote GitLab advertises is on
     * git.drupal.org. Naming the wrong one in a recovery instruction sends
     * people to test a host that was never the problem.
     */
    public static function sshHostOf(string $sshUrl): string
    {
        if (preg_match('/^([^@\s]+@[^:\s]+):/', trim($sshUrl), $m) === 1) {
            return $m[1];
        }

        return self::DEFAULT_SSH_HOST;
    }
}
