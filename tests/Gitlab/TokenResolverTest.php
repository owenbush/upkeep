<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gitlab;

use PHPUnit\Framework\TestCase;
use Upkeep\Gitlab\TokenResolver;

final class TokenResolverTest extends TestCase
{
    private const ENV_VAR = 'UPKEEP_TEST_GITLAB_TOKEN';

    protected function tearDown(): void
    {
        putenv(self::ENV_VAR);
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
}
