<?php

declare(strict_types=1);

namespace Upkeep\Tests\Patches;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Patches\PatchFetcher;
use Upkeep\Workflow\WorkflowException;

/**
 * The download boundary.
 *
 * Everything crossing it is untrusted: the URL is typed by an operator or read
 * off a remote API, and the filename comes from that same API. What lands on
 * disk here is handed straight to `git apply`, so this is the last place a
 * traversing name, a non-HTTP scheme, or an HTML interstitial can be stopped.
 */
final class PatchFetcherTest extends TestCase
{
    private const DIFF = "diff --git a/widget.module b/widget.module\n"
        . "--- a/widget.module\n+++ b/widget.module\n@@ -1 +1 @@\n-old\n+new\n";

    private string $cacheDir;

    protected function setUp(): void
    {
        $this->cacheDir = sys_get_temp_dir() . '/upkeep-patchfetch-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->cacheDir));
    }

    private function fetcher(MockResponse|callable $response): PatchFetcher
    {
        return new PatchFetcher(new MockHttpClient($response), $this->cacheDir);
    }

    public function testStoresTheDownloadedPatchUnderTheIssue(): void
    {
        $path = $this->fetcher(new MockResponse(self::DIFF))
            ->fetch(3597808, '3597808-9-fix.patch', 'https://www.drupal.org/files/issues/3597808-9-fix.patch');

        self::assertSame($this->cacheDir . '/3597808/3597808-9-fix.patch', $path);
        self::assertFileExists($path);
        self::assertSame(self::DIFF, file_get_contents($path));
    }

    /**
     * The name comes from a remote API, so it is treated as hostile: only a
     * basename from a conservative character set survives, and the result can
     * never point outside the cache directory.
     *
     * @return iterable<string, array{string, string}>
     */
    public static function hostileNames(): iterable
    {
        yield 'a traversing name' => ['../../../etc/cron.d/evil', 'evil'];
        yield 'an absolute path' => ['/etc/passwd', 'passwd'];
        yield 'a windows path' => ['..\\..\\windows\\system32\\x.patch', 'x.patch'];
        yield 'a leading dot' => ['.bashrc', 'bashrc'];
        yield 'shell metacharacters' => ['a;rm -rf $HOME.patch', 'a_rm_-rf__HOME.patch'];
        yield 'nothing usable at all' => ['../', 'patch.patch'];
        yield 'an ordinary name is untouched' => ['3597808-9-fix.patch', '3597808-9-fix.patch'];
    }

    #[DataProvider('hostileNames')]
    public function testTheFilenameCannotEscapeTheCacheDirectory(string $given, string $expected): void
    {
        self::assertSame($expected, PatchFetcher::safeName($given));

        $path = $this->fetcher(new MockResponse(self::DIFF))
            ->fetch(3597808, $given, 'https://example.test/p.patch');

        self::assertSame($this->cacheDir . '/3597808/' . $expected, $path);
        self::assertStringStartsWith($this->cacheDir . '/', $path);
    }

    /**
     * "Download this patch" must not become "read this local path through an
     * HTTP client" — a different operation than the one being authorised.
     *
     * @return iterable<string, array{string}>
     */
    public static function refusedSchemes(): iterable
    {
        yield 'a local file' => ['file:///etc/passwd'];
        yield 'a php stream' => ['php://filter/read=convert.base64-encode/resource=/etc/passwd'];
        yield 'an ftp URL' => ['ftp://example.test/p.patch'];
        yield 'no scheme at all' => ['/tmp/local.patch'];
    }

    #[DataProvider('refusedSchemes')]
    public function testOnlyHttpAndHttpsAreFetched(string $url): void
    {
        $fetcher = $this->fetcher(static fn (): MockResponse => throw new \LogicException('must not be requested'));

        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessage('only http and https');
        $fetcher->fetch(3597808, 'p.patch', $url);
    }

    /**
     * The usual cause is not an attack but a redirect to a login or
     * interstitial page. Caught here, it reads as what it is; passed through,
     * it would surface as an inscrutable git complaint about a corrupt patch.
     */
    public function testAnHtmlPageIsRefusedRatherThanHandedToGit(): void
    {
        $fetcher = $this->fetcher(new MockResponse('<!doctype html><html><body>Log in</body></html>'));

        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessage('does not look like a patch');
        $fetcher->fetch(3597808, 'p.patch', 'https://example.test/p.patch');
    }

    public function testAnEmptyBodyIsRefused(): void
    {
        $fetcher = $this->fetcher(new MockResponse("   \n  "));

        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessage('is empty');
        $fetcher->fetch(3597808, 'p.patch', 'https://example.test/p.patch');
    }

    /** @return iterable<string, array{string}> */
    public static function acceptedDiffShapes(): iterable
    {
        yield 'git format' => ["diff --git a/x b/x\n--- a/x\n+++ b/x\n"];
        yield 'plain unified diff' => ["--- a/x\n+++ b/x\n@@ -1 +1 @@\n"];
        yield 'subversion style' => ["Index: x\n===\n--- x\n+++ x\n"];
        yield 'a mail-formatted patch' => ["From abc\nSubject: [PATCH] fix\n\ndiff --git a/x b/x\n"];
    }

    #[DataProvider('acceptedDiffShapes')]
    public function testTheShapesRealPatchesArriveInAreAccepted(string $body): void
    {
        $path = $this->fetcher(new MockResponse($body))
            ->fetch(1, 'p.patch', 'https://example.test/p.patch');

        self::assertFileExists($path);
    }

    public function testANonOkResponseIsReported(): void
    {
        $fetcher = $this->fetcher(new MockResponse('', ['http_code' => 404]));

        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessage('HTTP 404');
        $fetcher->fetch(3597808, 'p.patch', 'https://example.test/p.patch');
    }

    public function testATransportFailureIsReportedRatherThanEscaping(): void
    {
        $fetcher = $this->fetcher(new MockResponse('', ['error' => 'Connection refused by the test']));

        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessage('failed:');
        $fetcher->fetch(3597808, 'p.patch', 'https://example.test/p.patch');
    }

    /**
     * A cap so a wrong URL cannot fill the disk before anyone notices. No
     * legitimate issue attachment comes close to it.
     */
    public function testAnOversizedBodyIsRefused(): void
    {
        $huge = "diff --git a/x b/x\n" . str_repeat('+', PatchFetcher::MAX_BYTES + 1);
        $fetcher = $this->fetcher(new MockResponse($huge));

        $this->expectException(WorkflowException::class);
        $this->expectExceptionMessage('exceeds the');
        $fetcher->fetch(3597808, 'p.patch', 'https://example.test/p.patch');
    }

    public function testRefetchingOverwritesRatherThanAccumulating(): void
    {
        $updated = self::DIFF . "\n@@ -9 +9 @@\n-a\n+b\n";

        $first = $this->fetcher(new MockResponse(self::DIFF))
            ->fetch(3597808, 'p.patch', 'https://example.test/p.patch');
        $second = $this->fetcher(new MockResponse($updated))
            ->fetch(3597808, 'p.patch', 'https://example.test/p.patch');

        self::assertSame($first, $second);
        self::assertSame($updated, file_get_contents($second));
    }
}
