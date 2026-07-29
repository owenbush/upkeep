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

    public static function fromApi(array $data): self
    {
        $createdAt = null;
        $raw = $data['commit']['created_at'] ?? $data['commit']['committed_date'] ?? null;
        if (\is_string($raw)) {
            try {
                $createdAt = new \DateTimeImmutable($raw);
            } catch (\Exception) {
                $createdAt = null;
            }
        }

        return new self(
            name: (string) ($data['name'] ?? ''),
            commitSha: isset($data['commit']['id']) ? (string) $data['commit']['id']
                : (isset($data['target']) ? (string) $data['target'] : null),
            createdAt: $createdAt,
        );
    }
}
