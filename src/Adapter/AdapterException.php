<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Raised when the engine adapter cannot provision, reuse, query, or dispose
 * an environment (failed engine command, missing base artifacts, broken
 * project state).
 */
final class AdapterException extends \RuntimeException
{
}
