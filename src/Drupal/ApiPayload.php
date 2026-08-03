<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

/**
 * The one place a decoded drupal.org response stops being `mixed`.
 *
 * The sibling of Gitlab\ApiPayload, deliberately the same shape: both wrap a
 * decoded JSON object from a server upkeep does not control, and both narrow
 * every field exactly once, at the boundary, into the type the model
 * declares. Everything inward of a `fromApi()` call therefore works with real
 * types and needs no narrowing.
 *
 * Narrowing is total and lossy by design: a field of the wrong JSON type is
 * not an exception, it is the caller-supplied default (or null). A wrongly
 * shaped field on one dashboard row must degrade that row, not abort the run
 * — and it must not be coerced into a plausible-looking lie, which a bare
 * cast would do ((string) on an array is the word "Array", (int) on one
 * is 1).
 *
 * The drupal.org REST API needs its own instance rather than sharing
 * GitLab's: it is a separate boundary with its own conventions (Drupal 7
 * field envelopes such as `{"und": [...]}`, entity references that arrive
 * either as an object or as a bare id).
 */
final readonly class ApiPayload
{
    /**
     * @param array<array-key, mixed> $data a decoded JSON object (or list)
     */
    public function __construct(private array $data)
    {
    }

    /**
     * A scalar field as a string; absent, null or structured yields $default.
     */
    public function string(string $key, string $default = ''): string
    {
        $value = $this->data[$key] ?? null;

        return \is_scalar($value) ? (string) $value : $default;
    }

    /**
     * A scalar field as a string, distinguishing "not supplied" as null.
     */
    public function stringOrNull(string $key): ?string
    {
        $value = $this->data[$key] ?? null;

        return \is_scalar($value) ? (string) $value : null;
    }

    /**
     * A numeric field as an int. The API renders node ids, timestamps and
     * taxonomy ids as quoted strings on some endpoints and as JSON numbers on
     * others, so numeric strings count too; anything else yields $default.
     */
    public function int(string $key, int $default = 0): int
    {
        return $this->intOrNull($key) ?? $default;
    }

    /**
     * A numeric field as an int, distinguishing "not supplied" as null. Used
     * where 0 would be a real answer — a node id or a status the caller must
     * be able to reject as absent rather than read as zero.
     */
    public function intOrNull(string $key): ?int
    {
        $value = $this->data[$key] ?? null;

        return \is_int($value) || \is_float($value) || (\is_string($value) && is_numeric($value))
            ? (int) $value
            : null;
    }

    /**
     * A nested JSON object as its own payload, or null when the field is
     * absent, null, or not an object at all.
     */
    public function child(string $key): ?self
    {
        $value = $this->data[$key] ?? null;

        return \is_array($value) ? new self($value) : null;
    }

    /**
     * A nested JSON object or list as a raw array, for iterating or for
     * handing to another model's `fromApi()`.
     *
     * @return array<array-key, mixed>|null
     */
    public function childArray(string $key): ?array
    {
        $value = $this->data[$key] ?? null;

        return \is_array($value) ? $value : null;
    }
}
