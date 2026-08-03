<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

/**
 * Save-and-restore for process environment variables.
 *
 * Every variable this tool reads — $HOME, $XDG_CONFIG_HOME, $UPKEEP_COCKPIT,
 * $UPKEEP_PROJECTS_ROOT, $UPKEEP_GITLAB_TOKEN — is process-global, so a test
 * that sets one without restoring it changes the meaning of every test that
 * runs after it. The resulting failures reproduce only in one test order,
 * which is the most expensive kind to debug; this class exists so restoring
 * is a single call that cannot be forgotten half-way through.
 *
 * The first value seen for a name is the one restored, so repeated sets of
 * the same variable still restore the value the process started with.
 */
final class EnvironmentGuard
{
    /** @var array<string, string|false> the value each name had before we touched it */
    private array $original = [];

    /** Sets a variable, or removes it when $value is null. */
    public function set(string $name, ?string $value): void
    {
        if (!\array_key_exists($name, $this->original)) {
            $this->original[$name] = getenv($name);
        }

        self::apply($name, $value);
    }

    /** Restores every variable this guard has touched. */
    public function restore(): void
    {
        foreach ($this->original as $name => $value) {
            self::apply($name, $value === false ? null : $value);
        }

        $this->original = [];
    }

    private static function apply(string $name, ?string $value): void
    {
        if ($value === null) {
            putenv($name);

            return;
        }

        putenv($name . '=' . $value);
    }
}
