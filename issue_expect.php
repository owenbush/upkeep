<?php

declare(strict_types=1);

/**
 * Generates the issue-parsing answers the Go port is held to.
 *
 * Two reasons this one matters more than it looks. The category is an integer
 * id in the payload and a label on the issue page, so an implementation that
 * passes the id through prints "1" where the other prints "Bug report". And
 * the dashboard snapshot stores these payloads verbatim: an issue payload
 * written by one implementation is read back by the other, so the attachment
 * envelope, the resolved-file shape and the round-trip have to agree.
 *
 *   php issue_expect.php
 */

require __DIR__ . '/../vendor/autoload.php';

use Upkeep\Drupal\Issue;

$out = [];
foreach (glob(__DIR__ . '/testdata/issues/*.json') as $file) {
    $name = basename($file);
    $data = json_decode((string) file_get_contents($file), true);
    $issue = is_array($data) ? Issue::fromApi($data) : null;

    if ($issue === null) {
        $out[$name] = ['parsed' => false];

        continue;
    }

    $files = [];
    foreach ($issue->files as $attachment) {
        $files[] = [
            'name' => $attachment->name,
            'url' => $attachment->url,
            'size' => $attachment->size,
            'timestamp' => $attachment->timestamp,
            'owner_uid' => $attachment->ownerUid,
            'is_patch' => $attachment->isPatch(),
            'comment_number' => $attachment->commentNumber(),
        ];
    }

    $latest = $issue->latestPatch();
    $out[$name] = [
        'parsed' => true,
        'nid' => $issue->nid,
        'title' => $issue->title,
        'status' => $issue->status->value,
        'url' => $issue->url,
        'project' => $issue->project,
        'priority' => $issue->priority,
        'priority_label' => $issue->priorityLabel(),
        'version' => $issue->version,
        'component' => $issue->component,
        'category' => $issue->category,
        'patch_count' => $issue->patchCount(),
        'latest_patch' => $latest?->name,
        'files' => $files,
        // The round trip the snapshot depends on.
        'round_tripped' => Issue::fromApi($issue->toApiArray())?->toApiArray(),
    ];
}

file_put_contents(
    __DIR__ . '/testdata/issues/expected.json',
    json_encode($out, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE) . "\n",
);
printf("%d fixtures\n", count($out));
