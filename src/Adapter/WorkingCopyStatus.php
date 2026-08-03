<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Snapshot of the git state of a module working copy: uncommitted changes,
 * untracked files, unpushed commits, and current branch. Used by guards
 * on destructive operations (ensureEnv stale teardown, prune, applyMr)
 * to refuse when local work would be lost.
 */
final readonly class WorkingCopyStatus
{
    public function __construct(
        public bool $hasStagedChanges,
        public bool $hasUnstagedChanges,
        public bool $hasUntrackedFiles,
        /** -1 when no upstream is configured. */
        public int $commitsAhead,
        /** null when HEAD is detached. */
        public ?string $currentBranch,
    ) {
    }

    /**
     * Hard dirty signals: uncommitted changes or untracked files that would
     * be destroyed by a checkout or teardown.
     */
    public function isDirty(): bool
    {
        return $this->hasStagedChanges
            || $this->hasUnstagedChanges
            || $this->hasUntrackedFiles;
    }

    /**
     * Whether the working copy is on a developer branch (not a base branch
     * like "1.0.x" or "2.x" and not an upkeep-managed "mr-*" branch).
     */
    public function isOnCustomBranch(): bool
    {
        if ($this->currentBranch === null) {
            return true;
        }

        if (preg_match('/^mr-\d+$/', $this->currentBranch) === 1) {
            return false;
        }

        if (
            preg_match('/^\d+\.\d+\.x$/', $this->currentBranch) === 1
            || preg_match('/^\d+\.x$/', $this->currentBranch) === 1
        ) {
            return false;
        }

        return true;
    }

    /**
     * Any signal that suggests local work: dirty files, unpushed commits,
     * or a developer branch.
     */
    public function hasLocalWork(): bool
    {
        return $this->isDirty()
            || $this->commitsAhead > 0
            || $this->commitsAhead === -1
            || $this->isOnCustomBranch();
    }

    /**
     * Human-readable list of reasons the working copy has local work.
     *
     * @return list<string>
     */
    public function describe(): array
    {
        $reasons = [];

        if ($this->hasStagedChanges) {
            $reasons[] = 'Staged changes not yet committed';
        }
        if ($this->hasUnstagedChanges) {
            $reasons[] = 'Unstaged changes to tracked files';
        }
        if ($this->hasUntrackedFiles) {
            $reasons[] = 'Untracked files not in .gitignore';
        }
        if ($this->commitsAhead > 0) {
            $reasons[] = sprintf('%d commit(s) ahead of origin (unpushed)', $this->commitsAhead);
        }
        if ($this->commitsAhead === -1) {
            $reasons[] = 'No upstream tracking branch configured';
        }
        if ($this->isOnCustomBranch()) {
            $reasons[] = $this->currentBranch === null
                ? 'HEAD is detached'
                : sprintf('On branch "%s" (not a base or MR branch)', $this->currentBranch);
        }

        return $reasons;
    }

    /**
     * Inspects the git state of a module working copy directory.
     */
    public static function inspect(string $moduleDir, CommandRunner $runner): self
    {
        $hasStagedChanges = false;
        $hasUnstagedChanges = false;
        $hasUntrackedFiles = false;
        $commitsAhead = 0;
        $currentBranch = null;

        $porcelain = $runner->tryRun(['git', '-C', $moduleDir, 'status', '--porcelain']);
        if ($porcelain !== null) {
            foreach (explode("\n", rtrim($porcelain, "\n")) as $line) {
                if ($line === '') {
                    continue;
                }
                $index = $line[0] ?? ' ';
                $worktree = $line[1] ?? ' ';

                if ($index === '?') {
                    $hasUntrackedFiles = true;
                } else {
                    if ($index !== ' ' && $index !== '?') {
                        $hasStagedChanges = true;
                    }
                    if ($worktree !== ' ' && $worktree !== '?') {
                        $hasUnstagedChanges = true;
                    }
                }
            }
        }

        $branchOutput = $runner->tryRun(['git', '-C', $moduleDir, 'symbolic-ref', '--short', 'HEAD']);
        if ($branchOutput !== null) {
            $currentBranch = trim($branchOutput);
        }

        $aheadOutput = $runner->tryRun(['git', '-C', $moduleDir, 'rev-list', '--count', '@{upstream}..HEAD']);
        if ($aheadOutput === null) {
            $commitsAhead = -1;
        } else {
            $commitsAhead = (int) trim($aheadOutput);
        }

        return new self(
            $hasStagedChanges,
            $hasUnstagedChanges,
            $hasUntrackedFiles,
            $commitsAhead,
            $currentBranch,
        );
    }
}
