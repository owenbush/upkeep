<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The instance rejected the credential (HTTP 401): the PAT is missing,
 * invalid, expired, or lacks the scope the endpoint needs.
 *
 * Deliberately distinct from EndpointClosed (403). A 403 on this
 * block-by-default instance means "that endpoint is not open to the API, use
 * the browser" — advice that is actively misleading for a bad token, where the
 * only useful action is to re-check the credential. The advice therefore names
 * the sources a token is read from, and never any token material.
 */
final readonly class Unauthorized extends ApiFailure
{
    public function __construct(string $browserUrl)
    {
        parent::__construct(
            sprintf(
                'GitLab rejected the credential (HTTP 401): the token is missing, invalid, expired, or '
                . 'lacks the required scope. Re-check the token configured in: %s. '
                . '(The token is never printed or logged.)',
                TokenResolver::describeDefaultSources(),
            ),
            401,
            $browserUrl,
        );
    }

    public function shortCode(): string
    {
        return '401';
    }

    /** A bad token stays bad for the whole run. */
    public function isTransient(): bool
    {
        return false;
    }
}
