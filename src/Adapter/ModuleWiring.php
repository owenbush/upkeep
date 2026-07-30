<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Wires the module working copy into a seeded project via a Composer path
 * repository.
 *
 * Chosen over the engine's symlink-project/poser mechanism because upkeep
 * seeds the full project tree from the canonical base artifact (the engine's
 * mechanism assumes the module IS the project root, which cannot host the
 * per-(module x core) matrix from one checkout). A path repository with
 * symlink:true makes Composer install the module as a symlink into
 * web/modules/contrib/<module> and resolve its dependencies properly, while
 * Composer never owns or writes into the checkout itself — `apply_mr` can
 * mutate the working copy freely and no composer operation will clobber it.
 */
final readonly class ModuleWiring
{
    /**
     * The dev version Composer assigns to a checked-out branch of a path
     * repository, per Composer's own branch normalization: version-like
     * branches ("1.0.x", "2.x", "11.1") become "<branch>-dev", anything else
     * ("main", "8.x-1.x") becomes "dev-<branch>". Requiring exactly this
     * constraint pins the working copy's branch and can never drift to a
     * different dev branch published on packages.drupal.org.
     */
    public static function devConstraintForBranch(string $branch): string
    {
        return preg_match('/^v?\d+(\.(\d+|[xX*]))*$/', $branch) === 1
            ? $branch . '-dev'
            : 'dev-' . $branch;
    }

    /**
     * Returns composer.json content with the path repository prepended, so it
     * outranks packages.drupal.org (Composer honors repository order; the
     * first repository providing a package wins).
     */
    public static function withPathRepository(string $composerJson, string $url): string
    {
        try {
            $data = json_decode($composerJson, true, flags: \JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            throw new AdapterException('Project composer.json is not parseable: ' . $e->getMessage(), previous: $e);
        }
        if (!is_array($data)) {
            throw new AdapterException('Project composer.json must decode to an object.');
        }

        $repository = ['type' => 'path', 'url' => $url, 'options' => ['symlink' => true]];

        $repositories = $data['repositories'] ?? [];
        if (!is_array($repositories) || !array_is_list($repositories)) {
            throw new AdapterException('Project composer.json "repositories" must be a list; refusing to rewrite it.');
        }

        $repositories = array_values(array_filter(
            $repositories,
            static fn (array $repo): bool => !(($repo['type'] ?? '') === 'path' && ($repo['url'] ?? '') === $url),
        ));
        array_unshift($repositories, $repository);
        $data['repositories'] = $repositories;

        return json_encode($data, \JSON_PRETTY_PRINT | \JSON_UNESCAPED_SLASHES | \JSON_THROW_ON_ERROR) . "\n";
    }
}
