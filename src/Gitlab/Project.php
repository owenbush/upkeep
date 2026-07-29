<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

final readonly class Project
{
    public function __construct(
        public int $id,
        public string $path,
        public string $pathWithNamespace,
        public string $name,
        public string $webUrl,
    ) {
    }

    public static function fromApi(array $data): self
    {
        return new self(
            id: (int) $data['id'],
            path: (string) ($data['path'] ?? ''),
            pathWithNamespace: (string) ($data['path_with_namespace'] ?? ''),
            name: (string) ($data['name'] ?? ''),
            webUrl: (string) ($data['web_url'] ?? ''),
        );
    }
}
