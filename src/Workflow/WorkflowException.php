<?php

declare(strict_types=1);

namespace Upkeep\Workflow;

/**
 * Raised when a single-MR workflow cannot even start: unknown module,
 * untracked core version, unresolvable or non-open merge request. Commands
 * map it to the infrastructure exit code (ExitCode::INFRASTRUCTURE) — it is
 * never a check verdict.
 */
final class WorkflowException extends \RuntimeException
{
}
