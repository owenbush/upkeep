<?php

declare(strict_types=1);

/**
 * Generates the registry-loading answers the Go port is held to.
 *
 * registry.yml is a file both implementations read and write, so a disagreement
 * about which ones are valid is a disagreement about the user's own config.
 * YAML parsers differ at exactly the edges this file lives on — unquoted
 * numbers, a null mapping, a scalar where a list belongs — so the fixtures are
 * loaded through the real PHP loader and the answers committed.
 *
 *   php registry_expect.php
 */

require __DIR__ . '/../vendor/autoload.php';
use Upkeep\Cockpit\ModuleRegistry;
$out = [];
foreach (glob(__DIR__ . '/testdata/registries/*.yml') as $file) {
    $name = basename($file);
    try {
        $registry = ModuleRegistry::fromFile($file);
        $modules = [];
        foreach ($registry->modules() as $key => $module) {
            $modules[(string) $key] = [
                'name' => $module->name,
                'project' => $module->project,
                'core_versions' => $module->coreVersions,
                'watched' => $module->watched,
            ];
        }
        // Cast, because PHP encodes an empty array as [] and the answers
        // file has to stay a mapping whether or not the registry was empty.
        $out[$name] = ['accepted' => true, 'modules' => (object) $modules];
    } catch (\Throwable $e) {
        // The message is deliberately not recorded. It names the absolute
        // path of the fixture, which would make this file machine-dependent —
        // and the two implementations word their refusals differently anyway.
        // What has to agree is *which* registries are accepted, and what the
        // accepted ones parse to.
        $out[$name] = ['accepted' => false];
    }
}
file_put_contents(__DIR__ . '/testdata/registries/expected.json', json_encode($out, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n");
printf("%d fixtures\n", count($out));
