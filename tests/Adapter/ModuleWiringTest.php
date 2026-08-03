<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\ModuleWiring;

final class ModuleWiringTest extends TestCase
{
    /** Shape of drupal/recommended-project's composer.json repositories. */
    private const PROJECT_COMPOSER_JSON = <<<'JSON'
        {
            "name": "drupal/recommended-project",
            "repositories": [
                {
                    "type": "composer",
                    "url": "https://packages.drupal.org/8"
                }
            ],
            "require": {
                "drupal/core-recommended": "^11.4"
            }
        }
        JSON;

    public function testPrependsThePathRepositoryAheadOfDrupalPackagist(): void
    {
        $result = json_decode(ModuleWiring::withPathRepository(self::PROJECT_COMPOSER_JSON, './module'), true);

        self::assertSame(
            ['type' => 'path', 'url' => './module', 'options' => ['symlink' => true]],
            $result['repositories'][0],
            'The path repository must come first so it always wins package resolution.',
        );
        self::assertSame('https://packages.drupal.org/8', $result['repositories'][1]['url']);
    }

    public function testPreservesEveryOtherComposerKey(): void
    {
        $result = json_decode(ModuleWiring::withPathRepository(self::PROJECT_COMPOSER_JSON, './module'), true);

        self::assertSame('drupal/recommended-project', $result['name']);
        self::assertSame(['drupal/core-recommended' => '^11.4'], $result['require']);
    }

    public function testIsIdempotentForTheSameUrl(): void
    {
        $once = ModuleWiring::withPathRepository(self::PROJECT_COMPOSER_JSON, './module');

        $result = json_decode(ModuleWiring::withPathRepository($once, './module'), true);
        $pathRepos = array_filter(
            $result['repositories'],
            static fn (array $repo): bool => ($repo['type'] ?? '') === 'path',
        );

        self::assertCount(1, $pathRepos);
    }

    public function testNamedBranchesGetTheDevPrefix(): void
    {
        self::assertSame('dev-main', ModuleWiring::devConstraintForBranch('main'));
        self::assertSame('dev-8.x-1.x', ModuleWiring::devConstraintForBranch('8.x-1.x'));
        // applyMr pins the working copy's mr-<iid> branch through the same rule.
        self::assertSame('dev-mr-2', ModuleWiring::devConstraintForBranch('mr-2'));
    }

    public function testVersionLikeBranchesGetTheDevSuffixPerComposerNormalization(): void
    {
        self::assertSame('1.0.x-dev', ModuleWiring::devConstraintForBranch('1.0.x'));
        self::assertSame('2.x-dev', ModuleWiring::devConstraintForBranch('2.x'));
        self::assertSame('11.1-dev', ModuleWiring::devConstraintForBranch('11.1'));
    }

    public function testRejectsUnparseableComposerJson(): void
    {
        $this->expectException(AdapterException::class);
        ModuleWiring::withPathRepository('not json', './module');
    }
}
