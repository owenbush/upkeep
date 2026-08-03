<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The server answered with an error status the taxonomy has no more specific
 * type for — anything >= 400 that is not 401, 403, 404 or 429.
 *
 * In practice: a 400 with a validation complaint, the 405 GitLab returns when
 * a merge is refused because the MR is a draft, or a 5xx. The message quotes
 * only the body's own "message"/"error" field, never request headers, so no
 * credential can reach it.
 */
final readonly class RequestRejected extends ApiFailure
{
    public function __construct(int $status, ?string $detail, string $browserUrl)
    {
        parent::__construct(
            sprintf(
                'GitLab rejected the request (HTTP %d)%s. Browser fallback: %s',
                $status,
                $detail !== null && $detail !== '' ? ': ' . $detail : '',
                $browserUrl,
            ),
            $status,
            $browserUrl,
        );
    }

    public function shortCode(): string
    {
        return (string) $this->status;
    }

    /**
     * Treated as transient so a 5xx blip during a long run is retried rather
     * than memoized. A genuinely stable rejection (a 400) costs one extra
     * request per resource, which is cheaper than a wrong cached verdict.
     */
    public function isTransient(): bool
    {
        return true;
    }
}
