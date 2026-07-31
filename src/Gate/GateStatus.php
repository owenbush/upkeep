<?php

declare(strict_types=1);

namespace Upkeep\Gate;

/**
 * The three derived row statuses on the dashboard. Only ReadyAuto rows are
 * eligible for the fast-lane merge (task 14); everything else needs a human.
 */
enum GateStatus: string
{
    case ReadyAuto = 'READY-AUTO';
    case Review = 'REVIEW';
    case Blocked = 'BLOCKED';
}
