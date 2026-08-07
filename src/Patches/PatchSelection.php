<?php

declare(strict_types=1);

namespace Upkeep\Patches;

use Upkeep\Drupal\IssueFile;

/**
 * What naming a patch on the command line resolved to.
 *
 * Three outcomes rather than two, because "several patches and you did not say
 * which" is not an error and not an answer — it is a question for the operator,
 * and only the command knows whether there is anyone there to ask.
 */
final readonly class PatchSelection
{
    /**
     * @param IssueFile|null  $chosen     the settled patch, when one was settled
     * @param list<IssueFile> $candidates every patch on the issue, newest first
     * @param string|null     $problem    why nothing could be settled
     */
    private function __construct(
        public ?IssueFile $chosen,
        public array $candidates,
        public ?string $problem,
        public bool $ambiguous,
    ) {
    }

    /** @param list<IssueFile> $candidates */
    public static function settled(IssueFile $chosen, array $candidates = []): self
    {
        return new self($chosen, $candidates, null, false);
    }

    /** @param list<IssueFile> $candidates */
    public static function ambiguous(array $candidates): self
    {
        return new self(null, $candidates, null, true);
    }

    public static function problem(string $problem): self
    {
        return new self(null, [], $problem, false);
    }
}
