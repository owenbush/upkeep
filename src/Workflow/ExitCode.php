<?php

declare(strict_types=1);

namespace Upkeep\Workflow;

use Upkeep\Adapter\CheckRunResult;

/**
 * The CLI-wide exit-code contract, kept in one place so scripting can rely
 * on it. Every upkeep command answers with one of exactly three codes:
 *
 *   0 — OK: the command did what was asked. (CheckStatus::NoTests and
 *       ::Unavailable are honest non-failures, matching CheckStatus::passed();
 *       so is a documented degraded mode, such as `patches` cross-referencing
 *       fewer merge requests because no token is configured.)
 *   1 — FAILED: upkeep worked, but the work it supervised reported failure —
 *       a red check, a merge GitLab refused, or a non-zero exit from a
 *       command run through `upkeep exec`.
 *   2 — INFRASTRUCTURE: upkeep could not do the job at all, so there is no
 *       verdict to report — bad usage, no cockpit or registry, no GitLab
 *       token, an unregistered module, an untracked core version, an
 *       unresolvable merge request, or an engine/API failure.
 *
 * The distinction that matters to a script: 1 means "look at the subject",
 * 2 means "look at your setup". A command never returns any other value —
 * notably, `upkeep exec` collapses every non-zero child code to 1 rather
 * than passing it through, so a child exiting 2 can never be mistaken for an
 * upkeep infrastructure failure.
 *
 * Command\UpkeepCommand is where the mapping is applied: it turns every
 * domain exception into INFRASTRUCTURE once, for all commands.
 */
final readonly class ExitCode
{
    public const OK = 0;
    public const FAILED = 1;
    public const INFRASTRUCTURE = 2;

    public static function forRun(CheckRunResult $run): int
    {
        return $run->allPassed() ? self::OK : self::FAILED;
    }

    /**
     * A wrapped command's outcome. Every non-zero code collapses to FAILED;
     * a process that produced no exit code at all never ran, which is an
     * infrastructure failure rather than a verdict about the command.
     */
    public static function forChildProcess(?int $childExitCode): int
    {
        return match ($childExitCode) {
            0 => self::OK,
            null => self::INFRASTRUCTURE,
            default => self::FAILED,
        };
    }
}
