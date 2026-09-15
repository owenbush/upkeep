<?php

declare(strict_types=1);

/**
 * Generates the dashboard-snapshot answers the Go port is held to, and the
 * canonical PHP-written snapshot it must read.
 *
 * The snapshot is a cache file on disk that outlives whichever binary wrote
 * it, so both implementations have to read each other's. It is also untrusted
 * input by the same argument, which is why the malformed fixtures matter as
 * much as the good one.
 *
 *   php snapshot_expect.php
 */

require __DIR__ . '/../vendor/autoload.php';

use Upkeep\Dashboard\ModuleSnapshot;

$dir = __DIR__ . '/testdata/snapshots';

// The canonical one, written by PHP for Go to read.
$snapshot = new ModuleSnapshot(
    new \DateTimeImmutable('2026-06-12T10:00:00+00:00'),
    json_decode((string) file_get_contents($dir . '/../project.json'), true),
    [json_decode((string) file_get_contents($dir . '/../mr.json'), true)],
    [json_decode((string) file_get_contents($dir . '/../issues/resolved-attachments.json'), true)],
    [json_decode((string) file_get_contents($dir . '/../merged-mr.json'), true)],
    [501 => 3603341, 502 => 3597808],
    ['2.0.x' => '^10.2 || ^11 || ^12', '1.0.x' => '^10'],
    [12 => 'deadbeefcafe', 9 => 'abc1234'],
);
file_put_contents($dir . '/php-written.json', $snapshot->toJson() . "\n");

$out = [];
foreach (glob($dir . '/*.json') as $file) {
    $name = basename($file);
    if ($name === 'expected.json') {
        continue;
    }
    $read = ModuleSnapshot::fromJson((string) file_get_contents($file));
    if ($read === null) {
        $out[$name] = ['read' => false];

        continue;
    }

    $out[$name] = [
        'read' => true,
        'fetched_at' => $read->fetchedAt->format(\DateTimeInterface::ATOM),
        'project_id' => $read->project()->id,
        'project_path' => $read->project()->pathWithNamespace,
        'merge_request_iids' => array_map(static fn ($mr) => $mr->iid, $read->mergeRequests()),
        'merged_iids' => array_map(static fn ($mr) => $mr->iid, $read->mergedMergeRequests()),
        'issue_nids' => array_map(static fn ($issue) => $issue->nid, $read->patchIssues()),
        'issue_patch_counts' => array_map(static fn ($issue) => $issue->patchCount(), $read->patchIssues()),
        'fork_nids' => (object) $read->forkNids,
        'core_constraints' => (object) $read->coreConstraints,
        'merge_ref_shas' => (object) $read->mergeRefShas,
        'age_labels' => [
            'just now' => $read->ageLabel($read->fetchedAt->modify('+30 seconds')),
            'minutes' => $read->ageLabel($read->fetchedAt->modify('+45 minutes')),
            'hours' => $read->ageLabel($read->fetchedAt->modify('+5 hours')),
            'days' => $read->ageLabel($read->fetchedAt->modify('+9 days')),
        ],
    ];
}

file_put_contents(
    $dir . '/expected.json',
    json_encode($out, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n",
);
printf("%d snapshot fixtures\n", count($out));
