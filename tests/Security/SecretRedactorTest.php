<?php

declare(strict_types=1);

namespace Upkeep\Tests\Security;

use PHPUnit\Framework\TestCase;
use Upkeep\Gitlab\TokenResolver;
use Upkeep\Security\CredentialEnvironment;
use Upkeep\Security\SecretRedactor;

final class SecretRedactorTest extends TestCase
{
    public function testReplacesEveryOccurrenceOfEachSecret(): void
    {
        $redactor = new SecretRedactor('glpat-AAAAAAAAAAAAAAAAAAAA', 'glpat-BBBBBBBBBBBBBBBBBBBB');

        $redacted = $redactor->redact(
            'header glpat-AAAAAAAAAAAAAAAAAAAA and again glpat-AAAAAAAAAAAAAAAAAAAA '
            . 'plus glpat-BBBBBBBBBBBBBBBBBBBB',
        );

        $this->assertStringNotContainsString('glpat-AAAAAAAAAAAAAAAAAAAA', $redacted);
        $this->assertStringNotContainsString('glpat-BBBBBBBBBBBBBBBBBBBB', $redacted);
        $this->assertSame(3, substr_count($redacted, SecretRedactor::MASK));
    }

    public function testLeavesTextWithoutSecretsUntouched(): void
    {
        $redactor = new SecretRedactor('glpat-AAAAAAAAAAAAAAAAAAAA');

        $this->assertSame('nothing to see here', $redactor->redact('nothing to see here'));
    }

    public function testWithNoSecretsIsAnIdentityFunction(): void
    {
        $redactor = new SecretRedactor();

        $this->assertSame('anything at all', $redactor->redact('anything at all'));
    }

    /**
     * A short or empty secret would shred unrelated output; masking every "a"
     * is worse than not masking at all, so short values are ignored.
     */
    public function testIgnoresEmptyAndImplausiblyShortSecrets(): void
    {
        $redactor = new SecretRedactor('', '   ', 'abc');

        $this->assertSame('abc and a plain sentence', $redactor->redact('abc and a plain sentence'));
    }

    /** A secret containing another masks fully, whichever order they arrive in. */
    public function testLongestSecretIsMaskedFirst(): void
    {
        $redactor = new SecretRedactor('token-prefix', 'token-prefix-and-suffix');

        $this->assertSame(SecretRedactor::MASK, $redactor->redact('token-prefix-and-suffix'));
    }

    public function testFromEnvironmentPicksUpTheCredentialVariable(): void
    {
        $previous = getenv(TokenResolver::DEFAULT_ENV_VAR);
        putenv(TokenResolver::DEFAULT_ENV_VAR . '=glpat-ENVSECRETENVSECRET');

        try {
            $redactor = SecretRedactor::fromEnvironment();

            $this->assertSame(
                'auth=' . SecretRedactor::MASK,
                $redactor->redact('auth=glpat-ENVSECRETENVSECRET'),
            );
        } finally {
            if (\is_string($previous)) {
                putenv(TokenResolver::DEFAULT_ENV_VAR . '=' . $previous);
            } else {
                putenv(TokenResolver::DEFAULT_ENV_VAR);
            }
        }
    }

    public function testCredentialEnvironmentUnsetsEveryCredentialVariable(): void
    {
        $scrubbed = CredentialEnvironment::scrubbed();

        $this->assertArrayHasKey(TokenResolver::DEFAULT_ENV_VAR, $scrubbed);
        // Symfony Process treats false — and only false — as "do not export".
        $this->assertSame(array_fill_keys(CredentialEnvironment::VARS, false), $scrubbed);
    }
}
