<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

enum IssueStatus: int
{
    case Active = 1;
    case Fixed = 2;
    case ClosedDuplicate = 3;
    case Postponed = 4;
    case ClosedWontFix = 5;
    case ClosedWorksAsDesigned = 6;
    case ClosedFixed = 7;
    case NeedsReview = 8;
    case NeedsWork = 13;
    case Rtbc = 14;
    case PatchToBePorted = 15;
    case PostponedNeedsInfo = 16;
    case ClosedOutdated = 17;
    case ClosedCannotReproduce = 18;

    /**
     * Every status an issue can hold while it is still someone's problem.
     *
     * The canonical list, because "which issues does upkeep look at?" used to
     * be answered separately by each command and both said Needs Review and
     * RTBC — the two statuses a *contribution* sits in. That made the tool
     * blind to the 55% of a project's open queue where work has not started
     * yet: on pathauto, 17 Active, 18 Needs work and 16 Postponed issues were
     * invisible, which is precisely where a maintainer's own work begins.
     *
     * @return list<self>
     */
    public static function open(): array
    {
        return array_values(array_filter(self::cases(), static fn (self $s): bool => $s->isOpen()));
    }

    /**
     * The subset carrying a contribution to review — the historical scan, kept
     * because "what needs reviewing?" is still a distinct question from "what
     * is open?".
     *
     * @return list<self>
     */
    public static function awaitingReview(): array
    {
        return [self::NeedsReview, self::Rtbc];
    }

    /** Whether the issue is still live, as opposed to resolved or abandoned. */
    public function isOpen(): bool
    {
        return match ($this) {
            self::Fixed, self::ClosedDuplicate, self::ClosedWontFix, self::ClosedWorksAsDesigned,
            self::ClosedFixed, self::ClosedOutdated, self::ClosedCannotReproduce => false,
            default => true,
        };
    }

    /**
     * Whether the ball is with the maintainer rather than the contributor.
     * Needs review and RTBC await a maintainer's verdict; Active is unclaimed;
     * Needs work and the postponed statuses are waiting on somebody else.
     */
    public function needsMaintainer(): bool
    {
        return $this === self::NeedsReview || $this === self::Rtbc;
    }

    public function label(): string
    {
        return match ($this) {
            self::Active => 'Active',
            self::Fixed => 'Fixed',
            self::ClosedDuplicate => 'Closed (duplicate)',
            self::Postponed => 'Postponed',
            self::ClosedWontFix => "Closed (won't fix)",
            self::ClosedWorksAsDesigned => 'Closed (works as designed)',
            self::ClosedFixed => 'Closed (fixed)',
            self::NeedsReview => 'Needs review',
            self::NeedsWork => 'Needs work',
            self::Rtbc => 'Reviewed & tested by the community',
            self::PatchToBePorted => 'Patch (to be ported)',
            self::PostponedNeedsInfo => 'Postponed (maintainer needs more info)',
            self::ClosedOutdated => 'Closed (outdated)',
            self::ClosedCannotReproduce => 'Closed (cannot reproduce)',
        };
    }

    public function shortLabel(): string
    {
        return match ($this) {
            self::Active => 'active',
            self::Fixed => 'fixed',
            self::ClosedDuplicate, self::ClosedWontFix, self::ClosedWorksAsDesigned,
            self::ClosedFixed, self::ClosedOutdated, self::ClosedCannotReproduce => 'closed',
            self::Postponed, self::PostponedNeedsInfo => 'postponed',
            self::NeedsReview => 'review',
            self::NeedsWork => 'needs work',
            self::Rtbc => 'RTBC',
            self::PatchToBePorted => 'to port',
        };
    }
}
