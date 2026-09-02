<?php

declare(strict_types=1);

namespace Upkeep\Patches;

use Upkeep\Drupal\DrupalUser;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Drupal\IssueReference;

/**
 * The commit message a promoted patch is carried into a merge request under.
 *
 * Promoting somebody's patch is the one operation in this tool that moves
 * another person's work under your own name. Nothing technical stops it and
 * nothing technical would notice; what stops it is that the commit says whose
 * work it is, in the place a reader of the branch will actually look.
 *
 * The subject follows drupal.org's own commit convention —
 * `Issue #NNN by author: Title` — which is both what maintainers already read
 * and, not incidentally, a string `Drupal\IssueReference::extractOwning()`
 * parses, so the resulting merge request pairs with its issue the same way
 * every other one does.
 *
 * **There is deliberately no `--author` and no `Co-authored-by:`.** Both want
 * an email address, and drupal.org's API publishes a username and a profile
 * URL and no address at all. Synthesising one would put a claim about somebody
 * else's identity into permanent history on the strength of a guess. A named
 * trailer says the true thing instead: here is the account, here is the file
 * it was posted as, here is where it came from. Credit on drupal.org is
 * allocated through the issue-credit system regardless, which is a browser
 * action on the issue and remains the promoter's to do.
 */
final readonly class PatchAttribution
{
    public function __construct(
        public int $issueNid,
        public string $issueTitle,
        public string $patchName,
        public string $patchUrl,
        /** Null when the file records no owner, or the account could not be read. */
        public ?DrupalUser $author,
        public ?string $promotedBy = null,
    ) {
    }

    public static function forPatch(Issue $issue, IssueFile $patch, ?DrupalUser $author, ?string $promotedBy): self
    {
        return new self($issue->nid, $issue->title, $patch->name, $patch->url, $author, $promotedBy);
    }

    /**
     * drupal.org's convention, and the reason the merge request needs no
     * separate title rule: "Issue #NNN by someone: Title".
     */
    public function subject(): string
    {
        $credit = $this->author !== null ? sprintf(' by %s', $this->author->name) : '';

        return sprintf('Issue #%d%s: %s', $this->issueNid, $credit, $this->issueTitle);
    }

    /** Subject, provenance, and the trailers that make it checkable. */
    public function message(): string
    {
        $lines = [$this->subject(), '', $this->provenance(), ''];

        if ($this->author !== null) {
            $lines[] = sprintf('Patch-author: %s <%s>', $this->author->name, $this->author->profileUrl);
        }
        $lines[] = sprintf('Patch-file: %s', $this->patchName);
        $lines[] = sprintf('Patch-source: %s', $this->patchUrl);
        $lines[] = sprintf('Issue: %s', IssueReference::issueUrl($this->issueNid));
        if ($this->promotedBy !== null) {
            $lines[] = sprintf('Promoted-by: %s', $this->promotedBy);
        }

        return implode("\n", $lines) . "\n";
    }

    /**
     * The paragraph a reviewer reads. It says the two things a promoted commit
     * has to say and that no amount of trailer parsing conveys: this is
     * somebody else's change, and the person who pushed it is not claiming it.
     */
    private function provenance(): string
    {
        if ($this->author === null) {
            return sprintf(
                "Applied from the patch \"%s\" posted on the issue, and promoted to a merge\n"
                . "request with `upkeep patch:promote`. The change is the patch author's work;\n"
                . 'drupal.org records no account for the file, so they are not named here — '
                . "credit\nthem on the issue.",
                $this->patchName,
            );
        }

        return sprintf(
            "Applied from the patch \"%s\", posted to the issue by %s, and\n"
            . "promoted to a merge request with `upkeep patch:promote`. The change is %s's\n"
            . 'work; whoever opens the merge request is carrying it over, not authoring it.',
            $this->patchName,
            $this->author->name,
            $this->author->name,
        );
    }
}
