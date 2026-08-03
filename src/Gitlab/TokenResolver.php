<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * Resolves the GitLab PAT without ever printing or persisting it.
 *
 * Sources, in order:
 *   1. the UPKEEP_GITLAB_TOKEN environment variable (non-empty);
 *   2. the config file ~/.config/upkeep/drupal-pat (or
 *      $XDG_CONFIG_HOME/upkeep/drupal-pat).
 *
 * In both cases only the first non-empty line is taken, and a value carrying
 * characters illegal in an HTTP header value is refused — otherwise it reaches
 * the HTTP client as a PRIVATE-TOKEN header and fails with an error naming
 * neither the cause nor the source.
 *
 * Returns null when neither source yields a usable token; callers decide how
 * to report that (and must not echo any token material while doing so).
 *
 * Diagnostics — a group/world-readable token file, an unusable value — go to
 * an injected warning sink so this class stays I/O-pure and testable. The
 * warnings never contain token material.
 */
final class TokenResolver
{
    public const DEFAULT_ENV_VAR = 'UPKEEP_GITLAB_TOKEN';

    private readonly string $configFile;

    /** @var \Closure(string): void */
    private readonly \Closure $warn;

    /** One permission warning per resolver, however often resolve() is called. */
    private bool $warnedAboutPermissions = false;

    /**
     * @param \Closure(string): void|null $warn defaults to a no-op sink
     */
    public function __construct(
        private readonly string $envVar = self::DEFAULT_ENV_VAR,
        ?string $configFile = null,
        ?\Closure $warn = null,
    ) {
        $this->configFile = $configFile ?? self::defaultConfigFile();
        $this->warn = $warn ?? static function (string $message): void {
        };
    }

    public function resolve(): ?string
    {
        $fromEnv = getenv($this->envVar);
        if (\is_string($fromEnv) && trim($fromEnv) !== '') {
            return $this->usable(self::firstLine($fromEnv), sprintf('env var %s', $this->envVar));
        }

        if (is_readable($this->configFile)) {
            $content = (string) file_get_contents($this->configFile);
            $this->warnIfReadableByOthers();
            $value = self::firstLine($content);
            if ($value !== '') {
                return $this->usable($value, sprintf('token file %s', $this->configFile));
            }
        }

        return null;
    }

    /**
     * Human-readable description of the sources consulted, safe to print
     * (never contains token material).
     */
    public function describeSources(): string
    {
        return self::describe($this->envVar, $this->configFile);
    }

    /**
     * The same description for callers that have no resolver instance — the
     * Unauthorized failure, raised deep inside the client, is the one such
     * caller. Also safe to print: it names sources, never token material.
     */
    public static function describeDefaultSources(): string
    {
        return self::describe(self::DEFAULT_ENV_VAR, self::defaultConfigFile());
    }

    private static function describe(string $envVar, string $configFile): string
    {
        return sprintf('env var %s, config file %s', $envVar, $configFile);
    }

    public static function defaultConfigFile(): string
    {
        $configHome = getenv('XDG_CONFIG_HOME');
        if (!\is_string($configHome) || $configHome === '') {
            $configHome = (getenv('HOME') ?: '~') . '/.config';
        }

        return $configHome . '/upkeep/drupal-pat';
    }

    /**
     * The first non-empty line, trimmed — so a .netrc-style block, a trailing
     * comment, or a stray newline cannot travel into a header value.
     */
    private static function firstLine(string $content): string
    {
        foreach (preg_split('/\R/', $content) ?: [] as $line) {
            $line = trim($line);
            if ($line !== '') {
                return $line;
            }
        }

        return '';
    }

    /**
     * Refuses a value that cannot legally be an HTTP header value. The
     * diagnostic names the source, never the value.
     */
    private function usable(string $value, string $source): ?string
    {
        if ($value === '') {
            return null;
        }

        if (preg_match('/[\x00-\x1F\x7F]/', $value) === 1) {
            ($this->warn)(sprintf(
                'The GitLab token from %s contains characters that cannot appear in an HTTP header '
                . 'and was ignored. Store the token alone, on one line.',
                $source,
            ));

            return null;
        }

        return $value;
    }

    private function warnIfReadableByOthers(): void
    {
        if ($this->warnedAboutPermissions) {
            return;
        }

        $stat = @stat($this->configFile);
        if ($stat === false || ($stat['mode'] & 0o077) === 0) {
            return;
        }

        $this->warnedAboutPermissions = true;
        ($this->warn)(sprintf(
            'The GitLab token file %s is readable by other accounts on this machine (mode %04o). '
            . 'Restrict it with: chmod 600 %s',
            $this->configFile,
            $stat['mode'] & 0o7777,
            $this->configFile,
        ));
    }
}
