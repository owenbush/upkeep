<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * The check suites a maintenance environment can run against the module
 * under maintenance. Values are upkeep's own check identifiers; how each maps
 * onto engine tooling is adapter-private.
 */
enum CheckType: string
{
    case PhpUnit = 'phpunit';
    case PhpCs = 'phpcs';
    case PhpStan = 'phpstan';
    case EsLint = 'eslint';
    case StyleLint = 'stylelint';

    /** The module installs/enables cleanly (`drush pm:install`). */
    case ModuleInstall = 'module_install';

    /** Front page returns HTTP 200 with the module enabled. */
    case FunctionalSmoke = 'functional_smoke';

    /** Deprecation/upgrade-status report for the target core, when the engine provides one. */
    case Deprecation = 'deprecation';
}
