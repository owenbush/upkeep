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
}
