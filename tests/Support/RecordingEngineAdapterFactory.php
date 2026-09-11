<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\ProjectsRoot;
use Upkeep\Cockpit\Cockpit;

/**
 * An engine factory that resolves the projects root the way the production
 * factory does, records the answer, and then hands back a fake engine.
 *
 * This is the seam the projects-root precedence tests observe. Resolution
 * deliberately goes through the real ProjectsRoot: what is under test is the
 * whole path from `--projects-root` on the command line to the directory the
 * engine would be given, and a factory that recorded only the raw option
 * would assert that the command passed a string along, not that the
 * documented precedence holds.
 */
final class RecordingEngineAdapterFactory implements EngineAdapterFactory
{
    /** The canonical projects root the last invocation resolved. */
    public ?string $projectsRoot = null;

    /** The cockpit root the last invocation resolved. */
    public ?string $cockpitRoot = null;

    public function __construct(private readonly ?EngineAdapterInterface $adapter = null)
    {
    }

    public function create(
        Cockpit $cockpit,
        ?string $projectsRootOption,
        \Closure $stageLog,
        \Closure $processLog,
        ?\Closure $processIdle = null,
    ): EngineAdapterInterface {
        $this->cockpitRoot = $cockpit->root;
        $this->projectsRoot = ProjectsRoot::resolve($projectsRootOption, $cockpit->root);

        return $this->adapter ?? FakeEngineAdapter::withEnvPath($this->projectsRoot);
    }
}
