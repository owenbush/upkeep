<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The git.drupalcode.org remote, in its two forms.
 *
 * Cloning happens over HTTPS because anonymous read needs no credentials and
 * every public contrib project answers it. Pushing over the same URL does not
 * work: git falls back to HTTP basic auth, prompts for a username and
 * password at a terminal that may not be attended, and GitLab then refuses the
 * password outright because it requires a token.
 *
 * So the push URL alone is moved to SSH, leaving fetch anonymous. That split
 * is the point:
 *
 *   - **upkeep never handles a credential for git.** The alternative — feeding
 *     the PAT to git — has no implementation that does not write the token to
 *     disk (`.git/config`, a credential store) or expose it in process argv
 *     where `ps` can read it. Every one of those would be a second exception
 *     to the rule that `UPKEEP_GITLAB_TOKEN` never reaches a child process.
 *     SSH means the operator's agent answers, and upkeep never sees a secret.
 *   - **Fetch keeps working with no key at all.** Checking a merge request,
 *     applying a patch, and running the suite are read-only, and a maintainer
 *     without an SSH key can still do all of it.
 *
 * The SSH form is derived from whatever origin already is, never assembled
 * from a project name, so a remote somebody set deliberately — a fork, a
 * mirror, an already-SSH clone — is recognised as not-ours and left alone.
 */
final readonly class DrupalCodeRemote
{
    public const HTTPS_BASE = 'https://git.drupalcode.org/';

    private const SSH_BASE = 'git@git.drupalcode.org:';

    /** Where drupal.org takes SSH keys, named in the failure that needs it. */
    public const SSH_KEY_URL = 'https://git.drupalcode.org/-/user_settings/ssh_keys';

    public static function httpsUrl(string $project): string
    {
        return self::HTTPS_BASE . $project . '.git';
    }

    /**
     * The SSH push URL for a git.drupalcode.org HTTPS remote, or null when the
     * URL is not one — already SSH, a different host, or empty.
     *
     * Null means "leave this remote alone", which is why it is null rather
     * than a best-effort rewrite: guessing at a remote somebody chose is how a
     * push ends up somewhere they did not intend.
     */
    public static function sshPushUrl(string $remoteUrl): ?string
    {
        $url = trim($remoteUrl);
        if (!str_starts_with($url, self::HTTPS_BASE)) {
            return null;
        }

        $path = substr($url, \strlen(self::HTTPS_BASE));
        if ($path === '' || $path === '.git') {
            return null;
        }

        return self::SSH_BASE . $path;
    }
}
