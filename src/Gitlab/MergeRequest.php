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
        public ?string $updatedAt = null,
    ) {
    }

    /**
     * @param array<array-key, mixed> $data a decoded merge-request JSON object
     */
    public static function fromApi(array $data): self
    {
        $payload = new ApiPayload($data);
        $author = $payload->child('author');
        $headPipeline = $payload->childArray('head_pipeline');
        $title = $payload->string('title');

        return new self(
            iid: $payload->int('iid'),
            title: $title,
            state: $payload->string('state'),
            authorUsername: $author?->string('username') ?? '',
            authorId: $author?->intOrNull('id'),
            sourceBranch: $payload->string('source_branch'),
            targetBranch: $payload->string('target_branch'),
            draft: $payload->bool('draft', str_starts_with($title, 'Draft: ')),
            detailedMergeStatus: $payload->stringOrNull('detailed_merge_status'),
            headSha: $payload->stringOrNull('sha'),
            webUrl: $payload->string('web_url'),
            description: $payload->stringOrNull('description'),
            headPipeline: $headPipeline !== null ? Pipeline::fromApi($headPipeline) : null,
            updatedAt: $payload->stringOrNull('updated_at'),
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
            'updated_at' => $this->updatedAt,
        ];
    }
}
