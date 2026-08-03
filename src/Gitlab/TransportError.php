<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The request never produced a usable HTTP response at all: DNS failure, TLS
 * error, connection reset, timeout — anything symfony/http-client reports as a
 * contract exception rather than a status line.
 *
 * `status` is therefore always null: there was no response to take one from.
 * A response that arrived but carried an error status is RequestRejected, and
 * one that arrived with an undecodable body is MalformedResponse. The message
 * never contains credentials.
 */
final readonly class TransportError extends ApiFailure
{
    public function __construct(string $message, ?string $browserUrl = null)
    {
        parent::__construct($message, null, $browserUrl);
    }

    public function shortCode(): string
    {
        return 'transport';
    }

    /** A network condition may well have cleared by the next attempt. */
    public function isTransient(): bool
    {
        return true;
    }
}
