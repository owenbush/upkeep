<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

final readonly class Issue
{
    /**
     * @param list<IssueFile> $files
     */
    public function __construct(
        public int $nid,
        public string $title,
        public IssueStatus $status,
        public string $url,
        public ?string $project,
        public ?int $priority,
        public ?string $version,
        public ?string $component,
        public ?string $category,
        public array $files = [],
    ) {
    }

    /**
     * @param array<array-key, mixed> $data a decoded drupal.org issue payload
     */
    public static function fromApi(array $data): ?self
    {
        $payload = new ApiPayload($data);

        // An issue without a usable node id, or with a status upkeep does not
        // understand, is not an issue it can reason about — no partial model
        // is built for it.
        $nid = $payload->intOrNull('nid');
        if ($nid === null) {
            return null;
        }

        $statusId = $payload->intOrNull('field_issue_status');
        $status = $statusId !== null ? IssueStatus::tryFrom($statusId) : null;
        if ($status === null) {
            return null;
        }

        return new self(
            nid: $nid,
            title: $payload->string('title'),
            status: $status,
            url: $payload->stringOrNull('url') ?? sprintf('https://www.drupal.org/node/%d', $nid),
            project: $payload->child('field_project')?->stringOrNull('machine_name'),
            priority: $payload->intOrNull('field_issue_priority'),
            version: $payload->stringOrNull('field_issue_version'),
            component: $payload->stringOrNull('field_issue_component'),
            category: self::categoryLabel($payload->intOrNull('field_issue_category')),
            files: self::parseFiles($payload),
        );
    }

    /** @return array<string, mixed> */
    public function toApiArray(): array
    {
        return [
            'nid' => $this->nid,
            'title' => $this->title,
            'field_issue_status' => (string) $this->status->value,
            'url' => $this->url,
            'field_project' => $this->project !== null ? ['machine_name' => $this->project] : null,
            'field_issue_priority' => $this->priority,
            'field_issue_version' => $this->version,
            'field_issue_component' => $this->component,
            'field_issue_category' => self::categoryId($this->category),
            'field_issue_files' => array_map(
                static fn (IssueFile $f): array => $f->toApiArray(),
                $this->files,
            ),
        ];
    }

    public function patchCount(): int
    {
        return \count(array_filter($this->files, static fn (IssueFile $f): bool => $f->isPatch()));
    }

    public function latestPatch(): ?IssueFile
    {
        $patches = array_filter($this->files, static fn (IssueFile $f): bool => $f->isPatch());
        if ($patches === []) {
            return null;
        }

        $patches = array_values($patches);
        usort($patches, static function (IssueFile $a, IssueFile $b): int {
            $ca = $a->commentNumber();
            $cb = $b->commentNumber();
            if ($ca !== null && $cb !== null) {
                return $cb <=> $ca;
            }

            return $b->timestamp <=> $a->timestamp;
        });

        return $patches[0];
    }

    public function priorityLabel(): ?string
    {
        return match ($this->priority) {
            400 => 'Critical',
            300 => 'Major',
            200 => 'Normal',
            100 => 'Minor',
            default => null,
        };
    }

    private static function categoryLabel(?int $id): ?string
    {
        return match ($id) {
            1 => 'Bug report',
            2 => 'Task',
            3 => 'Feature request',
            4 => 'Support request',
            5 => 'Plan',
            default => null,
        };
    }

    private static function categoryId(?string $label): ?int
    {
        return match ($label) {
            'Bug report' => 1,
            'Task' => 2,
            'Feature request' => 3,
            'Support request' => 4,
            'Plan' => 5,
            default => null,
        };
    }

    /**
     * The API returns attachments either as a plain list or wrapped in the
     * Drupal 7 field-language envelope (`{"und": [...]}`) depending on the
     * endpoint; both are read.
     *
     * @return list<IssueFile>
     */
    private static function parseFiles(ApiPayload $payload): array
    {
        $attachments = $payload->child('field_issue_files');
        if ($attachments === null) {
            return [];
        }

        $raw = $attachments->childArray('und') ?? $payload->childArray('field_issue_files') ?? [];

        $files = [];
        foreach ($raw as $entry) {
            if (!\is_array($entry)) {
                continue;
            }
            $file = IssueFile::fromApi($entry);
            if ($file !== null) {
                $files[] = $file;
            }
        }

        return $files;
    }
}
