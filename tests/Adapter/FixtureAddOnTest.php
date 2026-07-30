<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\FixtureAddOn;

#[CoversClass(FixtureAddOn::class)]
final class FixtureAddOnTest extends TestCase
{
    protected function tearDown(): void
    {
        putenv(FixtureAddOn::SOURCE_ENV);
    }

    public function testDefaultsToThePublishedAddOn(): void
    {
        putenv(FixtureAddOn::SOURCE_ENV);
        self::assertSame('owenbush/ddev-upkeep', FixtureAddOn::source());
    }

    public function testEnvOverridePointsAtALocalCheckoutForDevelopment(): void
    {
        putenv(FixtureAddOn::SOURCE_ENV . '=/Users/owen/contrib/ddev-upkeep');
        self::assertSame('/Users/owen/contrib/ddev-upkeep', FixtureAddOn::source());
    }
}
