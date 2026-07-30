<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\ProjectName;

final class ProjectNameTest extends TestCase
{
    public function testEncodesModuleAndCoreMajor(): void
    {
        self::assertSame('upkeep-token-or-d11', ProjectName::for('token_or', '11'));
    }

    public function testTranslatesEveryUnderscoreToHyphenForDnsSafety(): void
    {
        self::assertSame('upkeep-conditions-helper-d10', ProjectName::for('conditions_helper', '10'));
    }

    public function testRejectsInvalidModuleMachineName(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        ProjectName::for('Not A Module!', '11');
    }

    public function testRejectsEmptyModuleName(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        ProjectName::for('', '11');
    }

    public function testRejectsNonNumericCoreVersion(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        ProjectName::for('token_or', '11.x');
    }
}
