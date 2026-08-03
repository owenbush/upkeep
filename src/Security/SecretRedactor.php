<?php

declare(strict_types=1);

namespace Upkeep\Security;

/**
 * Masks known secret values in any string headed for a log, an exception
 * message, a rendered excerpt, or a file.
 *
 * This is the second of the two layers guarding the PAT: layer one keeps the
 * credential out of child environments entirely (CredentialEnvironment), and
 * this one catches a secret that reaches a reporting boundary by any future
 * route. It is deliberately dumb — literal substring replacement over the
 * exact values in play — because anything cleverer risks either missing a
 * value or shredding unrelated output.
 */
final readonly class SecretRedactor
{
    public const MASK = '[REDACTED]';

    /**
     * Values shorter than this are ignored: masking them would corrupt far
     * more output than it protects, and no credential this tool handles is
     * that short.
     */
    private const MIN_SECRET_LENGTH = 8;

    /** @var list<string> longest first, so a secret containing another still masks fully */
    private array $secrets;

    public function __construct(string ...$secrets)
    {
        $usable = [];
        foreach ($secrets as $secret) {
            $secret = trim($secret);
            if (\strlen($secret) >= self::MIN_SECRET_LENGTH) {
                $usable[$secret] = true;
            }
        }

        $usable = array_keys($usable);
        usort($usable, static fn (string $a, string $b): int => \strlen($b) <=> \strlen($a));

        $this->secrets = $usable;
    }

    /**
     * A redactor seeded from the credentials this process was given.
     */
    public static function fromEnvironment(): self
    {
        return new self(...CredentialEnvironment::values());
    }

    public function redact(string $text): string
    {
        if ($this->secrets === []) {
            return $text;
        }

        return str_replace($this->secrets, self::MASK, $text);
    }
}
