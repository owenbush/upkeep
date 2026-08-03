<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

use Upkeep\Adapter\ProjectName;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\FileWriter;

/**
 * File-backed cache for dashboard remote state (GitLab MR listings,
 * drupal.org issue data). One JSON file per module under
 * `<cockpit>/cache/dashboard/`.
 *
 * Local check results and gate verdicts are NOT cached here — they are
 * always resolved fresh so the dashboard reflects the latest `check` runs.
 *
 * Files are owner-only: a snapshot serialises the complete upstream payloads
 * fetched with the maintainer's PAT, which for any limited-visibility project
 * is token-scoped data. Reads are lenient in the same documented way as
 * ResultsCache::read() — a truncated or malformed cache file is a miss the
 * dashboard re-fetches over, never an uncaught exception demanding a manual
 * `rm`.
 */
final readonly class DashboardCache
{
    public function __construct(private string $cacheDir)
    {
    }

    public function load(string $module): ?ModuleSnapshot
    {
        try {
            $path = $this->path($module);
        } catch (\InvalidArgumentException) {
            return null;
        }

        if (!is_file($path)) {
            return null;
        }

        $content = file_get_contents($path);
        if ($content === false) {
            return null;
        }

        try {
            return ModuleSnapshot::fromJson($content);
        } catch (\Throwable) {
            return null;
        }
    }

    /**
     * @throws \InvalidArgumentException when the module name is not a machine name
     * @throws FilesystemException when the snapshot cannot be written
     */
    public function save(string $module, ModuleSnapshot $snapshot): void
    {
        $path = $this->path($module);
        FileWriter::ensureDirectory($this->cacheDir, FileWriter::MODE_PRIVATE_DIR);
        FileWriter::write($path, $snapshot->toJson(), FileWriter::MODE_PRIVATE);
    }

    /**
     * Oldest fetch timestamp across all cached modules, or null when no
     * cache exists.
     *
     * @param list<string> $moduleNames
     */
    public function oldestFetchedAt(array $moduleNames): ?\DateTimeImmutable
    {
        $oldest = null;
        foreach ($moduleNames as $name) {
            $snapshot = $this->load($name);
            if ($snapshot === null) {
                continue;
            }
            if ($oldest === null || $snapshot->fetchedAt < $oldest) {
                $oldest = $snapshot->fetchedAt;
            }
        }

        return $oldest;
    }

    private function path(string $module): string
    {
        if (!ProjectName::isModuleName($module)) {
            throw new \InvalidArgumentException(sprintf(
                'The dashboard cache is keyed by module machine name ([a-z][a-z0-9_]*), got "%s".',
                $module,
            ));
        }

        return $this->cacheDir . '/' . $module . '.json';
    }
}
