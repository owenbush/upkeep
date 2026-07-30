<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

use Symfony\Component\Yaml\Yaml;

/**
 * Identity and pinning of the engine add-on (ddev-drupal-contrib).
 *
 * VERSION is the single configuration point for the pinned add-on release.
 * Upgrade procedure (a deliberate adapter-maintenance event, never routine):
 *   1. Read the new release's diff — especially install.yaml,
 *      config.contrib.yaml, and the commands/ scripts the adapter shells out
 *      to (phpunit, phpcs, phpstan, core-version).
 *   2. Refresh the SHIPPED_CONFIG fixture in EngineAddOnTest with the new
 *      config.contrib.yaml and make adaptContribConfig() pass against it.
 *   3. Bump VERSION here; existing environments then self-report add-on skew
 *      via EnvironmentMeta::staleReasons() and are re-provisioned on the next
 *      ensure_env instead of being silently reused.
 *   4. Live-verify one full ensure_env -> run_checks -> teardown cycle.
 */
final readonly class EngineAddOn
{
    public const string NAME = 'ddev/ddev-drupal-contrib';
    public const string VERSION = '1.1.5';

    /**
     * The add-on config file it installs into <project>/.ddev/, adapted by
     * adaptContribConfig() after every `add-on get`.
     */
    public const string CONFIG_FILENAME = 'config.contrib.yaml';

    /**
     * Adapts the shipped config.contrib.yaml to upkeep's seeded-tree layout.
     *
     * The add-on assumes the MODULE is the project root ("module as the
     * center of the universe"): its post-start hook symlinks all project-root
     * files into the docroot via `symlink-project`, and its check commands
     * target web/<DRUPAL_PROJECTS_PATH>. Upkeep environments instead seed the
     * project root from the canonical base tree and wire the module in with a
     * Composer path repository, so:
     *
     *   - the post-start symlink-project hook is removed (against a full
     *     project tree it would symlink the whole codebase into itself), and
     *   - DRUPAL_PROJECTS_PATH is repointed at modules/contrib, where the
     *     path-repository install lands the module symlink — the add-on's
     *     phpunit/phpcs/phpstan commands then target exactly the module
     *     under maintenance (the seeded tree is otherwise module-free).
     *
     * The #ddev-generated marker is kept: a future `add-on get` may clobber
     * the file, which is why the adapter re-runs this adaptation after every
     * add-on installation.
     */
    public static function adaptContribConfig(string $shippedYaml): string
    {
        $config = Yaml::parse($shippedYaml);
        if (!is_array($config)) {
            throw new AdapterException('Engine add-on config is not a YAML mapping; refusing to adapt it.');
        }

        unset($config['hooks']);

        if (isset($config['web_environment']) && is_array($config['web_environment'])) {
            $config['web_environment'] = array_values(array_map(
                static fn (string $var): string => str_starts_with($var, 'DRUPAL_PROJECTS_PATH=')
                    ? 'DRUPAL_PROJECTS_PATH=modules/contrib'
                    : $var,
                $config['web_environment'],
            ));
        }

        return "#ddev-generated\n# Adapted by upkeep for the seeded-tree layout (see Upkeep\\Adapter\\EngineAddOn).\n"
            . Yaml::dump($config);
    }
}
