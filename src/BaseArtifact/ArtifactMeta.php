<?php

declare(strict_types=1);

namespace Upkeep\BaseArtifact;

use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;

/**
 * The meta.yml sidecar of a per-core-version base artifact set: records the
 * exact identity the artifact was built against so ensure_env and
 * snapshot-materialization can detect skew before reusing it.
 */
final readonly class ArtifactMeta
{
    private const REQUIRED_KEYS = ['core_version', 'core_major', 'php_version', 'db_engine', 'built_at'];

    public function __construct(
        public string $coreVersion,
        public string $coreMajor,
        public string $phpVersion,
        public string $dbEngine,
        public \DateTimeImmutable $builtAt,
    ) {
    }

    public static function fromYaml(string $yaml): self
    {
        try {
            $data = Yaml::parse($yaml);
        } catch (ParseException $e) {
            throw new MetaException('Malformed meta YAML: ' . $e->getMessage(), previous: $e);
        }

        if (!is_array($data) || array_is_list($data)) {
            throw new MetaException('Meta YAML must be a mapping of build metadata keys.');
        }

        foreach (self::REQUIRED_KEYS as $key) {
            if (!isset($data[$key]) || !is_scalar($data[$key])) {
                throw new MetaException(sprintf('Meta YAML is missing required scalar key "%s".', $key));
            }
        }

        try {
            $builtAt = new \DateTimeImmutable((string) $data['built_at']);
        } catch (\Exception $e) {
            throw new MetaException(
                sprintf('Meta YAML key "built_at" is not a parseable timestamp: "%s".', $data['built_at']),
                previous: $e,
            );
        }

        return new self(
            (string) $data['core_version'],
            (string) $data['core_major'],
            (string) $data['php_version'],
            (string) $data['db_engine'],
            $builtAt,
        );
    }

    /**
     * Compares this artifact's recorded build identity against a live
     * environment. PHP is compared at major.minor granularity (patch releases
     * are ABI-compatible and not skew); the DB engine identity must match
     * exactly. Returns human-readable skew reasons; empty means no skew.
     *
     * @return list<string>
     */
    public function skewAgainst(string $phpVersion, string $dbEngine): array
    {
        $reasons = [];

        $recordedPhp = self::phpMajorMinor($this->phpVersion);
        $livePhp = self::phpMajorMinor($phpVersion);
        if ($recordedPhp !== $livePhp) {
            $reasons[] = sprintf(
                'PHP version skew: artifact built on %s, environment runs %s.',
                $recordedPhp,
                $livePhp,
            );
        }

        if ($this->dbEngine !== $dbEngine) {
            $reasons[] = sprintf(
                'DB engine skew: artifact built on %s, environment runs %s.',
                $this->dbEngine,
                $dbEngine,
            );
        }

        return $reasons;
    }

    private static function phpMajorMinor(string $version): string
    {
        return implode('.', array_slice(explode('.', $version), 0, 2));
    }

    public function toYaml(): string
    {
        return Yaml::dump([
            'core_version' => $this->coreVersion,
            'core_major' => $this->coreMajor,
            'php_version' => $this->phpVersion,
            'db_engine' => $this->dbEngine,
            'built_at' => $this->builtAt->format(\DateTimeInterface::ATOM),
        ]);
    }
}
