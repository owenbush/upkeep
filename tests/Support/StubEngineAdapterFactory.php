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
final class StubEngineAdapterFactory implements EngineAdapterFactory
{
    /**
     * Lines to emit down each channel when a command builds its adapter, so a
     * test can assert *where* engine chatter lands rather than only that the
     * command ran. The distinction is the whole contract of the two closures:
     * the narrative belongs on screen, the engine's raw process output belongs
     * behind -v.
     */
    public function __construct(
        private readonly EngineAdapterInterface $adapter,
        private readonly ?string $stageLine = null,
        private readonly ?string $processLine = null,
    ) {
    }

    public function create(
        Cockpit $cockpit,
        ?string $projectsRootOption,
        \Closure $stageLog,
        \Closure $processLog,
    ): EngineAdapterInterface {
        if ($this->stageLine !== null) {
            $stageLog($this->stageLine);
        }
        if ($this->processLine !== null) {
            $processLog($this->processLine);
        }

        return $this->adapter;
    }
}
