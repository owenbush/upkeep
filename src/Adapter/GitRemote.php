<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * A git remote to push to: a name and a URL.
 *
 * It exists because "push the work branch" stopped being a question with one
 * answer. Contributing to Drupal does not put branches on the canonical
 * project — drupal.org mints an issue fork at `issue/<machine-name>-<nid>`,
 * the branch goes there, and the merge request is opened across projects into
 * the canonical repository. Measured on pathauto: 100 of 100 open merge
 * requests come from a fork, none from the project itself. So the destination
 * is decided by the command that knows about issues and forks, and handed to
 * the adapter, which only has to put a branch where it is told.
 *
 * Origin stays exactly as cloned: HTTPS, anonymous, read-only in practice.
 * Checking a merge request, applying a patch and running the suite need no
 * key and no account, and that should remain true however publishing works.
 */
final readonly class GitRemote
{
    private function __construct(
        public string $name,
        public string $url,
    ) {
    }

    /**
     * The remote for an issue fork, named after the issue rather than
     * something generic.
     *
     * A working copy is reused across issues — the same (module x core)
     * environment serves every one of them — so a single `fork` remote would
     * be silently re-pointed each time and a push could land on the fork for
     * whatever issue happened to be published last.
     *
     * @param string $sshUrl GitLab's own `ssh_url_to_repo`; see Gitlab\Project
     */
    public static function issueFork(int $issueNid, string $sshUrl): self
    {
        return new self('issue-' . $issueNid, $sshUrl);
    }
}
