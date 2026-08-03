<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;
use Upkeep\Filesystem\FilesystemException;
use Upkeep\Filesystem\FileWriter;

/**
 * The `.upkeep-env.yml` dotfile written into every provisioned environment:
 * records what the environment was provisioned for (module, core major), what
 * it was seeded from (exact core version of the base artifact), which engine
 * add-on version it carries, and when it was last used.
 *
 * `last_used_at` is what `prune --older-than` filters on, so it is stamped on
 * every reuse — without that, age falls back to creation time and prune
 * deletes environments that are in active use.
 *
 * It doubles as the provisioning completion marker — it is written as the
 * LAST provisioning step, so a missing/unparseable dotfile means a partial
 * provision that must be torn down and rebuilt, never reused. Every write of
 * it therefore goes through FileWriter's rename(): the path must never stop
 * existing, or a re-stamp would look like an interrupted provision and force
 * a multi-gigabyte rebuild. Task 16's disk attribution also reads it.
 */
final readonly class EnvironmentMeta
{
    public const FILENAME = '.upkeep-env.yml';

    private const REQUIRED_KEYS = ['module', 'core_major', 'seed_core_version', 'addon_version', 'created_at'];

    public function __construct(
        public string $moduleName,
        public string $coreMajor,
        public string $seedCoreVersion,
        public string $addOnVersion,
        public \DateTimeImmutable $createdAt,
        public ?\DateTimeImmutable $lastUsedAt = null,
    ) {
    }

    public static function fromYaml(string $yaml): self
    {
        try {
            $data = Yaml::parse($yaml);
        } catch (ParseException $e) {
            throw new AdapterException('Malformed environment meta YAML: ' . $e->getMessage(), previous: $e);
        }

        if (!is_array($data) || array_is_list($data)) {
            throw new AdapterException('Environment meta YAML must be a mapping.');
        }

        foreach (self::REQUIRED_KEYS as $key) {
            if (!isset($data[$key]) || !is_scalar($data[$key])) {
                throw new AdapterException(sprintf('Environment meta YAML is missing required scalar key "%s".', $key));
            }
        }

        $lastUsedRaw = $data['last_used_at'] ?? null;
        if ($lastUsedRaw !== null && !is_scalar($lastUsedRaw)) {
            throw new AdapterException('Environment meta key "last_used_at" must be a timestamp.');
        }

        return new self(
            (string) $data['module'],
            (string) $data['core_major'],
            (string) $data['seed_core_version'],
            (string) $data['addon_version'],
            self::timestamp('created_at', $data['created_at']),
            $lastUsedRaw === null ? null : self::timestamp('last_used_at', $lastUsedRaw),
        );
    }

    public function toYaml(): string
    {
        $data = [
            'module' => $this->moduleName,
            'core_major' => $this->coreMajor,
            'seed_core_version' => $this->seedCoreVersion,
            'addon_version' => $this->addOnVersion,
            'created_at' => $this->createdAt->format(\DateTimeInterface::ATOM),
        ];
        if ($this->lastUsedAt !== null) {
            $data['last_used_at'] = $this->lastUsedAt->format(\DateTimeInterface::ATOM);
        }

        return Yaml::dump($data);
    }

    public function withLastUsedAt(\DateTimeImmutable $at): self
    {
        return new self(
            $this->moduleName,
            $this->coreMajor,
            $this->seedCoreVersion,
            $this->addOnVersion,
            $this->createdAt,
            $at,
        );
    }

    /**
     * Writes this meta as the environment's dotfile, atomically.
     *
     * @throws FilesystemException when the dotfile cannot be written
     */
    public function writeTo(string $projectPath): void
    {
        FileWriter::write(
            rtrim($projectPath, '/') . '/' . self::FILENAME,
            $this->toYaml(),
            FileWriter::MODE_SHARED,
        );
    }

    /**
     * Records that the environment at $projectPath is in use right now, so
     * `prune --older-than` measures time since last use rather than time
     * since creation.
     *
     * @throws AdapterException when the existing dotfile cannot be read
     * @throws FilesystemException when the updated dotfile cannot be written
     */
    public static function stampLastUsed(string $projectPath, ?\DateTimeImmutable $at = null): void
    {
        $path = rtrim($projectPath, '/') . '/' . self::FILENAME;
        $content = @file_get_contents($path);
        if ($content === false) {
            throw new AdapterException(sprintf('Cannot read the environment meta at "%s".', $path));
        }

        self::fromYaml($content)->withLastUsedAt($at ?? new \DateTimeImmutable())->writeTo($projectPath);
    }

    private static function timestamp(string $key, mixed $value): \DateTimeImmutable
    {
        try {
            return new \DateTimeImmutable((string) (is_scalar($value) ? $value : ''));
        } catch (\Exception $e) {
            throw new AdapterException(sprintf(
                'Environment meta key "%s" is not a parseable timestamp: "%s".',
                $key,
                is_scalar($value) ? (string) $value : get_debug_type($value),
            ), previous: $e);
        }
    }

    /**
     * The reuse decision: compares what this environment was provisioned for
     * and from against what is being requested now. Any reason means the
     * environment is stale and must be torn down and re-provisioned; an empty
     * list means it is safe to reuse.
     *
     * @return list<string>
     */
    public function staleReasons(
        string $moduleName,
        string $coreMajor,
        string $seedCoreVersion,
        string $addOnVersion,
    ): array {
        $reasons = [];

        if ($this->moduleName !== $moduleName) {
            $reasons[] = sprintf(
                'Environment was provisioned for module "%s", requested "%s".',
                $this->moduleName,
                $moduleName,
            );
        }
        if ($this->coreMajor !== $coreMajor) {
            $reasons[] = sprintf(
                'Environment was provisioned for core %s, requested %s.',
                $this->coreMajor,
                $coreMajor,
            );
        }
        if ($this->seedCoreVersion !== $seedCoreVersion) {
            $reasons[] = sprintf(
                'Seed skew: environment was seeded from base artifact core %s, the canonical artifact is now core %s.',
                $this->seedCoreVersion,
                $seedCoreVersion,
            );
        }
        if ($this->addOnVersion !== $addOnVersion) {
            $reasons[] = sprintf(
                'Engine add-on skew: environment carries add-on %s, the adapter pins %s.',
                $this->addOnVersion,
                $addOnVersion,
            );
        }

        return $reasons;
    }
}
