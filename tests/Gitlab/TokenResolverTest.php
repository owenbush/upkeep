<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gitlab;

use PHPUnit\Framework\TestCase;
use Upkeep\Gitlab\TokenResolver;

final class TokenResolverTest extends TestCase
{
    private const ENV_VAR = 'UPKEEP_TEST_GITLAB_TOKEN';

    /** Original values, so XDG_CONFIG_HOME/HOME manipulation never leaks into other tests. */
    private bool|string $originalXdgConfigHome = false;
    private bool|string $originalHome = false;

    protected function setUp(): void
    {
        $this->originalXdgConfigHome = getenv('XDG_CONFIG_HOME');
        $this->originalHome = getenv('HOME');
    }

    protected function tearDown(): void
    {
        putenv(self::ENV_VAR);
        self::restoreEnv('XDG_CONFIG_HOME', $this->originalXdgConfigHome);
        self::restoreEnv('HOME', $this->originalHome);
    }

    private static function restoreEnv(string $name, bool|string $original): void
    {
        if ($original === false) {
            putenv($name);
        } else {
            putenv($name . '=' . $original);
        }
    }

    public function testEnvironmentVariableWinsOverConfigFile(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, "file-token\n");
        putenv(self::ENV_VAR . '=env-token');

        try {
            $resolver = new TokenResolver(self::ENV_VAR, $file);
            $this->assertSame('env-token', $resolver->resolve());
        } finally {
            unlink($file);
        }
    }

    public function testConfigFileIsUsedWhenEnvUnsetAndContentIsTrimmed(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, "  file-token-value\n\n");

        try {
            $resolver = new TokenResolver(self::ENV_VAR, $file);
            $this->assertSame('file-token-value', $resolver->resolve());
        } finally {
            unlink($file);
        }
    }

    public function testResolvesToNullWhenNothingConfigured(): void
    {
        $resolver = new TokenResolver(self::ENV_VAR, '/nonexistent/path/to/pat');

        $this->assertNull($resolver->resolve());
    }

    public function testEmptyEnvValueFallsThroughToFile(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, 'file-token');
        putenv(self::ENV_VAR . '=');

        try {
            $resolver = new TokenResolver(self::ENV_VAR, $file);
            $this->assertSame('file-token', $resolver->resolve());
        } finally {
            unlink($file);
        }
    }

    /**
     * A blank config file (readable, zero non-empty lines) must fall through
     * to null rather than being treated as "content found".
     */
    public function testConfigFileThatIsReadableButBlankResolvesToNull(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, "\n   \n\n");

        try {
            $resolver = new TokenResolver(self::ENV_VAR, $file);
            $this->assertNull($resolver->resolve());
        } finally {
            unlink($file);
        }
    }

    /**
     * An env var can be non-empty by PHP's trim() (which does not strip a
     * form feed) yet carry no real line content once split into lines — the
     * same "blank" a human would call it. It must resolve to null, not
     * travel forward as a one-character token.
     */
    public function testEnvVarThatIsWhitespaceOnlyByLineSplittingResolvesToNull(): void
    {
        putenv(self::ENV_VAR . "=\x0C");

        $resolver = new TokenResolver(self::ENV_VAR, '/nonexistent/path/to/pat');

        $this->assertNull($resolver->resolve());
    }

    public function testDefaultConfigFileUsesXdgConfigHomeWhenSet(): void
    {
        putenv('XDG_CONFIG_HOME=/xdg/custom/config');

        $this->assertSame('/xdg/custom/config/upkeep/drupal-pat', TokenResolver::defaultConfigFile());
    }

    public function testDefaultConfigFileFallsBackToHomeConfigWhenXdgConfigHomeIsUnset(): void
    {
        putenv('XDG_CONFIG_HOME');
        putenv('HOME=/home/example-user');

        $this->assertSame('/home/example-user/.config/upkeep/drupal-pat', TokenResolver::defaultConfigFile());
    }

    public function testDefaultConfigFileFallsBackToHomeConfigWhenXdgConfigHomeIsEmptyString(): void
    {
        putenv('XDG_CONFIG_HOME=');
        putenv('HOME=/home/example-user');

        $this->assertSame('/home/example-user/.config/upkeep/drupal-pat', TokenResolver::defaultConfigFile());
    }

    public function testDefaultConfigFileFallsBackToLiteralTildeWhenNeitherXdgConfigHomeNorHomeIsSet(): void
    {
        putenv('XDG_CONFIG_HOME');
        putenv('HOME');

        $this->assertSame('~/.config/upkeep/drupal-pat', TokenResolver::defaultConfigFile());
    }

    public function testDescribeSourcesNamesTheEnvVarAndPathButNoTokenMaterial(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, 'glpat-SUPERSECRETVALUE');
        putenv(self::ENV_VAR . '=glpat-ENVSECRETVALUE');

        try {
            $resolver = new TokenResolver(self::ENV_VAR, $file);
            // Resolve first: describeSources() must stay clean even after the
            // resolver has seen the token.
            $this->assertSame('glpat-ENVSECRETVALUE', $resolver->resolve());

            $described = $resolver->describeSources();
            $this->assertStringContainsString(self::ENV_VAR, $described);
            $this->assertStringContainsString($file, $described);
            $this->assertStringNotContainsString('glpat-ENVSECRETVALUE', $described);
            $this->assertStringNotContainsString('glpat-SUPERSECRETVALUE', $described);
        } finally {
            unlink($file);
        }
    }

    public function testGroupOrWorldReadableTokenFileWarnsWithoutEchoingTheToken(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, 'glpat-SUPERSECRETVALUE');
        chmod($file, 0o644);

        $warnings = [];
        try {
            $resolver = new TokenResolver(
                self::ENV_VAR,
                $file,
                static function (string $message) use (&$warnings): void {
                    $warnings[] = $message;
                },
            );

            $this->assertSame('glpat-SUPERSECRETVALUE', $resolver->resolve());
            $this->assertCount(1, $warnings);
            $this->assertStringContainsString($file, $warnings[0]);
            $this->assertStringContainsString('chmod 600', $warnings[0]);
            $this->assertStringNotContainsString('glpat-SUPERSECRETVALUE', $warnings[0]);
        } finally {
            unlink($file);
        }
    }

    public function testWarnsOnlyOnceAcrossRepeatedResolves(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, 'glpat-SUPERSECRETVALUE');
        chmod($file, 0o640);

        $warnings = [];
        try {
            $resolver = new TokenResolver(
                self::ENV_VAR,
                $file,
                static function (string $message) use (&$warnings): void {
                    $warnings[] = $message;
                },
            );
            $resolver->resolve();
            $resolver->resolve();

            $this->assertCount(1, $warnings);
        } finally {
            unlink($file);
        }
    }

    public function testPrivateTokenFileDoesNotWarn(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, 'glpat-SUPERSECRETVALUE');
        chmod($file, 0o600);

        $warnings = [];
        try {
            $resolver = new TokenResolver(
                self::ENV_VAR,
                $file,
                static function (string $message) use (&$warnings): void {
                    $warnings[] = $message;
                },
            );

            $this->assertSame('glpat-SUPERSECRETVALUE', $resolver->resolve());
            $this->assertSame([], $warnings);
        } finally {
            unlink($file);
        }
    }

    /** A .netrc-style or commented file must not become a multi-line header value. */
    public function testOnlyTheFirstNonEmptyLineOfTheConfigFileIsUsed(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, "\n\n  glpat-FIRSTLINE  \n# a trailing comment\nmore junk\n");
        chmod($file, 0o600);

        try {
            $resolver = new TokenResolver(self::ENV_VAR, $file);
            $this->assertSame('glpat-FIRSTLINE', $resolver->resolve());
        } finally {
            unlink($file);
        }
    }

    public function testValueWithCharactersIllegalInAnHttpHeaderIsRejectedWithoutEchoingIt(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, "glpat-BAD\x07VALUE\n");
        chmod($file, 0o600);

        $warnings = [];
        try {
            $resolver = new TokenResolver(
                self::ENV_VAR,
                $file,
                static function (string $message) use (&$warnings): void {
                    $warnings[] = $message;
                },
            );

            $this->assertNull($resolver->resolve());
            $this->assertCount(1, $warnings);
            $this->assertStringContainsString($file, $warnings[0]);
            $this->assertStringNotContainsString('glpat-BAD', $warnings[0]);
        } finally {
            unlink($file);
        }
    }
}
