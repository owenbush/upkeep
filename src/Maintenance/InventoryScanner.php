<?php

declare(strict_types=1);

namespace Upkeep\Maintenance;

use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;
use Upkeep\Adapter\EnvironmentMeta;
use Upkeep\Adapter\SnapshotLayout;
use Upkeep\BaseArtifact\ArtifactLayout;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Filesystem\FilesystemException;

/**
 * Walks the cockpit and the projects root and produces the typed disk
 * inventory `status --disk` renders and `prune` selects from.
 *
 * Sources:
 *   - <cockpit>/base-artifacts/<major>/           one BaseArtifact item each
 *   - <cockpit>/fixtures/*.sql.gz                 FixtureDump items (library)
 *   - <projects-root>/upkeep-*-d<major>/          one ProjectTree item each,
 *       attributed via its .upkeep-env.yml dotfile (read leniently: a partial
 *       provision without the completion marker is still inventoried, with
 *       unknown age), plus per project:
 *         SnapshotLayout::MATERIALIZED_DIR/*.sql  Snapshot items (age from the
 *             sidecar <name>.meta materialized_at in SnapshotLayout::META_DIR)
 *         module/tests/fixtures/*.sql.gz          FixtureDump items (committed)
 *
 * Keep markers: `<project>/.keep` marks a whole environment kept;
 * `<artifact>.keep` (e.g. materialized/<name>.sql.keep) marks one snapshot.
 *
 * Docker volumes are NOT scanned here — they come from VolumeProbe, since
 * they only exist engine-side.
 *
 * An unreadable directory is deliberately NOT the same thing as an empty one.
 * For prune, under-reporting fails safe (nothing is over-deleted), so a scan
 * never aborts on one — but it records a warning naming the directory, so the
 * operator can tell "there is nothing here" from "I could not look".
 */
final class InventoryScanner
{
    public const KEEP_MARKER = '.keep';

    private const PROJECT_DIR_PATTERN = '/^upkeep-[a-z0-9-]+-d\d+$/';
    private const MODULE_FIXTURES_DIR = 'module/tests/fixtures';

    /**
     * Bytes-on-disk measure for one path, defaulting to `du -sk`.
     *
     * @var \Closure(string): int
     */
    private readonly \Closure $sizer;

    /** @var list<string> */
    private array $warnings = [];

    /**
     * @param (\Closure(string): int)|null $sizer
     */
    public function __construct(
        private readonly Cockpit $cockpit,
        private readonly string $projectsRoot,
        ?\Closure $sizer = null,
    ) {
        $this->sizer = $sizer ?? DiskUsage::bytes(...);
    }

    /**
     * @return list<InventoryItem>
     */
    public function scan(): array
    {
        $this->warnings = [];

        return [
            ...$this->baseArtifacts(),
            ...$this->libraryDumps(),
            ...$this->projects(),
        ];
    }

    /**
     * Directories the last scan could not read, and so may have under-reported.
     *
     * @return list<string>
     */
    public function warnings(): array
    {
        return $this->warnings;
    }

    /**
     * The path prefixes nothing may ever be pruned from, for this cockpit.
     *
     * @return list<string>
     */
    public function protectedRoots(): array
    {
        return [$this->cockpit->baseArtifactsPath(), $this->cockpit->fixturesPath()];
    }

    /**
     * @return list<InventoryItem>
     */
    private function baseArtifacts(): array
    {
        $layout = new ArtifactLayout($this->cockpit->baseArtifactsPath());

        try {
            $versions = $layout->versionsOnDisk();
        } catch (FilesystemException $e) {
            // A prune must still be able to run over the rest of the disk.
            $this->warnings[] = $e->getMessage();

            return [];
        }

        $items = [];
        foreach ($versions as $version) {
            $path = $layout->versionDir($version);
            $items[] = new InventoryItem(
                path: $path,
                category: Category::BaseArtifact,
                sizeBytes: ($this->sizer)($path),
                coreMajor: $version,
            );
        }

        return $items;
    }

    /**
     * @return list<InventoryItem>
     */
    private function libraryDumps(): array
    {
        $items = [];
        foreach ($this->globIn($this->cockpit->fixturesPath(), '*.sql.gz') as $dump) {
            $items[] = new InventoryItem(
                path: $dump,
                category: Category::FixtureDump,
                sizeBytes: ($this->sizer)($dump),
            );
        }

        return $items;
    }

    /**
     * glob() over a directory, distinguishing "nothing matched" from "could
     * not read the directory" — glob() reports both as the empty list.
     *
     * @return list<string>
     */
    private function globIn(string $dir, string $pattern): array
    {
        if (is_dir($dir) && !is_readable($dir)) {
            $this->warnings[] = sprintf(
                'Directory "%s" is not readable — its contents are missing from this inventory.',
                $dir,
            );

            return [];
        }

        return glob(rtrim($dir, '/') . '/' . $pattern) ?: [];
    }

