<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * A merge request as the dashboard/gate consume it.
 *
 * Exposes exactly the fields the fast-lane gate keys on for bot-MR
 * classification: author username (+ id), source branch, title, draft state,
 * detailed_merge_status, and head SHA — plus the diff refs that say whether
 * the MR carries any changes at all (see carriesChanges()).
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
        public ?string $diffBaseSha = null,
        public ?string $diffHeadSha = null,
        /**
         * The project holding the source branch, which on drupal.org is
         * almost never the project the merge request targets — it is the
         * issue fork. Null when the payload does not carry it.
         */
        public ?int $sourceProjectId = null,
        /**
         * When it merged, for a merged MR. The evidence that an open issue's
         * work has already landed — which a Project Update Bot compatibility
         * issue, kept open by convention, cannot tell you itself.
         */
        public ?string $mergedAt = null,
    ) {
    }

    /**
     * Whether this merge request carries any changes, or null when the payload
     * it was built from cannot say.
     *
     * An MR whose branch holds no commits the target does not already have is
     * an *empty* MR: it exists, it can be linked from an issue, and it covers
     * nothing. The Project Update Bot opens exactly such an MR on a great many
     * contrib projects when it finds nothing to fix.
     *
     * The signal is `diff_refs.base_sha == head_sha`, deliberately, because the
     * two more obvious fields both lie:
     *   - `changes_count` is *null* on an empty MR, indistinguishable from
     *     "the diff has not been generated yet";
     *   - `detailed_merge_status` reports `draft_status` for a draft MR,
     *     masking the emptiness underneath (and a draft carrying real commits
     *     is not empty at all).
     *
     * Null means "not known from this payload" rather than "not empty": the
     * merge-request *list* endpoint omits diff_refs entirely, so only the
     * single-MR endpoint (GitlabClient::mergeRequest()) can settle it. Callers
     * must decide what an unknown means for them; none may read it as false.
     */
    public function carriesChanges(): ?bool
    {
        if ($this->diffBaseSha === null || $this->diffHeadSha === null) {
            return null;
        }

        return $this->diffBaseSha !== $this->diffHeadSha;
    }

    /**
     * The most recently merged of a set of merge requests, if any.
     *
     * The question a still-open drupal.org issue cannot answer about itself.
     * Project Update Bot compatibility issues are kept open on purpose so the
     * bot can post again as core moves, so an open one may have had its real
     * work merged months ago — and a maintainer staring at the bot's leftover
     * draft has no way to tell. Shared by Patches\\Contribution and by the
     * dashboard row, because a landing shown in one view and not the other is
     * worse than not showing it at all.
     *
     * @param list<self> $mergeRequests
     */
    public static function latestMerged(array $mergeRequests): ?self
    {
        $latest = null;
        foreach ($mergeRequests as $mr) {
            if ($mr->state !== 'merged') {
                continue;
            }
            if ($latest === null || (string) $mr->mergedAt > (string) $latest->mergedAt) {
                $latest = $mr;
            }
        }

        return $latest;
    }

    /**
     * @param array<array-key, mixed> $data a decoded merge-request JSON object
     */
    public static function fromApi(array $data): self
    {
        $payload = new ApiPayload($data);
        $author = $payload->child('author');
        $headPipeline = $payload->childArray('head_pipeline');
        $diffRefs = $payload->child('diff_refs');
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
            diffBaseSha: $diffRefs?->stringOrNull('base_sha'),
            diffHeadSha: $diffRefs?->stringOrNull('head_sha'),
            sourceProjectId: $payload->intOrNull('source_project_id'),
            mergedAt: $payload->stringOrNull('merged_at'),
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
            'source_project_id' => $this->sourceProjectId,
            'merged_at' => $this->mergedAt,
            // Round-tripped so a cached snapshot answers carriesChanges()
            // without refetching. Null when unknown, which reads back as
            // unknown rather than as "not empty".
            'diff_refs' => $this->diffBaseSha === null && $this->diffHeadSha === null
                ? null
                : ['base_sha' => $this->diffBaseSha, 'head_sha' => $this->diffHeadSha],
        ];
    }
}
