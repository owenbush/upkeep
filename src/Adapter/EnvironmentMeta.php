<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Symfony\Component\Yaml\Exception\ParseException;
use Symfony\Component\Yaml\Yaml;

/**
 * The `.upkeep-env.yml` dotfile written into every provisioned environment:
 * records what the environment was provisioned for (module, core major), what
 * it was seeded from (exact core version of the base artifact), and which
 * engine add-on version it carries.
 *
 * It doubles as the provisioning completion marker — it is written as the
 * LAST provisioning step, so a missing/unparseable dotfile means a partial
 * provision that must be torn down and rebuilt, never reused. Task 16's disk
 * attribution also reads it.
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

        try {
            $createdAt = new \DateTimeImmutable((string) $data['created_at']);
        } catch (\Exception $e) {
            throw new AdapterException(sprintf(
                'Environment meta key "created_at" is not a parseable timestamp: "%s".',
                $data['created_at'],
            ), previous: $e);
        }

        return new self(
            (string) $data['module'],
            (string) $data['core_major'],
            (string) $data['seed_core_version'],
            (string) $data['addon_version'],
            $createdAt,
        );
    }

    public function toYaml(): string
    {
        return Yaml::dump([
            'module' => $this->moduleName,
            'core_major' => $this->coreMajor,
            'seed_core_version' => $this->seedCoreVersion,
            'addon_version' => $this->addOnVersion,
            'created_at' => $this->createdAt->format(\DateTimeInterface::ATOM),
        ]);
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
