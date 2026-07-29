<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The instance refused the request (HTTP 401/403). On git.drupalcode.org's
 * block-by-default API this is an expected condition, not an error: the
 * endpoint may simply not be opened for PAT use. Carries the HTTP status and
 * a browser URL the maintainer can open to perform the action by hand.
 */
final readonly class EndpointClosed extends ApiFailure
{
    public function __construct(
        public int $status,
        public string $browserUrl,
    ) {
        parent::__construct(sprintf(
            'Endpoint closed to API access (HTTP %d). Use the browser instead: %s',
            $status,
            $browserUrl,
        ));
    }
}
