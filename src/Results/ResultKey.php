<?php

declare(strict_types=1);

namespace Upkeep\Results;

/**
 * What a cached check result is *about*: a merge request, or a patch on an
 * issue.
 *
 * The two live in one store and must never collide. Both are identified by a
 * number, and nothing separates the ranges — a module could plausibly have
 * merge request !3597808 while an issue carries node id 3597808 — so the
 * distinction is made structurally, in the path segment, rather than left to
 * the hope that the numbers stay apart. A patch result read as an MR result
 * would put patch evidence in front of the fast-lane gate, which is the one
 * thing that must not happen.
 */
final readonly class ResultKey
{
    private function __construct(
        public string $segment,
        public bool $isPatch,
        public int $number,
    ) {
    }

    public static function mergeRequest(int $iid): self
    {
        return new self((string) self::assertPositive($iid, 'merge request IID'), false, $iid);
    }

    public static function patch(int $issueNid): self
    {
        return new self('patch-' . self::assertPositive($issueNid, 'issue node id'), true, $issueNid);
    }

    private static function assertPositive(int $value, string $what): int
    {
        if ($value < 1) {
            throw new \InvalidArgumentException(sprintf(
                'A cached result is keyed by a positive %s, got %d.',
                $what,
                $value,
            ));
        }

        return $value;
    }
}
