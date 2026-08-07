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
    ) {
    }

    /** The local branch this patch is applied on. */
    public function branchName(): string
    {
        return ManagedBranch::forPatch($this->issueNid);
    }
}
