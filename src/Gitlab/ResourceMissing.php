<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * A domain-level miss: the API call succeeded, but the collection it returned
 * does not contain the item the caller asked for.
 *
 * `upkeep notes --since-tag=9.9.9` against a project whose tag list came back
 * HTTP 200 without that tag is this condition — no HTTP failure happened, so
 * `status` stays null and the message never claims a 404. The browser URL
 * points at the collection that was searched.
 */
final readonly class ResourceMissing extends ApiFailure
{
    /**
     * @param string $resource what was looked for, e.g. 'tag "9.9.9"'
     * @param string $searched what was searched, e.g. 'the tags of project/foo'
     */
    public function __construct(
        public string $resource,
        string $searched,
        string $browserUrl,
    ) {
        parent::__construct(
            sprintf('No %s in %s. Check in the browser: %s', $resource, $searched, $browserUrl),
            null,
            $browserUrl,
        );
    }

    public function shortCode(): string
    {
        return 'missing';
    }

    public function isTransient(): bool
    {
        return false;
    }
}
