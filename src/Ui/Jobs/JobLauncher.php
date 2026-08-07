<?php

declare(strict_types=1);

namespace Upkeep\Ui\Jobs;

use Upkeep\Adapter\ShellArgument;

/**
 * Starts a job and gets out of its way.
 *
 * The child is wrapped in a shell that redirects its output to the job's log
 * and, when it finishes, writes its exit status to a sentinel file:
 *
 *     upkeep … >>output.log 2>&1; printf %s $? >exit
 *
 * That sentinel is what makes the job survive its parent. A check takes
 * minutes; the HTTP request that started it returns in milliseconds, the
 * server may serve later polls from a different worker, and the operator may
 * restart the whole UI in between. Nothing needs to still be watching — the
 * status is on disk where JobStore reconciles it.
 */
final readonly class JobLauncher
{
    /**
     * @param \Closure(string): void $spawn detaches a shell command line;
     *                                      injected so tests never fork
     * @param \Closure(): string     $clock start timestamps
     */
    public function __construct(
        private JobStore $store,
        private string $binary,
        private string $cockpit,
        private \Closure $spawn,
        private \Closure $clock,
    ) {
    }

    public function launch(JobAction $action): Job
    {
        $id = JobStore::newId();
        $argv = array_merge($action->argv, ['--cockpit=' . $this->cockpit]);

        $job = new Job(
            $id,
            $action->label,
            $argv,
            Job::RUNNING,
            null,
            (string) ($this->clock)(),
            null,
        );
        $this->store->create($job);

        ($this->spawn)($this->commandLine($id, $argv));

        return $job;
    }

    /**
     * @param list<string> $argv
     */
    private function commandLine(string $id, array $argv): string
    {
        $words = array_map(ShellArgument::quote(...), array_merge([$this->binary], $argv));

        return sprintf(
            '%s >>%s 2>&1; printf %%s $? >%s',
            implode(' ', $words),
            ShellArgument::quote($this->store->logPath($id) ?? '/dev/null'),
            ShellArgument::quote($this->store->exitPath($id) ?? '/dev/null'),
        );
    }
}
