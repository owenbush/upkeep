<?php

declare(strict_types=1);

namespace Upkeep\Ui\Http;

/**
 * One outbound response, as data rather than as side effects.
 *
 * Handlers return these instead of calling header()/echo, which is what makes
 * the whole request surface testable without a socket: a test asserts on a
 * value object, and exactly one place — the front controller — turns it into
 * actual output.
 */
final readonly class Response
{
    /**
     * @param array<string, string> $headers
     */
    private function __construct(
        public int $status,
        public string $body,
        public array $headers,
    ) {
    }

    /**
     * @param array<string, mixed> $headers
     */
    public static function of(int $status, string $body, array $headers = []): self
    {
        $narrowed = [];
        foreach ($headers as $name => $value) {
            if (\is_scalar($value)) {
                $narrowed[$name] = (string) $value;
            }
        }

        return new self($status, $body, $narrowed);
    }

    /**
     * @param array<array-key, mixed> $data
     */
    public static function json(array $data, int $status = 200): self
    {
        return self::of(
            $status,
            json_encode($data, \JSON_THROW_ON_ERROR | \JSON_UNESCAPED_SLASHES) . "\n",
            self::securityHeaders() + ['Content-Type' => 'application/json'],
        );
    }

    public static function error(string $message, int $status): self
    {
        return self::json(['error' => $message], $status);
    }

    public static function html(string $body): self
    {
        return self::of($body === '' ? 404 : 200, $body, self::securityHeaders() + [
            'Content-Type' => 'text/html; charset=utf-8',
        ]);
    }

    public static function asset(string $body, string $contentType): self
    {
        return self::of(200, $body, self::securityHeaders() + ['Content-Type' => $contentType]);
    }

    /** Plain text, for streaming a job's captured output back to the page. */
    public static function text(string $body): self
    {
        return self::of(200, $body, self::securityHeaders() + ['Content-Type' => 'text/plain; charset=utf-8']);
    }

    public function withCookie(string $name, string $value): self
    {
        // HttpOnly so no script can read the token back out; SameSite=Strict so
        // another origin cannot ride the cookie into an action; no Secure,
        // because this only ever serves plain HTTP on the loopback interface
        // and setting it would stop the cookie being sent at all.
        return new self($this->status, $this->body, $this->headers + [
            'Set-Cookie' => sprintf('%s=%s; Path=/; HttpOnly; SameSite=Strict', $name, $value),
        ]);
    }

    public function withHeader(string $name, string $value): self
    {
        return new self($this->status, $this->body, [$name => $value] + $this->headers);
    }

    /**
     * Headers every response carries.
     *
     * The content-security policy is the load-bearing one: the page is served
     * from the loopback interface with a credential-holding process behind it,
     * so it may not fetch, frame, or execute anything that did not come from
     * this server. No CDN, no analytics, no remote fonts.
     *
     * @return array<string, string>
     */
    private static function securityHeaders(): array
    {
        return [
            'Content-Security-Policy' => "default-src 'self'; style-src 'self' 'unsafe-inline'; "
                . "img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
            'X-Content-Type-Options' => 'nosniff',
            'Referrer-Policy' => 'no-referrer',
            'Cache-Control' => 'no-store',
        ];
    }
}
