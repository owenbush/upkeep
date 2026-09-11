<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Security\SecretRedactor;

/**
 * The production EngineAdapterFactory: the ddev + ddev-drupal-contrib engine.
 *
 * This is the only class that assembles a DdevContribAdapter, and it lives
 * inside src/Adapter/ where engine specifics belong. It also owns the one
 * decision that used to be made five different ways in five command classes:
 * the redactor every child-process line is filtered through before it reaches
 * a log, an exception message, or a persisted result.
 */
final readonly class DdevContribAdapterFactory implements EngineAdapterFactory
{
    /**
     * @param ?SecretRedactor $redactor injected by the composition root; the
     *                                  ProcessRunner default is used when absent
     */
    public function __construct(private ?SecretRedactor $redactor = null)
    {
    }

    public function create(
        Cockpit $cockpit,
        ?string $projectsRootOption,
        \Closure $stageLog,
        \Closure $processLog,
        ?\Closure $processIdle = null,
    ): EngineAdapterInterface {
        return new DdevContribAdapter(
            new ArtifactLayout($cockpit->baseArtifactsPath()),
            ProjectsRoot::resolve($projectsRootOption, $cockpit->root),
            new ProcessRunner($processLog, $this->redactor, $processIdle),
            $stageLog,
        );
    }
}
