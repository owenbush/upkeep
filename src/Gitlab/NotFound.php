<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The resource does not exist (HTTP 404) — or is hidden from this PAT, which
 * GitLab also reports as 404. Carries the browser URL for manual inspection.
 */
final readonly class NotFound extends ApiFailure
{
    public function __construct(
        public string $browserUrl,
    ) {
        parent::__construct(sprintf(
            'Resource not found (HTTP 404). Check in the browser: %s',
            $browserUrl,
        ));
    }
}
