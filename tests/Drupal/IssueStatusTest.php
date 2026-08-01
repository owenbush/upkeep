<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\IssueStatus;

final class IssueStatusTest extends TestCase
{
    public function testAllStatusesHaveLabels(): void
    {
        foreach (IssueStatus::cases() as $status) {
            self::assertNotEmpty($status->label(), sprintf('%s->label() must not be empty', $status->name));
            self::assertNotEmpty($status->shortLabel(), sprintf('%s->shortLabel() must not be empty', $status->name));
        }
    }

    public function testKeyStatusIds(): void
    {
        self::assertSame(8, IssueStatus::NeedsReview->value);
        self::assertSame(13, IssueStatus::NeedsWork->value);
        self::assertSame(14, IssueStatus::Rtbc->value);
        self::assertSame(2, IssueStatus::Fixed->value);
    }

    public function testShortLabelForDashboardColumn(): void
    {
        self::assertSame('review', IssueStatus::NeedsReview->shortLabel());
        self::assertSame('needs work', IssueStatus::NeedsWork->shortLabel());
        self::assertSame('RTBC', IssueStatus::Rtbc->shortLabel());
    }
}
