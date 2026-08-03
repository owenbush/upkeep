<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * A response arrived with a non-error status, but its body is not usable as
 * the JSON array/object the endpoint is documented to return.
 *
 * Typically a captive-portal or gateway interstitial served as HTML with an
 * HTTP 200, a truncated body, or a bare JSON scalar. Also raised when a
 * paginated endpoint keeps returning data that is not a list — the condition
 * that would otherwise make pagination loop forever.
 *
 * Distinct from TransportError (no response at all) and from RequestRejected
 * (a response whose status already says it failed).
 */
final readonly class MalformedResponse extends ApiFailure
{
    public function __construct(string $message, ?int $status = null, ?string $browserUrl = null)
    {
        parent::__construct($message, $status, $browserUrl);
    }

    public function shortCode(): string
    {
        return 'malformed';
    }

    /** Usually an interstitial or a truncated read; worth another attempt. */
    public function isTransient(): bool
    {
        return true;
    }
}
