<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * A merge request as the dashboard/gate consume it.
 *
 * Exposes exactly the fields the fast-lane gate keys on for bot-MR
 * classification: author username (+ id), source branch, title, draft state,
 * detailed_merge_status, and head SHA.
 */
final readonly class MergeRequest
{
    public function __construct(
        public int $iid,
        public string $title,
        public string $state,
        public string $authorUsername,
        public ?int $authorId,
        public string $sourceBranch,
        public string $targetBranch,
        public bool $draft,
        public ?string $detailedMergeStatus,
        public ?string $headSha,
        public string $webUrl,
        public ?string $description = null,
        public ?Pipeline $headPipeline = null,
    ) {
    }

    public static function fromApi(array $data): self
    {
        $title = (string) ($data['title'] ?? '');

        return new self(
            iid: (int) $data['iid'],
            title: $title,
            state: (string) ($data['state'] ?? ''),
            authorUsername: (string) ($data['author']['username'] ?? ''),
            authorId: isset($data['author']['id']) ? (int) $data['author']['id'] : null,
            sourceBranch: (string) ($data['source_branch'] ?? ''),
            targetBranch: (string) ($data['target_branch'] ?? ''),
            draft: (bool) ($data['draft'] ?? str_starts_with($title, 'Draft: ')),
            detailedMergeStatus: isset($data['detailed_merge_status']) ? (string) $data['detailed_merge_status'] : null,
            headSha: isset($data['sha']) ? (string) $data['sha'] : null,
            webUrl: (string) ($data['web_url'] ?? ''),
            description: isset($data['description']) ? (string) $data['description'] : null,
            headPipeline: isset($data['head_pipeline']) && \is_array($data['head_pipeline'])
                ? Pipeline::fromApi($data['head_pipeline'])
                : null,
        );
    }

    /** @return array<string, mixed> */
    public function toApiArray(): array
    {
        return [
            'iid' => $this->iid,
            'title' => $this->title,
            'state' => $this->state,
            'author' => ['username' => $this->authorUsername, 'id' => $this->authorId],
            'source_branch' => $this->sourceBranch,
            'target_branch' => $this->targetBranch,
            'draft' => $this->draft,
            'detailed_merge_status' => $this->detailedMergeStatus,
            'sha' => $this->headSha,
            'web_url' => $this->webUrl,
            'description' => $this->description,
            'head_pipeline' => $this->headPipeline?->toApiArray(),
        ];
    }
}
