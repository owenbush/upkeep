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
        /**
         * The SHA of the merge request's `/merge` ref — the branch merged into
         * the current tip of its target.
         *
         * What a check of this merge request is actually about, since that is
         * the tree the adapter checks out and the tree CI analyses. Null when
         * GitLab publishes no merge ref (the merge request conflicts with its
         * target), which is the same case the adapter falls back to the branch
         * on. See Gitlab\MergeRevision.
         */
        public ?string $mergeRefSha = null,
    ) {
    }
}
