<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\Cockpit\Cockpit;

/**
 * Builds the engine adapter for one command invocation.
 *
 * The adapter cannot be built once at startup: it needs the cockpit and the
 * projects root the invocation resolved, plus the log sinks that invocation's
 * output verbosity implies. This factory is that seam — commands receive it by
 * injection and never name, choose, or construct an engine implementation.
 *
 * The choice of engine is made in exactly one place, `bin/upkeep` (the
 * composition root), which is also the only place outside src/Adapter/ allowed
 * to mention a concrete engine.
 */
interface EngineAdapterFactory
{
    /**
     * @param ?string $projectsRootOption the raw --projects-root value, if any
     * @param \Closure(string): void $stageLog one line per orchestration stage
     * @param \Closure(string): void $processLog streamed child-process output
     * @param ?\Closure(): void $processIdle called as each child process exits, which is
     *                                       when a live status line must be cleared
     *
     * @throws AdapterException when the environment location cannot be resolved
     */
    public function create(
        Cockpit $cockpit,
        ?string $projectsRootOption,
        \Closure $stageLog,
        \Closure $processLog,
        ?\Closure $processIdle = null,
    ): EngineAdapterInterface;
}
