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
        /**
         * The push URL as GitLab itself advertises it. Never constructed:
         * git.drupalcode.org serves HTTPS but advertises SSH on
         * git.drupal.org, and assembling the URL from the host you fetched
         * from produces one that does not answer.
         */
        public string $sshUrl = '',
        /** The branch a merge request targets when nothing else says. */
        public string $defaultBranch = '',
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
            sshUrl: $payload->string('ssh_url_to_repo'),
            defaultBranch: $payload->string('default_branch'),
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
            'ssh_url_to_repo' => $this->sshUrl,
            'default_branch' => $this->defaultBranch,
        ];
    }
}
