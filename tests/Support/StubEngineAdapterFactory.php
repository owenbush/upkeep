<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Cockpit\Cockpit;

/**
 * Hands the commands a pre-built adapter, bypassing every engine and
 * filesystem decision the real factory makes.
 */
final readonly class StubEngineAdapterFactory implements EngineAdapterFactory
{
    public function __construct(private EngineAdapterInterface $adapter)
    {
    }

    public function create(
        Cockpit $cockpit,
        ?string $projectsRootOption,
        \Closure $stageLog,
        \Closure $processLog,
    ): EngineAdapterInterface {
        return $this->adapter;
    }
}
