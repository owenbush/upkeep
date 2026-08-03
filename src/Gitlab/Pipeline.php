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

    /**
     * @param array<array-key, mixed> $data a decoded pipeline JSON object
     */
    public static function fromApi(array $data): self
    {
        $payload = new ApiPayload($data);
        $rawStatus = $payload->string('status');

        return new self(
            id: $payload->int('id'),
            status: PipelineStatus::fromApi($rawStatus),
            rawStatus: $rawStatus,
            sha: $payload->stringOrNull('sha'),
            webUrl: $payload->string('web_url'),
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
