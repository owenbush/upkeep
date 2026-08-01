<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Upkeep\Maintenance\Category;
use Upkeep\Maintenance\InventoryItem;

/**
 * Best-effort probe for the engine projects' docker named volumes: names via
 * `docker volume ls` (verified live: the engine's compose-managed volumes
 * carry com.docker.compose.project=ddev-<projectname>), sizes via
 * `docker system df -v`. Only volumes whose label maps to an inventoried
 * project tree are reported — other projects' volumes are none of our
 * business. When docker is unavailable the probe yields nothing; volume
 * reclamation itself is always the adapter teardown's job, never this
 * class's.
 */
final readonly class VolumeProbe
{
    private const PROJECT_LABEL = 'com.docker.compose.project';
    private const COMPOSE_PROJECT_PREFIX = 'ddev-';

    /**
     * @param \Closure(list<string>): ?string $exec runs a command, returns stdout or null on failure
     */
    public function __construct(private \Closure $exec)
    {
    }

    public static function withRunner(ProcessRunner $runner): self
    {
        return new self(static fn (array $command): ?string => $runner->tryRun($command, timeout: 120));
    }

    /**
     * @param array<string, InventoryItem> $treesByProjectName inventoried ProjectTree items
     *
     * @return list<InventoryItem> one ProjectVolume item per matching volume
     */
    public function items(array $treesByProjectName): array
    {
        $listing = ($this->exec)(['docker', 'volume', 'ls', '--format', sprintf('{{.Name}}\t{{.Label "%s"}}', self::PROJECT_LABEL)]);
        if ($listing === null) {
            return [];
        }

        $volumeProjects = array_intersect(self::parseVolumeList($listing), array_keys($treesByProjectName));
        if ($volumeProjects === []) {
            return [];
        }

        $sizes = self::parseVolumeSizes(($this->exec)(['docker', 'system', 'df', '-v']) ?? '');

        $items = [];
        foreach ($volumeProjects as $volumeName => $projectName) {
            $tree = $treesByProjectName[$projectName];
            $items[] = new InventoryItem(
                path: $volumeName,
                category: Category::ProjectVolume,
                sizeBytes: $sizes[$volumeName] ?? 0,
                module: $tree->module,
                coreMajor: $tree->coreMajor,
                projectName: $projectName,
                lastUsedAt: $tree->lastUsedAt,
            );
        }

        return $items;
    }

    /**
     * Parses `docker volume ls --format '{{.Name}}\t{{.Label ...}}'` output.
     * Only volumes whose compose-project label carries the engine's prefix
     * are engine volumes; everything else is omitted.
     *
     * @return array<string, string> volume name => engine project name
     */
    public static function parseVolumeList(string $output): array
    {
        $volumes = [];
        foreach (explode("\n", trim($output)) as $line) {
            $parts = explode("\t", $line);
            if (\count($parts) === 2 && $parts[0] !== '' && str_starts_with($parts[1], self::COMPOSE_PROJECT_PREFIX)) {
                $volumes[$parts[0]] = substr($parts[1], \strlen(self::COMPOSE_PROJECT_PREFIX));
            }
        }

        return $volumes;
    }

    /**
     * Parses the "Local Volumes space usage" section of `docker system df -v`
     * (columns: VOLUME NAME, LINKS, SIZE; sizes in docker's SI notation).
     *
     * @return array<string, int> volume name => size in bytes
     */
    public static function parseVolumeSizes(string $output): array
    {
        $sizes = [];
        $inVolumeSection = false;
        foreach (explode("\n", $output) as $line) {
            if (str_starts_with($line, 'Local Volumes space usage')) {
                $inVolumeSection = true;
                continue;
            }
            if (!$inVolumeSection) {
                continue;
            }
            if (preg_match('/^(\S+)\s+\d+\s+([\d.]+)\s*([kKMGT]?B)\s*$/', trim($line), $m) === 1) {
                $sizes[$m[1]] = self::siToBytes((float) $m[2], $m[3]);
            } elseif (trim($line) !== '' && !str_starts_with(trim($line), 'VOLUME NAME')) {
                // A non-matching non-empty line ends the section (next header).
                $inVolumeSection = $sizes === [];
            }
        }

        return $sizes;
    }

    private static function siToBytes(float $value, string $unit): int
    {
        $multiplier = match (strtoupper($unit)) {
            'B' => 1,
            'KB' => 1000,
            'MB' => 1000 ** 2,
            'GB' => 1000 ** 3,
            'TB' => 1000 ** 4,
            default => 1,
        };

        return (int) round($value * $multiplier);
    }
}
