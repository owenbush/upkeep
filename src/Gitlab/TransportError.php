<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The request never produced a usable HTTP response (network failure, TLS
 * error, undecodable body) or the server rejected it with a status the client
 * has no more specific type for. The message never contains credentials.
 */
final readonly class TransportError extends ApiFailure
{
    public function __construct(
        string $message,
        public ?int $status = null,
    ) {
        parent::__construct($message);
    }
}
