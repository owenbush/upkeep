<?php

declare(strict_types=1);

namespace Upkeep\Results;

use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;
use Upkeep\Adapter\ProjectName;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\FileWriter;

/**
 * File-backed store for local check results, shared by the check command
 * (writer) and the dashboard/gate (readers).
 *
 * Layout: <cockpit>/results/<module>/<mr-iid>/<core>/<head-sha>.json — one
 * file per checked head, so history survives re-checks and staleness is a
 * SHA comparison, not a timestamp guess.
 *
 * Every component of that path is validated before it becomes a path segment.
 * The module name and core version come from the registry (validated at load,
 * re-asserted here because this is a public API), and the head SHA is an
 * unvalidated remote value from the GitLab API, so it is required to look like
 * a SHA before it becomes a filename.
 *
 * Files are owner-only: the payload embeds up to 4000 bytes of raw check
 * output per check — host paths, source fragments, stack traces, DB
 * diagnostics — which has no business being world-readable.
 */
final readonly class ResultsCache
{
    /** Cached output is an excerpt for reporting, not a full log archive. */
    private const OUTPUT_EXCERPT_BYTES = 4000;

    private const SHA_PATTERN = '/^[0-9a-f]{7,64}$/';

    public function __construct(private string $resultsDir)
    {
    }

    /**
     * @throws \InvalidArgumentException when a path component is not what it claims to be
     * @throws FilesystemException when the result file cannot be written
     */
    public function store(
        string $module,
        int $mrIid,
        string $coreMajor,
        string $sha,
        CheckRunResult $result,
        ?\DateTimeImmutable $recordedAt = null,
    ): void {
        $dir = $this->entryDir($module, $mrIid, $coreMajor);
        self::assertSha($sha);

        $payload = [
            'sha' => $sha,
            'recorded_at' => ($recordedAt ?? new \DateTimeImmutable())->format(\DateTimeInterface::ATOM),
            'results' => array_map(
                static fn (CheckResult $check): array => [
                    'type' => $check->type->value,
                    'status' => $check->status->value,
                    'exit_code' => $check->exitCode,
                    'output' => substr($check->output, 0, self::OUTPUT_EXCERPT_BYTES),
                    'duration_seconds' => $check->durationSeconds,
                ],
                $result->results,
            ),
        ];

        FileWriter::ensureDirectory($dir, FileWriter::MODE_PRIVATE_DIR);
        FileWriter::write(
            $dir . '/' . $sha . '.json',
            json_encode($payload, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n",
            FileWriter::MODE_PRIVATE,
        );
    }

    /** The result recorded for one exact head SHA, or null when never checked. */
    public function find(string $module, int $mrIid, string $coreMajor, string $sha): ?CachedResult
    {
        try {
            $dir = $this->entryDir($module, $mrIid, $coreMajor);
            self::assertSha($sha);
        } catch (\InvalidArgumentException) {
            // A read for an impossible identity is a miss, not a crash: the
            // caller only wants to know whether a result was recorded.
            return null;
        }

        return $this->read($dir . '/' . $sha . '.json');
    }

    /**
     * The most recently recorded result for the (module, MR, core) regardless
     * of head SHA. Callers deciding freshness must compare its sha against
     * the MR's current head.
     *
     * @throws FilesystemException when the entry directory exists but cannot be listed
     */
    public function latest(string $module, int $mrIid, string $coreMajor): ?CachedResult
    {
        try {
            $dir = $this->entryDir($module, $mrIid, $coreMajor);
        } catch (\InvalidArgumentException) {
            return null;
        }

        // An unreadable directory globs to the empty list, exactly like one
        // with no results in it. Distinguished explicitly: reporting it as
        // "never checked" would let the gate deny a merge for a reason that is
        // invisible to the operator.
        if (is_dir($dir) && !is_readable($dir)) {
            throw new FilesystemException(sprintf(
                'Cannot list the cached results in "%s" — the directory is not readable. Its results are being '
                . 'reported as absent, which is not the same as never checked.',
                $dir,
            ));
        }

        $files = glob($dir . '/*.json') ?: [];

        $newest = null;
        foreach ($files as $file) {
            $entry = $this->read($file);
            if ($entry !== null && ($newest === null || $entry->recordedAt > $newest->recordedAt)) {
                $newest = $entry;
            }
        }

        return $newest;
    }

    private function entryDir(string $module, int $mrIid, string $coreMajor): string
    {
        if (!ProjectName::isModuleName($module)) {
            throw new \InvalidArgumentException(sprintf(
                'Results are stored per module machine name ([a-z][a-z0-9_]*), got "%s".',
                $module,
            ));
        }
        if (!ProjectName::isCoreMajor($coreMajor)) {
            throw new \InvalidArgumentException(sprintf(
                'Results are stored per core major version number, got "%s".',
                $coreMajor,
            ));
        }

        return $this->resultsDir . '/' . $module . '/' . $mrIid . '/' . $coreMajor;
    }

    private static function assertSha(string $sha): void
    {
        if (preg_match(self::SHA_PATTERN, $sha) !== 1) {
            throw new \InvalidArgumentException(sprintf(
                'A cached result is keyed by its head SHA (7-64 hex characters), got "%s".',
                $sha,
            ));
        }
    }

    /** Lenient by design: a malformed cache file is a miss, never a crash. */
    private function read(string $file): ?CachedResult
    {
        if (!is_file($file)) {
            return null;
        }

        try {
            $data = json_decode((string) file_get_contents($file), true, 32, JSON_THROW_ON_ERROR);
            if (!is_array($data) || !is_string($data['sha'] ?? null) || !is_string($data['recorded_at'] ?? null)) {
                return null;
            }
            $results = $data['results'] ?? null;
            if (!is_array($results)) {
                return null;
            }

            $checks = [];
            foreach ($results as $check) {
                if (!is_array($check)) {
                    return null;
                }
                $checks[] = new CheckResult(
                    CheckType::from(is_string($check['type'] ?? null) ? $check['type'] : ''),
                    CheckStatus::from(is_string($check['status'] ?? null) ? $check['status'] : ''),
                    is_int($check['exit_code'] ?? null) ? $check['exit_code'] : null,
                    is_string($check['output'] ?? null) ? $check['output'] : '',
                    is_numeric($check['duration_seconds'] ?? null) ? (float) $check['duration_seconds'] : 0.0,
                );
            }

            return new CachedResult(
                $data['sha'],
                new \DateTimeImmutable($data['recorded_at']),
                new CheckRunResult($checks),
            );
        } catch (\Throwable) {
            return null;
        }
    }
}
