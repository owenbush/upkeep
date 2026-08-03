<?php

declare(strict_types=1);

namespace Upkeep\Tests\Gitlab;

use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Input\ArrayInput;
use Symfony\Component\Console\Output\BufferedOutput;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\GitlabClientFactory;
use Upkeep\Gitlab\TokenResolver;

final class GitlabClientFactoryTest extends TestCase
{
    private const ENV_VAR = 'UPKEEP_TEST_FACTORY_TOKEN';

    protected function tearDown(): void
    {
        putenv(self::ENV_VAR);
    }

    public function testMissingTokenMessageNamesBothSourcesAndPromisesNoLogging(): void
    {
        $resolver = new TokenResolver(self::ENV_VAR, '/nonexistent/upkeep/drupal-pat');

        $message = GitlabClientFactory::missingTokenMessage($resolver);

        $this->assertStringContainsString('No GitLab token found', $message);
        $this->assertStringContainsString(self::ENV_VAR, $message);
        $this->assertStringContainsString('/nonexistent/upkeep/drupal-pat', $message);
        $this->assertStringContainsString('never printed or logged', $message);
    }

    public function testFromResolvedTokenReportsAndReturnsNullWhenNoTokenIsConfigured(): void
    {
        $output = new BufferedOutput();
        $io = new SymfonyStyle(new ArrayInput([]), $output);

        $client = GitlabClientFactory::fromResolvedToken(
            new TokenResolver(self::ENV_VAR, '/nonexistent/upkeep/drupal-pat'),
            $io,
        );

        $this->assertNull($client);
        $this->assertStringContainsString('No GitLab token found', $output->fetch());
    }

    public function testFromResolvedTokenBuildsAClientWhenATokenIsConfigured(): void
    {
        putenv(self::ENV_VAR . '=glpat-FACTORYSECRETVALUE');
        $output = new BufferedOutput();
        $io = new SymfonyStyle(new ArrayInput([]), $output);

        $client = GitlabClientFactory::fromResolvedToken(
            new TokenResolver(self::ENV_VAR, '/nonexistent/upkeep/drupal-pat'),
            $io,
        );

        $this->assertInstanceOf(GitlabClient::class, $client);
        $this->assertStringNotContainsString('glpat-FACTORYSECRETVALUE', $output->fetch());
    }

    /** The console-wired resolver routes file-permission warnings to the user. */
    public function testForConsoleSurfacesTheTokenFilePermissionWarning(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat-');
        file_put_contents($file, 'glpat-CONSOLESECRETVALUE');
        chmod($file, 0o644);

        $output = new BufferedOutput();
        $io = new SymfonyStyle(new ArrayInput([]), $output);

        try {
            $client = GitlabClientFactory::forConsole($io, self::ENV_VAR, $file);

            $this->assertInstanceOf(GitlabClient::class, $client);
            $rendered = $output->fetch();
            $this->assertStringContainsString('chmod 600', $rendered);
            $this->assertStringNotContainsString('glpat-CONSOLESECRETVALUE', $rendered);
        } finally {
            unlink($file);
        }
    }
}
