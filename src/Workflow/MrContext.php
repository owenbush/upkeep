<?php

declare(strict_types=1);

namespace Upkeep\Workflow;

use Upkeep\Cockpit\Module;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;

/**
 * Everything the single-MR commands (check, review) need to act: the
 * registered module, its GitLab project, the resolved open merge request,
 * and the core major version the run targets.
 */
final readonly class MrContext
{
    public function __construct(
        public Module $module,
        public Project $project,
        public MergeRequest $mergeRequest,
        public string $coreMajor,
    ) {
    }
}
