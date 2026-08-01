<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

final readonly class Pipeline
{
    public function __construct(
        public int $id,
        public PipelineStatus $status,
        public string $rawStatus,
        public ?string $sha,
        public string $webUrl,
    ) {
    }

    public static function fromApi(array $data): self
    {
        $rawStatus = (string) ($data['status'] ?? '');

        return new self(
            id: (int) ($data['id'] ?? 0),
            status: PipelineStatus::fromApi($rawStatus),
            rawStatus: $rawStatus,
            sha: isset($data['sha']) ? (string) $data['sha'] : null,
            webUrl: (string) ($data['web_url'] ?? ''),
        );
    }

    /** @return array<string, mixed> */
    public function toApiArray(): array
    {
        return [
            'id' => $this->id,
            'status' => $this->rawStatus,
            'sha' => $this->sha,
            'web_url' => $this->webUrl,
        ];
    }
}
