<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * @implements \IteratorAggregate<int, MergeRequest>
 */
final readonly class MergeRequestList implements \Countable, \IteratorAggregate
{
    /**
     * @param list<MergeRequest> $mergeRequests
     */
    public function __construct(
        private array $mergeRequests,
    ) {
    }

    public static function fromApi(array $items): self
    {
        return new self(array_map(MergeRequest::fromApi(...), array_values($items)));
    }

    /**
     * @return list<MergeRequest>
     */
    public function all(): array
    {
        return $this->mergeRequests;
    }

    public function first(): ?MergeRequest
    {
        return $this->mergeRequests[0] ?? null;
    }

    public function count(): int
    {
        return \count($this->mergeRequests);
    }

    public function getIterator(): \Traversable
    {
        return new \ArrayIterator($this->mergeRequests);
    }
}
