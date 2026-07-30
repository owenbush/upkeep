<?php

declare(strict_types=1);

namespace Upkeep\Results;

use Upkeep\Adapter\CheckRunResult;

/**
 * One cached local-check outcome for a (module, MR, core) at a specific MR
 * head SHA. The SHA is part of the identity: a result recorded against an
 * older head is stale evidence, and consumers (dashboard LOCAL column, the
 * fast-lane gate) must compare it against the MR's current head before
 * trusting it.
 */
final readonly class CachedResult
{
    public function __construct(
        public string $sha,
        public \DateTimeImmutable $recordedAt,
        public CheckRunResult $result,
    ) {
    }
}
