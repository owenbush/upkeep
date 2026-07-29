<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * Base type for every non-success API outcome.
 *
 * GitlabClient methods never throw for HTTP-level outcomes; they return one of
 * the concrete subtypes (EndpointClosed, RateLimited, NotFound, TransportError)
 * so callers can pattern-match on the condition.
 */
abstract readonly class ApiFailure
{
    public function __construct(
        public string $message,
    ) {
    }
}
