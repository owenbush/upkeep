<?php

declare(strict_types=1);

namespace Upkeep\Ui\Jobs;

use Upkeep\Filesystem\FileWriter;

/**
 * Where a running job's record and captured output live.
 *
 * Layout: `<cockpit>/cache/ui/jobs/<id>/{meta.json,output.log}`. On disk rather
 * than in the server process, for three reasons that all matter: the work runs
 * in a *different* process, the page must survive a refresh, and a server
 * restart must not orphan a check that is still running in a container.
 *
 * Owner-only throughout. The captured output is raw check output — host paths,
 * stack traces, database diagnostics — the same material `Results\ResultsCache`
 * keeps at 0600 for the same reason.
 */
final class JobStore
{
    /** Reading the tail of a log the browser polls must not load a megabyte each time. */
    public const MAX_CHUNK_BYTES = 64 * 1024;

    /** @var \Closure(): string */
    private \Closure $clock;

    /**
     * @param ?\Closure(): string $clock finish timestamps; injected so a test
     *                                   can assert on one
     */
    public function __construct(
        private string $jobsDir,
        ?\Closure $clock = null,
    ) {
        $this->clock = $clock ?? static fn (): string => (new \DateTimeImmutable())->format(\DateTimeInterface::ATOM);
    }

    /**
     * A job id: unguessable, and a safe path segment by construction rather
     * than by validation — nothing derived from a request ever becomes one.
     */
    public static function newId(): string
    {
        return bin2hex(random_bytes(8));
    }

    public function create(Job $job): void
    {
        $dir = $this->dir($job->id);
        FileWriter::ensureDirectory($dir, FileWriter::MODE_PRIVATE_DIR);
        FileWriter::write($dir . '/output.log', '', FileWriter::MODE_PRIVATE);
        $this->save($job);
    }

    public function save(Job $job): void
    {
        FileWriter::ensureDirectory($this->dir($job->id), FileWriter::MODE_PRIVATE_DIR);
        FileWriter::write(
            $this->dir($job->id) . '/meta.json',
            json_encode($job->toArray(), \JSON_THROW_ON_ERROR | \JSON_PRETTY_PRINT) . "\n",
            FileWriter::MODE_PRIVATE,
        );
    }

    public function find(string $id): ?Job
    {
        if (!self::isId($id)) {
            return null;
        }

        $file = $this->dir($id) . '/meta.json';
        if (!is_file($file)) {
            return null;
        }

        try {
            $data = json_decode((string) file_get_contents($file), true, 16, \JSON_THROW_ON_ERROR);
        } catch (\Throwable) {
            return null;
        }

        $job = \is_array($data) ? Job::fromArray($data) : null;

        return $job === null ? null : $this->reconciled($job);
    }

    /**
     * A job whose recorded state is caught up with what is on disk.
     *
     * Nothing watches a running job: the process that started it returned long
     * ago, and the server may have been restarted since. So "has it finished?"
     * is answered by the sentinel file the job's own shell wrapper writes, not
     * by any live handle — and the answer is written back so it is only read
     * once.
     */
    private function reconciled(Job $job): Job
    {
        if ($job->isFinished()) {
            return $job;
        }

        $exitFile = $this->exitPath($job->id);
        if ($exitFile === null || !is_file($exitFile)) {
            return $job;
        }

        $raw = trim((string) @file_get_contents($exitFile));
        if (preg_match('/^\d{1,3}$/', $raw) !== 1) {
            return $job;
        }

        $exitCode = (int) $raw;
        $finished = new Job(
            $job->id,
            $job->label,
            $job->argv,
            Job::stateFor($exitCode),
            $exitCode,
            $job->startedAt,
            ($this->clock)(),
        );
        $this->save($finished);

        return $finished;
    }

    public function exitPath(string $id): ?string
    {
        return self::isId($id) ? $this->dir($id) . '/exit' : null;
    }

    /**
     * Every job, newest first. A UI listing, not an archive index: jobs are
     * disposable and `prune` may remove them.
     *
     * @return list<Job>
     */
    public function all(): array
    {
        $jobs = [];
        foreach (glob($this->jobsDir . '/*/meta.json') ?: [] as $file) {
            $id = basename(\dirname($file));
            $job = $this->find($id);
            if ($job !== null) {
                $jobs[] = $job;
            }
        }

        usort($jobs, static fn (Job $a, Job $b): int => $b->startedAt <=> $a->startedAt);

        return $jobs;
    }

    public function logPath(string $id): ?string
    {
        return self::isId($id) ? $this->dir($id) . '/output.log' : null;
    }

    /**
     * The bytes after $offset, and where the caller should resume.
     *
     * Offset-based rather than line-based on purpose: it is the one thing a
     * browser can hold across a poll, a refresh, or a reconnect without the
     * server remembering anything about that client. Capped per read so a
     * long check cannot make each poll heavier than the last.
     *
     * @return array{output: string, offset: int, complete: bool}
     */
    public function readFrom(string $id, int $offset): array
    {
        $path = $this->logPath($id);
        if ($path === null || !is_file($path)) {
            return ['output' => '', 'offset' => $offset, 'complete' => true];
        }

        $size = (int) @filesize($path);
        $from = max(0, min($offset, $size));
        $length = min(self::MAX_CHUNK_BYTES, $size - $from);
        if ($length <= 0) {
            return ['output' => '', 'offset' => $from, 'complete' => true];
        }

        $chunk = @file_get_contents($path, false, null, $from, $length);
        $chunk = $chunk === false ? '' : $chunk;

        return [
            'output' => $chunk,
            'offset' => $from + \strlen($chunk),
            'complete' => $from + \strlen($chunk) >= $size,
        ];
    }

    /** Ids are minted here, so anything not of that shape came from elsewhere. */
    public static function isId(string $id): bool
    {
        return preg_match('/^[0-9a-f]{16}$/', $id) === 1;
    }

    private function dir(string $id): string
    {
        return $this->jobsDir . '/' . $id;
    }
}
