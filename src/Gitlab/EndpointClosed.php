<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The instance refused the request with HTTP 403 — and only 403.
 *
 * On git.drupalcode.org's block-by-default API this is an expected condition,
 * not an error: the endpoint may simply not be opened for PAT use. The browser
 * URL is the maintainer's documented fallback for performing the action by
 * hand. A rejected credential is HTTP 401 and is Unauthorized instead, because
 * "use the browser" would be the wrong advice there.
 */
final readonly class EndpointClosed extends ApiFailure
{
    public function __construct(string $browserUrl)
    {
        parent::__construct(
            sprintf('Endpoint closed to API access (HTTP 403). Use the browser instead: %s', $browserUrl),
            403,
            $browserUrl,
        );
    }

    public function shortCode(): string
    {
        return '403';
    }

    /** Instance policy, not a blip: stable for the whole run. */
    public function isTransient(): bool
    {
        return false;
    }
}