    /**
     * @return list<InventoryItem>
     */
    private function projects(): array
    {
        $root = rtrim($this->projectsRoot, '/');
        if (!is_dir($root)) {
            return [];
        }
        if (!is_readable($root)) {
            $this->warnings[] = sprintf(
                'Projects root "%s" is not readable — no environments are reported from it.',
                $root,
            );

            return [];
        }

        $items = [];
        foreach (scandir($root) ?: [] as $entry) {
            $projectPath = $root . '/' . $entry;
            if (preg_match(self::PROJECT_DIR_PATTERN, $entry) !== 1 || !is_dir($projectPath)) {
                continue;
            }
            array_push($items, ...$this->projectItems($entry, $projectPath));
        }

        return $items;
    }

    /**
     * @return list<InventoryItem>
     */
    private function projectItems(string $projectName, string $projectPath): array
    {
        [$module, $coreMajor, $lastUsedAt] = $this->readEnvMeta($projectPath);

        $snapshots = $this->snapshots($projectName, $projectPath, $module, $coreMajor);

        // The tree's size excludes its materialized snapshots so the
        // inventory total never double-counts bytes.
        $snapshotOverhead = ($this->sizer)($projectPath . '/' . SnapshotLayout::MATERIALIZED_DIR)
            + ($this->sizer)($projectPath . '/' . SnapshotLayout::META_DIR);

        $items = [new InventoryItem(
            path: $projectPath,
            category: Category::ProjectTree,
            sizeBytes: max(0, ($this->sizer)($projectPath) - $snapshotOverhead),
            module: $module,
            coreMajor: $coreMajor,
            projectName: $projectName,
            lastUsedAt: $lastUsedAt,
            keepMarked: is_file($projectPath . '/' . self::KEEP_MARKER),
        )];
        array_push($items, ...$snapshots);

        foreach ($this->globIn($projectPath . '/' . self::MODULE_FIXTURES_DIR, '*.sql.gz') as $dump) {
            $items[] = new InventoryItem(
                path: $dump,
                category: Category::FixtureDump,
                sizeBytes: ($this->sizer)($dump),
                module: $module,
                coreMajor: $coreMajor,
                projectName: $projectName,
            );
        }

        return $items;
    }

    /**
     * @return list<InventoryItem>
     */
    private function snapshots(string $projectName, string $projectPath, ?string $module, ?string $coreMajor): array
    {
        $materializedDir = $projectPath . '/' . SnapshotLayout::MATERIALIZED_DIR;

        // glob() resolves through symlinked path components, so a symlinked
        // materialized/ would enumerate files outside the environment — and
        // prune deletes what the inventory reports. Nothing outside the tree
        // is ever this environment's snapshot store.
        if (is_link($materializedDir)) {
            $this->warnings[] = sprintf(
                'Snapshot directory "%s" is a symlink — skipped: only files inside the environment tree are '
                . 'inventoried as its snapshots.',
                $materializedDir,
            );

            return [];
        }

        $items = [];
        foreach ($this->globIn($materializedDir, '*.sql') as $artifact) {
            $name = basename($artifact, '.sql');
            $items[] = new InventoryItem(
                path: $artifact,
                category: Category::Snapshot,
                sizeBytes: ($this->sizer)($artifact),
                module: $module,
                coreMajor: $coreMajor,
                projectName: $projectName,
                lastUsedAt: $this->snapshotMaterializedAt($projectPath, $name) ?? self::mtime($artifact),
                keepMarked: is_file($artifact . self::KEEP_MARKER),
            );
        }

        return $items;
    }

    /**
     * Lenient read of the environment meta dotfile: any missing or malformed
     * detail degrades to "unknown" attribution/age instead of failing the
     * whole inventory (a partial provision is still disk usage to report).
     *
     * @return array{?string, ?string, ?\DateTimeImmutable}
     */
    private function readEnvMeta(string $projectPath): array
    {
        $metaPath = $projectPath . '/' . EnvironmentMeta::FILENAME;
        if (!is_file($metaPath)) {
            return [null, null, null];
        }

        try {
            $data = Yaml::parse((string) file_get_contents($metaPath));
        } catch (ParseException) {
            return [null, null, null];
        }
        if (!is_array($data)) {
            return [null, null, null];
        }

        // Age prefers last_used_at (stamped on adapter reuse) over created_at.
        $lastUsed = self::timestamp($data['last_used_at'] ?? null) ?? self::timestamp($data['created_at'] ?? null);

        return [
            isset($data['module']) && is_scalar($data['module']) ? (string) $data['module'] : null,
            isset($data['core_major']) && is_scalar($data['core_major']) ? (string) $data['core_major'] : null,
            $lastUsed,
        ];
    }

    private function snapshotMaterializedAt(string $projectPath, string $name): ?\DateTimeImmutable
    {
        $metaPath = $projectPath . '/' . SnapshotLayout::META_DIR . '/' . $name . '.meta';
        if (
            !is_file($metaPath)
            || preg_match('/^materialized_at=(.+)$/m', (string) file_get_contents($metaPath), $m) !== 1
        ) {
            return null;
        }

        return self::timestamp(trim($m[1]));
    }

    private static function timestamp(mixed $value): ?\DateTimeImmutable
    {
        if (!is_scalar($value)) {
            return null;
        }

        try {
            return new \DateTimeImmutable((string) $value);
        } catch (\Exception) {
            return null;
        }
    }

    private static function mtime(string $path): ?\DateTimeImmutable
    {
        $mtime = @filemtime($path);

        return $mtime === false ? null : new \DateTimeImmutable('@' . $mtime);
    }
}
