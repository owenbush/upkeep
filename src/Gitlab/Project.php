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
        /**
         * Your GitLab access level on this project, or null when the payload
         * cannot say — an unauthenticated read omits `permissions` entirely.
         * Null is genuinely unknown and must never be read as "no".
         */
        public ?int $accessLevel = null,
    ) {
    }

    /** GitLab's Developer role: the level at which pushing becomes possible. */
    private const DEVELOPER = 30;

    /**
     * Whether you may push here, or null when it cannot be told.
     *
     * On drupal.org this is the question a fresh issue fork answers "no" to:
     * creating a fork does not grant push access to it, and the grant is a
     * separate button on the issue page. Asking before pushing turns a
     * post-hoc rejection into something that can be said up front.
     *
     * Null when unknown, and callers must proceed on null rather than refuse.
     * An absent `permissions` key means the request was unauthenticated or the
     * shape changed — neither is evidence about access, and refusing on it
     * would block pushes that would have worked.
     */
    public function canPush(): ?bool
    {
        return $this->accessLevel === null ? null : $this->accessLevel >= self::DEVELOPER;
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
            accessLevel: self::accessLevel($payload),
        );
    }

    /**
     * The highest of the direct and inherited grants, since either is enough
     * to push and GitLab reports them separately.
     */
    private static function accessLevel(ApiPayload $payload): ?int
    {
        $permissions = $payload->child('permissions');
        if ($permissions === null) {
            return null;
        }

        $levels = [];
        foreach (['project_access', 'group_access'] as $key) {
            $level = $permissions->child($key)?->intOrNull('access_level');
            if ($level !== null) {
                $levels[] = $level;
            }
        }

        return $levels === [] ? null : max($levels);
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
            'permissions' => $this->accessLevel === null
                ? null
                : ['project_access' => ['access_level' => $this->accessLevel]],
        ];
    }
}
