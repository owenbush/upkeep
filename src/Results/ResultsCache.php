<?php

declare(strict_types=1);

namespace Upkeep\Results;

use Upkeep\Adapter\CheckResult;
use Upkeep\Adapter\CheckRunResult;
use Upkeep\Adapter\CheckStatus;
use Upkeep\Adapter\CheckType;

/**
 * File-backed store for local check results, shared by the check command
 * (writer) and the dashboard/gate (readers).
 *
 * Layout: <cockpit>/results/<module>/<mr-iid>/<core>/<head-sha>.json — one
 * file per checked head, so history survives re-checks and staleness is a
 * SHA comparison, not a timestamp guess.
 */
final readonly class ResultsCache
{
    /** Cached output is an excerpt for reporting, not a full log archive. */
    private const OUTPUT_EXCERPT_BYTES = 4000;

    public function __construct(private string $resultsDir)
    {
    }

    public function store(
        string $module,
        int $mrIid,
        string $coreMajor,
        string $sha,
        CheckRunResult $result,
        ?\DateTimeImmutable $recordedAt = null,
    ): void {
        $dir = $this->entryDir($module, $mrIid, $coreMajor);
        if (!is_dir($dir) && !mkdir($dir, 0o755, true) && !is_dir($dir)) {
            throw new \RuntimeException(sprintf('Cannot create results cache directory "%s".', $dir));
        }

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

        file_put_contents(
            $dir . '/' . $sha . '.json',
            json_encode($payload, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n",
        );
    }

    /** The result recorded for one exact head SHA, or null when never checked. */
    public function find(string $module, int $mrIid, string $coreMajor, string $sha): ?CachedResult
    {
        return $this->read($this->entryDir($module, $mrIid, $coreMajor) . '/' . $sha . '.json');
    }

    /**
     * The most recently recorded result for the (module, MR, core) regardless
     * of head SHA. Callers deciding freshness must compare its sha against
     * the MR's current head.
     */
    public function latest(string $module, int $mrIid, string $coreMajor): ?CachedResult
    {
        $files = glob($this->entryDir($module, $mrIid, $coreMajor) . '/*.json') ?: [];
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
        return $this->resultsDir . '/' . $module . '/' . $mrIid . '/' . $coreMajor;
    }

    /** Lenient by design: a malformed cache file is a miss, never a crash. */
    private function read(string $file): ?CachedResult
    {
        if (!is_file($file)) {
            return null;
        }

        try {
            /** @var array{sha?: string, recorded_at?: string, results?: list<array{type?: string, status?: string, exit_code?: int|null, output?: string, duration_seconds?: float}>} $data */
            $data = json_decode((string) file_get_contents($file), true, 32, JSON_THROW_ON_ERROR);
            if (!isset($data['sha'], $data['recorded_at'], $data['results']) || !is_array($data['results'])) {
                return null;
            }

            $checks = [];
            foreach ($data['results'] as $check) {
                $checks[] = new CheckResult(
                    CheckType::from($check['type'] ?? ''),
                    CheckStatus::from($check['status'] ?? ''),
                    $check['exit_code'] ?? null,
                    $check['output'] ?? '',
                    (float) ($check['duration_seconds'] ?? 0.0),
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
