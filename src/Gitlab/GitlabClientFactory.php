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
     * A client for commands that only read.
     *
     * git.drupalcode.org serves every public project's merge requests, refs,
     * forks and raw files without a credential, so requiring one to *look* was
     * a restriction upkeep imposed rather than one GitLab does. Reading
     * anonymously is now the documented degraded mode: it always returns a
     * client, and notes what is lost when there is no token.
     *
     * What is lost is real and worth saying. A private project answers an
     * anonymous read with 404 rather than 401 — GitLab hides existence — so a
     * module you can see while signed in reads as missing; and rate limits are
     * tighter. Writing is refused by the client itself, so no command can
     * reach a merge or a comment down this path by forgetting to check.
     *
     * @param callable(string): void $report receives the degraded-mode note
     */
    public static function readOnly(TokenResolver $resolver, callable $report): GitlabClient
    {
        $token = $resolver->resolve();
        if ($token !== null) {
            return new GitlabClient(HttpClient::create(), $token);
        }

        $report(self::anonymousReadMessage($resolver));

        return new GitlabClient(HttpClient::create());
    }

    /**
     * The injected client, or a read-only one built here.
     *
     * The "injected in tests, resolved otherwise" pattern lives in the factory
     * rather than being written out at each call site, because written inline
     * the fallback arm sits on the line immediately before a live request —
     * which means no offline test can reach it, and the coverage floor cannot
     * tell a deliberate gap from an accident. Here it is reachable on its own.
     *
     * @param callable(string): void $report receives the degraded-mode note
     */
    public static function readOnlyOr(?GitlabClient $injected, TokenResolver $resolver, callable $report): GitlabClient
    {
        return $injected ?? self::readOnly($resolver, $report);
    }

    /**
     * Said once, in one wording, like the missing-token guidance it replaces
     * for read-only commands. It never contains token material.
     */
    public static function anonymousReadMessage(TokenResolver $resolver): string
    {
        return sprintf(
            'No GitLab token configured, so this is reading git.drupalcode.org anonymously. Public projects '
            . 'answer fine; a private one will read as "not found" rather than "not allowed", and rate limits '
            . 'are tighter. Configure a token in %s to read as yourself — and to merge, comment or publish, '
            . 'which anonymous access cannot do at all.',
            // The resolver's own sources, not the defaults: a run pointed at a
            // different env var or config file must be told about that one, or
            // the guidance sends somebody to edit a file nothing reads.
            $resolver->describeSources(),
        );
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
