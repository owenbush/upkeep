<?php

declare(strict_types=1);

/**
 * Generates the meta.yml answers the Go port is held to.
 *
 * meta.yml is written by whichever implementation built the artifact set and
 * read by whichever one uses it, so the two have to agree about which sidecars
 * are readable and what they say. The skew comparison is included because a
 * disagreement there silently reuses a tree built on the wrong PHP.
 *
 *   php meta_expect.php
 */

require __DIR__ . '/vendor/autoload.php';

use Upkeep\BaseArtifact\ArtifactMeta;

$out = [];
foreach (glob(__DIR__ . '/testdata/metas/*.yml') as $file) {
    $name = basename($file);
    try {
        $meta = ArtifactMeta::fromYaml((string) file_get_contents($file));
        $out[$name] = [
            'accepted' => true,
            'core_version' => $meta->coreVersion,
            'core_major' => $meta->coreMajor,
            'php_version' => $meta->phpVersion,
            'db_engine' => $meta->dbEngine,
            'built_at' => $meta->builtAt->format(\DateTimeInterface::ATOM),
            // Round-tripped, because what one side writes the other must read.
            'yaml' => $meta->toYaml(),
            // A few live comparisons, so a divergence about what counts as
            // skew is caught rather than assumed away.
            'skew' => [
                'same' => $meta->skewAgainst($meta->phpVersion, $meta->dbEngine),
                'patch_only' => $meta->skewAgainst(
                    preg_replace('/\.\d+$/', '.99', $meta->phpVersion) ?? $meta->phpVersion,
                    $meta->dbEngine,
                ),
                'minor' => $meta->skewAgainst('7.0.0', $meta->dbEngine),
                'engine' => $meta->skewAgainst($meta->phpVersion, 'sqlite'),
            ],
        ];
    } catch (\Throwable $e) {
        // The message is not recorded: the two word their refusals
        // differently, and what has to agree is which sidecars are readable.
        $out[$name] = ['accepted' => false];
    }
}

file_put_contents(
    __DIR__ . '/testdata/metas/expected.json',
    json_encode($out, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n",
);
printf("%d fixtures\n", count($out));
