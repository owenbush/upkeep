<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * A patch file as the adapter consumes it: which issue it belongs to, what it
 * is called, and where it has already been downloaded to on this machine.
 *
 * The adapter never fetches. By the time a patch reaches this boundary it is a
 * local file that something else has already retrieved and verified — the same
 * division as everywhere else here, where the adapter owns engine mechanics
 * and nothing more.
 */
final readonly class PatchApplication
{
    public function __construct(
        public int $issueNid,
        public string $name,
        public string $localPath,
        /**
         * The branch this patch was generated against, when it is known.
         *
         * An issue is filed against a version and its patches are cut from
         * that branch. Without this the adapter used whatever the working copy
         * sat on — the clone's default — so a 2.0.x patch was applied to 1.0.x
         * and reported as needing a re-roll. Null means "nobody could tell",
         * and the adapter falls back to resolving from the working copy.
         */
        public ?string $baseBranch = null,
    ) {
    }

    /** The local branch this patch is applied on. */
    public function branchName(): string
    {
        return ManagedBranch::forPatch($this->issueNid);
    }
}
