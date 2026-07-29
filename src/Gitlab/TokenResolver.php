<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * Resolves the GitLab PAT without ever printing or persisting it.
 *
 * Sources, in order:
 *   1. the UPKEEP_GITLAB_TOKEN environment variable (non-empty);
 *   2. the config file ~/.config/upkeep/drupal-pat (or
 *      $XDG_CONFIG_HOME/upkeep/drupal-pat), content trimmed.
 *
 * Returns null when neither is available; callers decide how to report that
 * (and must not echo any token material while doing so).
 */
final readonly class TokenResolver
{
    public const DEFAULT_ENV_VAR = 'UPKEEP_GITLAB_TOKEN';

    private string $configFile;

    public function __construct(
        private string $envVar = self::DEFAULT_ENV_VAR,
        ?string $configFile = null,
    ) {
        $this->configFile = $configFile ?? self::defaultConfigFile();
    }

    public function resolve(): ?string
    {
        $fromEnv = getenv($this->envVar);
        if (\is_string($fromEnv) && trim($fromEnv) !== '') {
            return trim($fromEnv);
        }

        if (is_readable($this->configFile)) {
            $content = trim((string) file_get_contents($this->configFile));
            if ($content !== '') {
                return $content;
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
        return sprintf('env var %s, config file %s', $this->envVar, $this->configFile);
    }

    public static function defaultConfigFile(): string
    {
        $configHome = getenv('XDG_CONFIG_HOME');
        if (!\is_string($configHome) || $configHome === '') {
            $configHome = (getenv('HOME') ?: '~') . '/.config';
        }

        return $configHome . '/upkeep/drupal-pat';
    }
}
