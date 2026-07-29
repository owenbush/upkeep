<?php

declare(strict_types=1);

namespace Upkeep\Tests\BaseArtifact;

use PHPUnit\Framework\TestCase;
use Upkeep\BaseArtifact\ComposerLock;
use Upkeep\BaseArtifact\MetaException;

final class ComposerLockTest extends TestCase
{
    public function testExtractsExactCoreVersion(): void
    {
        $lock = json_encode([
            'packages' => [
                ['name' => 'drupal/core-recommended', 'version' => '11.4.4'],
                ['name' => 'drupal/core', 'version' => '11.4.4'],
                ['name' => 'symfony/console', 'version' => 'v7.3.1'],
            ],
        ]);

        self::assertSame('11.4.4', ComposerLock::coreVersion($lock));
    }

    public function testThrowsWhenCoreAbsent(): void
    {
        $this->expectException(MetaException::class);
        $this->expectExceptionMessage('drupal/core');

        ComposerLock::coreVersion(json_encode(['packages' => [['name' => 'symfony/console', 'version' => 'v7.3.1']]]));
    }

    public function testThrowsOnInvalidJson(): void
    {
        $this->expectException(MetaException::class);

        ComposerLock::coreVersion('not json');
    }
}
