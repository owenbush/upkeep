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
