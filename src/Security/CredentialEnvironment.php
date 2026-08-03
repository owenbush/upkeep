<?php

declare(strict_types=1);

namespace Upkeep\Security;

use Upkeep\Gitlab\TokenResolver;

/**
 * The credential-carrying environment variables, and the override map that
 * keeps them out of child processes.
 *
 * Symfony's Process merges the *entire* parent environment into every child
 * when no explicit environment is given, so a PAT supplied through
 * UPKEEP_GITLAB_TOKEN would otherwise be visible to every subprocess the tool
 * spawns — and from there to any child that echoes its own environment, whose
 * output this tool logs, renders, and caches on disk.
 */
final readonly class CredentialEnvironment
{
    /** @var list<string> */
    public const VARS = [TokenResolver::DEFAULT_ENV_VAR];

    /**
     * Environment overrides to pass as Process's $env argument. Symfony treats
     * false — and only false — as "do not export this variable", so this
     * removes the credential rather than blanking it.
     *
     * @return array<string, false>
     */
    public static function scrubbed(): array
    {
        return array_fill_keys(self::VARS, false);
    }

    /**
     * The credential values currently present in this process's environment,
     * for redaction purposes. Never printed, never persisted.
     *
     * @return list<string>
     */
    public static function values(): array
    {
        $values = [];
        foreach (self::VARS as $name) {
            $value = getenv($name);
            if (\is_string($value) && trim($value) !== '') {
                $values[] = trim($value);
            }
        }

        return $values;
    }
}
