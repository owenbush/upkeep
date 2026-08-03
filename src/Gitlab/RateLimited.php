<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The instance answered HTTP 429. Back off and retry later; if a Retry-After
 * header was present its value (seconds) is carried here.
 */
final readonly class RateLimited extends ApiFailure
{
    public function __construct(
        public ?int $retryAfterSeconds = null,
    ) {
        parent::__construct(
            $retryAfterSeconds === null
                ? 'Rate limited by the GitLab instance (HTTP 429). Please retry later.'
                : sprintf(
                    'Rate limited by the GitLab instance (HTTP 429). Please retry in %d seconds.',
                    $retryAfterSeconds,
                ),
        );
    }
}
