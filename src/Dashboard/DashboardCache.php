<?php

declare(strict_types=1);

namespace Upkeep\Dashboard;

/**
 * File-backed cache for dashboard remote state (GitLab MR listings,
 * drupal.org issue data). One JSON file per module under
 * `<cockpit>/cache/dashboard/`.
 *
 * Local check results and gate verdicts are NOT cached here — they are
 * always resolved fresh so the dashboard reflects the latest `check` runs.
 */
final readonly class DashboardCache
{
    public function __construct(private string $cacheDir)
    {
    }

    public function load(string $module): ?ModuleSnapshot
    {
        $path = $this->path($module);
        if (!is_file($path)) {
            return null;
        }

        $content = file_get_contents($path);
        if ($content === false) {
            return null;
        }

        return ModuleSnapshot::fromJson($content);
    }

    public function save(string $module, ModuleSnapshot $snapshot): void
    {
        if (!is_dir($this->cacheDir)) {
            mkdir($this->cacheDir, 0o755, true);
        }

        file_put_contents($this->path($module), $snapshot->toJson());
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
        return $this->cacheDir . '/' . $module . '.json';
    }
}
