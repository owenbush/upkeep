<?php

declare(strict_types=1);

namespace Upkeep\Workflow;

/**
 * Whether the packages this tool declares it needs are actually installed.
 *
 * The check `./bin/upkeep list` cannot make. Adding `composer/semver` to
 * composer.json is a one-line commit that leaves every existing checkout
 * broken until somebody runs `composer install` — and nothing says so. What
 * happened instead was an uncaught `Class "Composer\Semver\VersionParser" not
 * found` forty lines deep in a stack trace, from a tool whose whole exit-code
 * contract is that it says what it could not do and what to run about it.
 *
 * It could not be caught by the gates either. `./bin/upkeep list` boots the
 * application and touches none of the code paths that reach a dependency, so
 * it exits 0 with the package removed; and the test suite runs against a
 * vendor/ where the package is present by construction. The repository was
 * correct throughout — composer.json declared it. What was missing was any
 * signal to an install that predated the declaration.
 *
 * So the manifest is the source of truth and this compares it against what is
 * on disk. A dependency added tomorrow is covered by that comparison without
 * anyone remembering to add it here, which is the only version of this check
 * worth having.
 */
final readonly class RuntimeRequirements
{
    /**
     * The package names composer.json requires at runtime.
     *
     * The platform requirements (`php`, `ext-*`, `lib-*`) are left out: they
     * are not installed packages, PHP has already refused to run if the
     * version is wrong, and a missing extension produces its own clear error.
     *
     * An unreadable or unparseable manifest yields no requirements, so the
     * check passes. Refusing to start because the manifest could not be read
     * would turn a cosmetic problem into a fatal one — the opposite of the
     * point.
     *
     * @return list<string>
     */
    public static function declaredIn(string $composerJsonPath): array
    {
        if (!is_file($composerJsonPath) || !is_readable($composerJsonPath)) {
            return [];
        }

        // Cast rather than branch: a read that fails after the file was found
        // readable is a race or a permissions change mid-call, and the cast
        // sends it through the decode below as unparseable — the same "no
        // requirements, start anyway" answer, by a route that is reachable.
        $raw = (string) file_get_contents($composerJsonPath);

        try {
            $manifest = json_decode($raw, true, 512, \JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return [];
        }

        $require = \is_array($manifest) ? ($manifest['require'] ?? null) : null;
        if (!\is_array($require)) {
            return [];
        }

        $packages = [];
        foreach (array_keys($require) as $package) {
            $package = (string) $package;
            if ($package === 'php' || str_contains($package, '/') === false) {
                continue;
            }
            $packages[] = $package;
        }

        return $packages;
    }

    /**
     * Which of them are not installed here.
     *
     * @param list<string>           $declared
     * @param callable(string): bool $isInstalled normally
     *                                            Composer\InstalledVersions::isInstalled(...), which reads
     *                                            the metadata composer writes beside the packages
     *
     * @return list<string>
     */
    public static function missing(array $declared, callable $isInstalled): array
    {
        return array_values(array_filter($declared, static fn (string $p): bool => !$isInstalled($p)));
    }

    /**
     * What to print, and what to run.
     *
     * Names the directory, because the recovery is a command that has to be
     * run somewhere in particular and a person meeting this has usually just
     * pulled in one checkout among several.
     *
     * @param list<string> $missing
     */
    public static function message(array $missing, string $packageRoot): string
    {
        $lines = [
            sprintf(
                'upkeep cannot start: %d required %s not installed.',
                \count($missing),
                \count($missing) === 1 ? 'package is' : 'packages are',
            ),
            '',
        ];
        foreach ($missing as $package) {
            $lines[] = '  ' . $package;
        }

        $lines[] = '';
        $lines[] = 'This usually means vendor/ is older than the code: the dependency list';
        $lines[] = 'changed since it was last installed. From ' . $packageRoot . ':';
        $lines[] = '';
        $lines[] = '  composer install';
        $lines[] = '';

        return implode(\PHP_EOL, $lines);
    }
}
