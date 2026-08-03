<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\HttpClient\HttpClient;

/**
 * The single seam between token resolution and an authenticated client.
 *
 * Every command needing GitLab goes through here so the "no token configured"
 * experience — the tool's primary security-relevant UX — has one wording, one
 * set of sources, and one place where token-file warnings surface.
 */
final readonly class GitlabClientFactory
{
    /**
     * Resolver wired to the console: file-permission and malformed-token
     * warnings reach the operator instead of vanishing into a no-op sink.
     */
    public static function resolver(
        SymfonyStyle $io,
        string $envVar = TokenResolver::DEFAULT_ENV_VAR,
        ?string $configFile = null,
    ): TokenResolver {
        return new TokenResolver(
            $envVar,
            $configFile,
            static function (string $message) use ($io): void {
                $io->warning($message);
            },
        );
    }

    /**
     * Resolve and build in one step, reporting the missing-token case.
     */
    public static function forConsole(
        SymfonyStyle $io,
        string $envVar = TokenResolver::DEFAULT_ENV_VAR,
        ?string $configFile = null,
    ): ?GitlabClient {
        return self::fromResolvedToken(self::resolver($io, $envVar, $configFile), $io);
    }

    /**
     * Returns null — after printing the shared guidance as an error — when no
     * token is configured. The guidance never contains token material.
     */
    public static function fromResolvedToken(TokenResolver $resolver, SymfonyStyle $io): ?GitlabClient
    {
        return self::authenticated($resolver, static function (string $message) use ($io): void {
            $io->error($message);
        });
    }

    /**
     * The one place a resolved token becomes a live client, so "how do we talk
     * to GitLab" is decided once. $report receives the shared missing-token
     * guidance and decides how loud it is: `patches` reports it as a warning
     * because running without a credential is a documented degraded mode,
     * every other command as an error.
     *
     * @param callable(string): void $report
     */
    public static function authenticated(TokenResolver $resolver, callable $report): ?GitlabClient
    {
        $token = $resolver->resolve();
        if ($token === null) {
            $report(self::missingTokenMessage($resolver));

            return null;
        }

        return new GitlabClient(HttpClient::create(), $token);
    }

    /**
     * The one wording for "no GitLab token configured", for callers that
     * report it their own way (an exception, a warning, a degraded mode).
     */
    public static function missingTokenMessage(TokenResolver $resolver): string
    {
        return sprintf(
            'No GitLab token found. Configure one of: %s. (The token is never printed or logged.)',
            $resolver->describeSources(),
        );
    }
}
