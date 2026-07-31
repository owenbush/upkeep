<?php

declare(strict_types=1);

namespace Upkeep\Config;

/**
 * The single shared definition of what a Project Update Bot merge request
 * looks like on git.drupalcode.org.
 *
 * Defaults are the phase-1 live-verified observations:
 * - author username "Project-Update-Bot" (user id 66574),
 * - source branch "project-update-bot-only",
 * - title "Automated Project Update Bot fixes" (optionally "Draft: "-prefixed).
 *
 * Consumers:
 * - The release-notes command (task 15) uses matchesAuthor() only, to group
 *   bot compatibility MRs under one heading.
 * - Task 12's dashboard/fast-lane gate must reuse this same value object —
 *   matches() (author AND source branch) is the gate's classification hook,
 *   so the pattern lives in exactly one place.
 */
final readonly class BotPattern
{
    public function __construct(
        public string $authorUsername = 'Project-Update-Bot',
        public int $authorId = 66574,
        public string $sourceBranch = 'project-update-bot-only',
        public string $title = 'Automated Project Update Bot fixes',
    ) {
    }

    /**
     * The bot pattern to apply when gating MRs for one target core version.
     *
     * The phase-1 verified pattern is core-independent (same account, same
     * "project-update-bot-only" branch for every core), so today every core
     * gets the defaults — but consumers (the fast-lane gate) must obtain
     * their pattern through here, keyed by target core, so a future
     * transition where the bot adopts per-core branches or accounts is a
     * change to this one method, not to gate logic.
     */
    public static function forCore(string $coreMajor): self
    {
        return new self();
    }

    /**
     * Author-only match: the MR was opened by the bot account, identified by
     * username or (rename-proof) by user id.
     */
    public function matchesAuthor(string $username, ?int $id): bool
    {
        return $username === $this->authorUsername
            || ($id !== null && $id === $this->authorId);
    }

    /**
     * Full pattern match for gate-style classification (task 12): the bot
     * author AND the bot's dedicated source branch. A push from any other
     * branch — even under the bot account — does not qualify.
     */
    public function matches(string $authorUsername, ?int $authorId, string $sourceBranch): bool
    {
        return $this->matchesAuthor($authorUsername, $authorId)
            && $sourceBranch === $this->sourceBranch;
    }
}
