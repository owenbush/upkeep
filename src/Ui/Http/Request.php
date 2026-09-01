<?php

declare(strict_types=1);

namespace Upkeep\Ui\Http;

/**
 * One inbound request, narrowed out of PHP's superglobals at the edge.
 *
 * The same discipline `Gitlab\ApiPayload` applies to a decoded API body: every
 * field a handler reads is narrowed exactly once, here, so nothing inward of
 * this class works with `mixed` or trusts a superglobal's shape.
 */
final readonly class Request
{
    /**
     * @param array<string, string> $query
     * @param array<string, string> $cookies
     */
    private function __construct(
        public string $method,
        public string $path,
        public array $query,
        public array $cookies,
        public string $body,
    ) {
    }

    /**
     * @param array<array-key, mixed> $server
     * @param array<array-key, mixed> $query
     * @param array<array-key, mixed> $cookies
     */
    public static function fromGlobals(array $server, array $query, array $cookies, string $body): self
    {
        $uri = \is_string($server['REQUEST_URI'] ?? null) ? $server['REQUEST_URI'] : '/';
        $path = parse_url($uri, \PHP_URL_PATH);

        return new self(
            \is_string($server['REQUEST_METHOD'] ?? null) ? $server['REQUEST_METHOD'] : 'GET',
            \is_string($path) && $path !== '' ? $path : '/',
            self::strings($query),
            self::strings($cookies),
            $body,
        );
    }

    /**
     * @param array<string, string> $query
     * @param array<string, string> $cookies
     */
    public static function of(
        string $method,
        string $path,
        array $query = [],
        array $cookies = [],
        string $body = '',
    ): self {
        return new self($method, $path, $query, $cookies, $body);
    }

    /**
     * The token the caller presented.
     *
     * **The URL wins over the cookie**, and the order is the whole point. A
     * token in the query string is the operator deliberately presenting a
     * credential — they have just been handed a launch URL and followed it —
     * while the cookie is only what some earlier visit left behind.
     *
     * Preferring the cookie made a restart unrecoverable: `upkeep ui` mints a
     * fresh token each run, so the browser arrives at the new URL still
     * holding the old cookie, the old value shadows the new one, and every
     * fresh link is refused as expired. Restarting again produced another
     * link that could not work, which is exactly how it was reported.
     */
    public function token(): ?string
    {
        return $this->query['token'] ?? $this->cookies['upkeep_ui'] ?? null;
    }

    /**
     * A field of the JSON body, as a string. A body that is not a JSON object
     * has no fields, rather than being an error the handler must branch on.
     */
    public function bodyString(string $key): ?string
    {
        $decoded = json_decode($this->body, true);
        if (!\is_array($decoded)) {
            return null;
        }

        $value = $decoded[$key] ?? null;

        return \is_scalar($value) ? (string) $value : null;
    }

    /**
     * A path segment, 0-indexed after the leading slash: "/api/jobs/abc" has
     * segment 2 "abc".
     */
    public function segment(int $index): ?string
    {
        $segments = array_values(array_filter(explode('/', $this->path), static fn (string $s): bool => $s !== ''));

        return $segments[$index] ?? null;
    }

    /**
     * @param array<array-key, mixed> $values
     * @return array<string, string>
     */
    private static function strings(array $values): array
    {
        $narrowed = [];
        foreach ($values as $key => $value) {
            if (\is_string($key) && \is_scalar($value)) {
                $narrowed[$key] = (string) $value;
            }
        }

        return $narrowed;
    }
}
