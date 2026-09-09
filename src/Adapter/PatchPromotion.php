<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * What promoting a patch onto a work branch actually achieved.
 *
 * Promotion used to be all or nothing: the patch applied and was committed, or
 * it did not and you were told to re-roll it. For a patch that has gone stale
 * — or one cut against a release tarball, which can never apply to a git
 * checkout at all — that is a dead end. The work of re-rolling is exactly the
 * work the failed apply was doing: put what still fits onto a branch, fix what
 * does not, commit, publish. So a partial promotion is a first-class outcome
 * rather than an error, and this says which it was.
 *
 * A partial promotion is deliberately *not* committed. The tree is left dirty
 * with `.rej` files beside the files that would not take, because a commit
 * carrying the patch author's attribution should say what the author wrote —
 * and half of it, plus rejects, is not that yet.
 */
final readonly class PatchPromotion
{
    /**
     * @param ?string      $sha      the commit carrying the patch, or null when hunks were rejected
     * @param list<string> $applied  files the patch changed successfully
     * @param list<string> $rejected files left with a `.rej` beside them
     */
    private function __construct(
        public ?string $sha,
        public array $applied,
        public array $rejected,
    ) {
    }

    /** @param list<string> $applied */
    public static function committed(string $sha, array $applied = []): self
    {
        return new self($sha, $applied, []);
    }

    /**
     * @param list<string> $applied
     * @param list<string> $rejected
     */
    public static function partial(array $applied, array $rejected): self
    {
        return new self(null, $applied, $rejected);
    }

    public function isComplete(): bool
    {
        return $this->sha !== null;
    }

    /**
     * The commit that carries the patch.
     *
     * @throws \LogicException when asked of a partial promotion, which has none
     */
    public function requireSha(): string
    {
        return $this->sha ?? throw new \LogicException('A partial promotion has no commit.');
    }
}
