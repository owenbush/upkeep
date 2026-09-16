<?php

declare(strict_types=1);

/**
 * Generates the filename-reduction answers the Go port is held to.
 *
 * The name arrives from a remote API and becomes a path segment, so this is
 * the one place in the patch surface where a divergence is a security
 * difference rather than a cosmetic one — and the two implementations cache
 * under the name this produces, so they must agree on it byte for byte.
 *
 *   php safename_expect.php
 */

require __DIR__ . '/vendor/autoload.php';

use Upkeep\Patches\PatchFetcher;

$names = [
    '../../etc/passwd',
    '..%2F..%2Fpasswd',
    '..\..\windows\system32',
    '/absolute/path.patch',
    '...',
    '',
    '..',
    '.',
    '.hidden',
    "space and 'quotes'.patch",
    '3597808-9-d11.patch',
    'a/b/c.patch',
    '....//x',
    // Multi-byte: PHP's regex works on bytes, so one accented character
    // becomes two underscores. Go's regexp works on runes and would make one.
    'été.patch',
    'ünïcödé.diff',
    '日本語.patch',
    "x\ny.patch",
    "tab\there.patch",
    'CON.patch',
    '-rf.patch',
    '--upload-pack=evil.patch',
    'x'  . str_repeat('y', 300) . '.patch',
    'no-extension',
    '.patch',
    '£$%^&*().patch',
    'trailing.dots...',
    "nul\0byte.patch",
];

$out = [];
foreach ($names as $name) {
    $out[] = ['name' => $name, 'safe' => PatchFetcher::safeName($name)];
}

file_put_contents(
    __DIR__ . '/testdata/safenames.json',
    json_encode($out, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE) . "\n",
);
printf("%d names\n", count($out));
