<?php

declare(strict_types=1);

namespace Upkeep\Workflow;

use Upkeep\Adapter\PatchApplication;
use Upkeep\Cockpit\Module;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Patches\PatchRevision;

/**
 * Everything a patch command needs after resolution: which module and core
 * major it acts on, which issue the work belongs to, which of that issue's
 * patches was chosen, and where the file now sits on disk.
 *
 * The patch-side counterpart of MrContext. It carries the issue as well as the
 * file because the report is about a *contribution*, and a maintainer reading
 * "phpcs failed" needs to know which issue to go and say so on.
 */
final readonly class PatchContext
{
    public function __construct(
        public Module $module,
        public string $coreMajor,
        public Issue $issue,
        public IssueFile $patch,
        public string $localPath,
        /**
         * The branch the issue is filed against, resolved against the
         * project's real branches. Null when it could not be told, which the
         * adapter reads as "work it out from the working copy".
         */
        public ?string $baseBranch = null,
    ) {
    }

    public function application(): PatchApplication
    {
        return new PatchApplication(
            $this->issue->nid,
            $this->patch->name,
            $this->localPath,
            $this->baseBranch,
        );
    }

    /**
     * What identifies the patch that was checked, for the results cache — see
     * Patches\PatchRevision for why it is derived from the source URL rather
     * than the downloaded bytes.
     */
    public function revision(): string
    {
        return PatchRevision::of($this->patch->url);
    }
}
