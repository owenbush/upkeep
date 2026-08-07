<?php

declare(strict_types=1);

namespace Upkeep\Patches;

use Symfony\Contracts\HttpClient\Exception\ExceptionInterface as HttpClientExceptionInterface;
use Symfony\Contracts\HttpClient\HttpClientInterface;
use Upkeep\Filesystem\FileWriter;
use Upkeep\Workflow\WorkflowException;

/**
 * Downloads a patch file into the cockpit and hands back its local path.
 *
 * Kept out of the adapter deliberately: the adapter's job stops at engine
 * mechanics, and by the time a patch reaches it the file must already be on
 * disk and vouched for. Everything that could go wrong with an untrusted URL
 * and an untrusted filename is therefore decided here, once.
 */
final readonly class PatchFetcher
{
    /**
     * Patches are text diffs; the largest thing anyone legitimately attaches
     * to a drupal.org issue is orders of magnitude under this. The cap exists
     * so a wrong URL cannot fill the disk before anyone notices.
     */
    public const MAX_BYTES = 8 * 1024 * 1024;

    public function __construct(
        private HttpClientInterface $http,
        private string $cacheDir,
    ) {
    }

    /**
     * Fetches $url and stores it as $name under the cache directory, keyed by
     * issue. Returns the absolute local path.
     *
     * @throws WorkflowException when the URL is unusable, the download fails,
     *                           or the body is not a patch
     */
    public function fetch(int $issueNid, string $name, string $url): string
    {
        $safeName = self::safeName($name);
        $target = sprintf('%s/%d/%s', rtrim($this->cacheDir, '/'), $issueNid, $safeName);

        $body = $this->download(self::assertFetchableUrl($url));
        self::assertLooksLikeAPatch($body, $name);

        FileWriter::ensureDirectory(\dirname($target));
        FileWriter::write($target, $body);

        return $target;
    }

    /**
     * The filename, reduced to something that cannot escape the cache
     * directory. The name arrives from a remote API, so it is treated as
     * hostile input: only the basename survives, and only from a conservative
     * character set.
     */
    public static function safeName(string $name): string
    {
        $base = basename(str_replace('\\', '/', $name));
        $base = preg_replace('/[^A-Za-z0-9._-]/', '_', $base) ?? '';
        $base = ltrim($base, '.');

        return $base === '' ? 'patch.patch' : $base;
    }

    /**
     * Only http(s). A patch source is a URL someone typed, and `file://`,
     * `php://` and friends would turn "download this patch" into "read this
     * local path through an HTTP client" — a different operation than the one
     * being authorised.
     *
     * @throws WorkflowException
     */
    private static function assertFetchableUrl(string $url): string
    {
        $scheme = strtolower((string) (parse_url($url, \PHP_URL_SCHEME) ?? ''));
        if ($scheme !== 'http' && $scheme !== 'https') {
            throw new WorkflowException(sprintf(
                'Refusing to fetch "%s": only http and https patch URLs are supported.',
                $url,
            ));
        }

        return $url;
    }

    /**
     * A body that is not a unified diff is refused before it can reach `git
     * apply`. The usual cause is not an attack but a redirect to an HTML login
     * or interstitial page, which would otherwise surface as an inscrutable
     * git error about a corrupt patch.
     *
     * @throws WorkflowException
     */
    private static function assertLooksLikeAPatch(string $body, string $name): void
    {
        if (trim($body) === '') {
            throw new WorkflowException(sprintf('Downloaded patch "%s" is empty.', $name));
        }

        foreach (['diff --git ', '--- ', 'Index: ', 'index '] as $marker) {
            if (str_contains($body, "\n" . $marker) || str_starts_with($body, $marker)) {
                return;
            }
        }

        throw new WorkflowException(sprintf(
            'Downloaded "%s" does not look like a patch (no diff header found). The URL may have redirected to '
            . 'an HTML page rather than the file.',
            $name,
        ));
    }

    /** @throws WorkflowException */
    private function download(string $url): string
    {
        try {
            $response = $this->http->request('GET', $url, [
                'headers' => ['Accept' => 'text/plain, */*'],
                'timeout' => 30,
                'max_duration' => 120,
            ]);

            $status = $response->getStatusCode();
            if ($status !== 200) {
                throw new WorkflowException(sprintf('Downloading %s failed with HTTP %d.', $url, $status));
            }

            $body = $response->getContent(false);
        } catch (HttpClientExceptionInterface $e) {
            throw new WorkflowException(sprintf('Downloading %s failed: %s', $url, $e->getMessage()));
        }

        if (\strlen($body) > self::MAX_BYTES) {
            throw new WorkflowException(sprintf(
                'Refusing %s: %d bytes exceeds the %d-byte patch limit.',
                $url,
                \strlen($body),
                self::MAX_BYTES,
            ));
        }

        return $body;
    }
}
