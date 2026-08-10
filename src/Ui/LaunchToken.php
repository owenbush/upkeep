<?php

declare(strict_types=1);

namespace Upkeep\Ui;

/**
 * The secret in the launch URL, and the only thing standing between a browser
 * tab and a process holding a GitLab token that can merge.
 *
 * Binding to 127.0.0.1 keeps the port off the network, but it does not keep it
 * away from *other software on this machine* — any local process, and any web
 * page that can be tricked into issuing a request, can reach a localhost port.
 * So the server refuses every request that does not carry this token, which is
 * minted fresh per run and never written to disk.
 *
 * Compared with hash_equals: a timing-variable comparison on a secret a caller
 * can retry at will is a guessing oracle, and "it is only localhost" is the
 * reasoning that makes those oracles ship.
 */
final readonly class LaunchToken
{
    /** 256 bits, hex-encoded: long enough that guessing is not a strategy. */
    private const BYTES = 32;

    private function __construct(public string $value)
    {
    }

    public static function mint(): self
    {
        return new self(bin2hex(random_bytes(self::BYTES)));
    }

    /** For the server process, which receives the minted token in its environment. */
    public static function of(string $value): self
    {
        return new self($value);
    }

    public function matches(?string $candidate): bool
    {
        return $candidate !== null && hash_equals($this->value, $candidate);
    }

    /**
     * The URL to hand the operator. The token travels in the query string
     * because that is what a browser can be handed on the command line; the
     * page swaps it for a cookie on first load so it stops appearing in the
     * address bar and in any screenshot of it.
     */
    public function launchUrl(string $host, int $port): string
    {
        return sprintf('http://%s:%d/?token=%s', $host, $port, $this->value);
    }
}
