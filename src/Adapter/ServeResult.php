<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Result of EngineAdapterInterface::serve(): where a human can open the
 * environment in a browser.
 */
final readonly class ServeResult
{
    public function __construct(
        /** The environment's primary URL (https://<project>.ddev.site form, engine-assigned). */
        public string $url,
        /** One-time authenticated login URL, when the engine can mint one; null otherwise. */
        public ?string $loginUrl,
    ) {
    }
}
