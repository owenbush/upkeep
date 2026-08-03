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
        ?string $browserUrl = null,
    ) {
        parent::__construct(
            $retryAfterSeconds === null
                ? 'Rate limited by the GitLab instance (HTTP 429). Please retry later.'
                : sprintf(
                    'Rate limited by the GitLab instance (HTTP 429). Please retry in %d seconds.',
                    $retryAfterSeconds,
                ),
            429,
            $browserUrl,
        );
    }

    public function shortCode(): string
    {
        return 'rate-limited';
    }

    /** Transient by definition — never memoized for the rest of the run. */
    public function isTransient(): bool
    {
        return true;
    }
}
