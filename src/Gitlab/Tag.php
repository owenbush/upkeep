<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

final readonly class Tag
{
    public function __construct(
        public string $name,
        public ?string $commitSha,
        public ?\DateTimeImmutable $createdAt,
    ) {
    }

    /**
     * @param array<array-key, mixed> $data a decoded tag JSON object
     */
    public static function fromApi(array $data): self
    {
        $payload = new ApiPayload($data);
        $commit = $payload->child('commit');

        $createdAt = null;
        $raw = $commit?->stringOrNull('created_at') ?? $commit?->stringOrNull('committed_date');
        if ($raw !== null) {
            try {
                $createdAt = new \DateTimeImmutable($raw);
            } catch (\Exception) {
                $createdAt = null;
            }
        }

        return new self(
            name: $payload->string('name'),
            commitSha: $commit?->stringOrNull('id') ?? $payload->stringOrNull('target'),
            createdAt: $createdAt,
        );
    }
}
