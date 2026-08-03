<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The engine's machine-readable project description (`ddev describe -j`),
 * narrowed once here at the boundary.
 *
 * Child output is untrusted: the engine may be a different version, may
 * report an error object, or may print nothing parseable at all. Rather than
 * every caller re-deciding what a missing or wrongly typed field means, the
 * description is parsed in one place and exposes typed readers — absent,
 * null-shaped and wrongly typed all collapse to the same "not known" answer,
 * which is what every caller wants anyway.
 */
final readonly class EngineDescription
{
    /**
     * @param array<array-key, mixed> $fields the description object's own fields
     */
    private function __construct(private array $fields)
    {
    }

    /**
     * Parses the engine's describe output, or null when there is nothing
     * usable in it — no output, invalid JSON, or no description object.
     */
    public static function fromJson(?string $json): ?self
    {
        if ($json === null) {
            return null;
        }

        $decoded = json_decode($json, true);
        if (!\is_array($decoded)) {
            return null;
        }

        $fields = $decoded['raw'] ?? null;

        return \is_array($fields) ? new self($fields) : null;
    }

    /**
     * A scalar field as a string, addressed by a path of keys. Null when any
     * step of the path is missing or the value is structured — the same
     * narrowing rule as the API-payload readers at the other two boundaries
     * (Gitlab\ApiPayload, Drupal\ApiPayload): a wrongly shaped field is "not
     * known", never the word "Array".
     */
    public function stringOrNull(string ...$path): ?string
    {
        $value = $this->fields;
        foreach ($path as $key) {
            if (!\is_array($value) || !\array_key_exists($key, $value)) {
                return null;
            }
            $value = $value[$key];
        }

        return \is_scalar($value) ? (string) $value : null;
    }
}
