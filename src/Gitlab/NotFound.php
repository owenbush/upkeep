<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The server answered HTTP 404: the resource does not exist — or is hidden
 * from this PAT, which GitLab also reports as 404.
 *
 * Strictly an HTTP-level outcome. A resource the API returned successfully but
 * which simply does not contain the asked-for item (an unknown tag in a tag
 * list, say) is ResourceMissing, so this message's "HTTP 404" is always true.
 */
final readonly class NotFound extends ApiFailure
{
    public function __construct(string $browserUrl)
    {
        parent::__construct(
            sprintf('Resource not found (HTTP 404). Check in the browser: %s', $browserUrl),
            404,
            $browserUrl,
        );
    }

    public function shortCode(): string
    {
        return '404';
    }

    public function isTransient(): bool
    {
        return false;
    }
}
