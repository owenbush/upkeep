<?php

declare(strict_types=1);

namespace Upkeep\Drupal;

/**
 * A drupal.org account, resolved from the uid an attachment records as its
 * owner.
 *
 * It exists for one job: naming the person whose work a patch is, in the
 * commit that carries that work into a merge request. drupal.org allocates
 * credit through its own issue-credit system rather than through git
 * authorship, and the API offers a username and a profile URL and no email at
 * all — so this deliberately carries no address. A `git commit --author` line
 * assembled from a synthesised address would be a claim about identity that
 * nothing here can support.
 */
final readonly class DrupalUser
{
    public function __construct(
        public int $uid,
        public string $name,
        public string $profileUrl,
    ) {
    }

    /**
     * @param array<array-key, mixed> $data
     */
    public static function fromApi(array $data): ?self
    {
        $payload = new ApiPayload($data);
        $uid = $payload->intOrNull('uid');
        $name = $payload->string('name');

        if ($uid === null || $uid < 1 || $name === '') {
            return null;
        }

        return new self(
            uid: $uid,
            name: $name,
            profileUrl: $payload->stringOrNull('url') ?? self::profileUrlFor($name),
        );
    }

    /**
     * drupal.org's own profile path. Used only when the payload omits `url`,
     * which the account resource normally supplies.
     */
    private static function profileUrlFor(string $name): string
    {
        return 'https://www.drupal.org/u/' . rawurlencode(strtolower(str_replace(' ', '-', $name)));
    }
}
