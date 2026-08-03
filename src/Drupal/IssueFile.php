<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

final readonly class IssueFile
{
    public function __construct(
        public string $name,
        public string $url,
        public int $size,
        public int $timestamp,
    ) {
    }

    public function isPatch(): bool
    {
        $lower = strtolower($this->name);

        return str_ends_with($lower, '.patch') || str_ends_with($lower, '.diff');
    }

    /**
     * Drupal.org convention: {nid}-{comment#}.patch or
     * {nid}-{comment#}-{description}.patch. The comment number identifies
     * which revision of the patch this is — higher = newer.
     */
    public function commentNumber(): ?int
    {
        if (preg_match('/^\d+-(\d+)/', $this->name, $m) === 1) {
            return (int) $m[1];
        }

        return null;
    }

    /**
     * An attachment entry, which the API returns either wrapped in a "file"
     * envelope or flat. Without a name and a URL there is nothing to download
     * or classify, so such an entry is no file at all.
     *
     * @param array<array-key, mixed> $data
     */
    public static function fromApi(array $data): ?self
    {
        $payload = new ApiPayload($data);
        $file = $payload->child('file') ?? $payload;

        $name = $file->stringOrNull('filename') ?? $file->string('name');
        $url = $file->string('url');

        if ($name === '' || $url === '') {
            return null;
        }

        return new self(
            name: $name,
            url: $url,
            size: $file->int('filesize'),
            timestamp: $file->int('timestamp'),
        );
    }

    /** @return array<string, mixed> */
    public function toApiArray(): array
    {
        return [
            'file' => [
                'filename' => $this->name,
                'url' => $this->url,
                'filesize' => (string) $this->size,
                'timestamp' => (string) $this->timestamp,
            ],
        ];
    }
}
