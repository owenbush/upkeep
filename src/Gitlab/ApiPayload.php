<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * The one place a decoded GitLab response stops being `mixed`.
 *
 * `json_decode()` hands back arbitrarily shaped data from a server we do not
 * control, so every field read out of it is untrusted input. Rather than
 * scatter `is_string()`/`is_int()` checks (or blind casts) through the models
 * and their consumers, each field is narrowed exactly once — here, at the
 * boundary — into the type the model declares. Everything inward of a
 * `fromApi()` call therefore works with real types and needs no narrowing.
 *
 * Narrowing is total and lossy by design: a field of the wrong JSON type is
 * not an exception, it is the caller-supplied default. A wrong-typed field on
 * a dashboard row must degrade that row, not abort the run. Structural faults
 * that no default can paper over — a body that is not a list of objects when
 * the endpoint promises one — are GitlabClient's business and surface there as
 * MalformedResponse.
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
     * A numeric field as an int. JSON numbers arrive as int|float, and GitLab
     * ids are sometimes quoted, so numeric strings count too; anything else
     * yields $default.
     */
    public function int(string $key, int $default = 0): int
    {
        $value = $this->data[$key] ?? null;

        return \is_int($value) || \is_float($value) || (\is_string($value) && is_numeric($value))
            ? (int) $value
            : $default;
    }

    /**
     * A numeric field as an int, distinguishing "not supplied" as null.
     */
    public function intOrNull(string $key): ?int
    {
        $value = $this->data[$key] ?? null;

        return \is_int($value) || \is_float($value) || (\is_string($value) && is_numeric($value))
            ? (int) $value
            : null;
    }

    /**
     * A scalar field as a bool; absent, null or structured yields $default.
     * The default is required because callers derive it (MergeRequest infers
     * draft state from the title when the field is absent).
     */
    public function bool(string $key, bool $default): bool
    {
        $value = $this->data[$key] ?? null;

        return \is_scalar($value) ? (bool) $value : $default;
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
     * A nested JSON object as a raw array, for handing to another model's
     * `fromApi()`.
     *
     * @return array<array-key, mixed>|null
     */
    public function childArray(string $key): ?array
    {
        $value = $this->data[$key] ?? null;

        return \is_array($value) ? $value : null;
    }
}
