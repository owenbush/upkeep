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
    /**
     * Reading needs no credential.
     *
     * drupalcode serves a public project's merge requests, refs, forks and raw
     * files anonymously — measured across upkeep's whole read surface — so
     * requiring a token to *look* was a restriction upkeep imposed rather than
     * one GitLab does. readOnly() therefore always returns a client.
     */
    public function testReadOnlyReturnsAClientWithNoTokenConfigured(): void
    {
        $notes = [];
        $client = GitlabClientFactory::readOnly(
            new TokenResolver('UPKEEP_NO_SUCH_TOKEN_VAR', '/nonexistent/pat'),
            static function (string $note) use (&$notes): void {
                $notes[] = $note;
            },
        );

        self::assertInstanceOf(GitlabClient::class, $client);
        self::assertTrue($client->isAnonymous());
        self::assertCount(1, $notes, 'said once, not per request');
    }

    /**
     * And the note says what is actually lost, because both consequences are
     * surprising: a private project answers an anonymous read with 404 rather
     * than 401 — GitLab hides existence — so a module you can see while signed
     * in reads as missing; and writing is not degraded but impossible.
     */
    public function testTheDegradedModeNoteNamesWhatAnonymousReadingCosts(): void
    {
        $note = GitlabClientFactory::anonymousReadMessage(
            new TokenResolver('UPKEEP_NO_SUCH_TOKEN_VAR', '/nonexistent/pat'),
        );

        self::assertStringContainsString('anonymously', $note);
        self::assertStringContainsString('not found', $note);
        self::assertStringContainsString('merge, comment or publish', $note);
        self::assertStringContainsString('UPKEEP_NO_SUCH_TOKEN_VAR', $note, 'and where to configure one');
    }

    /** With a token it is an ordinary authenticated client, and says nothing. */
    public function testReadOnlyUsesTheTokenWhenThereIsOneAndStaysQuiet(): void
    {
        $file = tempnam(sys_get_temp_dir(), 'upkeep-pat');
        self::assertIsString($file);
        file_put_contents($file, "glpat-read-only-fixture-token\n");
        chmod($file, 0o600);

        $notes = [];
        try {
            $client = GitlabClientFactory::readOnly(
                new TokenResolver('UPKEEP_NO_SUCH_TOKEN_VAR', $file),
                static function (string $note) use (&$notes): void {
                    $notes[] = $note;
                },
            );

            self::assertFalse($client->isAnonymous());
            self::assertSame([], $notes);
        } finally {
            unlink($file);
        }
    }

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
