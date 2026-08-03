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

    /**
     * @param array<array-key, mixed> $data a decoded project JSON object
     */
    public static function fromApi(array $data): self
    {
        $payload = new ApiPayload($data);

        return new self(
            id: $payload->int('id'),
            path: $payload->string('path'),
            pathWithNamespace: $payload->string('path_with_namespace'),
            name: $payload->string('name'),
            webUrl: $payload->string('web_url'),
        );
    }

    /** @return array<string, mixed> */
    public function toApiArray(): array
    {
        return [
            'id' => $this->id,
            'path' => $this->path,
            'path_with_namespace' => $this->pathWithNamespace,
            'name' => $this->name,
            'web_url' => $this->webUrl,
        ];
    }
}
