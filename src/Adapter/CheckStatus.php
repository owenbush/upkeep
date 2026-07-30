<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Outcome status of one check. Richer than pass/fail because two non-failure
 * states must stay visible in reports instead of being silently folded away:
 * a module legitimately having no tests, and a check the engine cannot
 * provide for the target core.
 */
enum CheckStatus: string
{
    case Passed = 'passed';
    case Failed = 'failed';

    /** The check ran but discovered nothing to run (e.g. module has no tests/). */
    case NoTests = 'no-tests';

    /** The engine provides no way to run this check for the target core. */
    case Unavailable = 'unavailable';

    /**
     * Whether this status blocks an all-green verdict: only a real failure
     * does. NoTests and Unavailable are honest, visible non-failures.
     */
    public function passed(): bool
    {
        return $this !== self::Failed;
    }
}
