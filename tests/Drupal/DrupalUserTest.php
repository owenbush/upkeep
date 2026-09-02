<?php

declare(strict_types=1);

namespace Upkeep\Tests\Drupal;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Drupal\DrupalUser;
use Upkeep\Drupal\IssueFile;

/**
 * Who posted a patch — the fact a promoted patch's commit is built around.
 *
 * The shape is the one drupal.org actually returns, checked against the live
 * api-d7: a file resource carries an `owner` *reference* ({uri, id, resource})
 * and no username, so the name costs one further request against
 * /user/<uid>.json. Both halves are pinned here because a wrong guess about
 * either produces an unattributed commit, which is the failure this whole path
 * exists to avoid.
 */
final class DrupalUserTest extends TestCase
{
    private static function client(callable $handler): DrupalOrgClient
    {
        return new DrupalOrgClient(new MockHttpClient($handler));
    }

    /**
     * @param array<string, mixed> $data
     */
    private static function json(array $data): MockResponse
    {
        return new MockResponse(
            json_encode($data, \JSON_THROW_ON_ERROR),
            ['response_headers' => ['content-type' => 'application/json']],
        );
    }

    public function testAnAccountIsReadFromTheApiShape(): void
    {
        $client = self::client(static fn (string $method, string $url): MockResponse => self::json([
            'uid' => '3644742',
            'name' => 'hebatelhayah',
            'url' => 'https://www.drupal.org/u/hebatelhayah',
        ]));

        $user = $client->user(3644742);

        self::assertInstanceOf(DrupalUser::class, $user);
        self::assertSame(3644742, $user->uid);
        self::assertSame('hebatelhayah', $user->name);
        self::assertSame('https://www.drupal.org/u/hebatelhayah', $user->profileUrl);
    }

    /** The profile URL is derivable, so a payload without one is still usable. */
    public function testAMissingProfileUrlIsDerivedFromTheName(): void
    {
        $user = DrupalUser::fromApi(['uid' => '42', 'name' => 'Project Update Bot']);

        self::assertInstanceOf(DrupalUser::class, $user);
        self::assertSame('https://www.drupal.org/u/project-update-bot', $user->profileUrl);
    }

    /**
     * Half an account is no account. A nameless user would put an empty
     * credit into a commit message, which is worse than saying it is unknown.
     */
    public function testAnAccountWithoutAUsableUidOrNameIsNotBuilt(): void
    {
        self::assertNull(DrupalUser::fromApi(['uid' => '42']));
        self::assertNull(DrupalUser::fromApi(['name' => 'someone']));
        self::assertNull(DrupalUser::fromApi(['uid' => '0', 'name' => 'anonymous']));
    }

    /** No request is made for a uid that cannot be one. */
    public function testANonPositiveUidIsRefusedWithoutAsking(): void
    {
        $asked = false;
        $client = self::client(static function () use (&$asked): MockResponse {
            $asked = true;

            return self::json([]);
        });

        self::assertNull($client->user(0));
        self::assertFalse($asked);
    }

    /**
     * An unreadable account is null *and* recorded — the client's standing
     * contract, and here the difference between "this patch has no author" and
     * "the author could not be looked up just now".
     */
    public function testAnUnreadableAccountIsNullAndWarnedAbout(): void
    {
        $client = self::client(static fn (): MockResponse => new MockResponse('', ['http_code' => 503]));

        self::assertNull($client->user(3644742));
        self::assertNotSame([], $client->warnings());
        self::assertStringContainsString('3644742', implode(' ', $client->warnings()));
    }

    public function testAnAccountPayloadInAnUnreadableShapeIsNull(): void
    {
        $client = self::client(static fn (): MockResponse => self::json(['nothing' => 'useful']));

        self::assertNull($client->user(3644742));
    }

    // ------------------------------------------- the owner on the attachment

    /**
     * The reference shape the live file resource returns. Parsing it costs
     * nothing: the client already dereferences /file/<fid>.json to learn the
     * filename, so the owner id is in a payload it has anyway.
     */
    public function testTheOwnerReferenceOnAFileIsRead(): void
    {
        $file = IssueFile::fromApi(['file' => [
            'filename' => '3597808-9-fix.patch',
            'url' => 'https://www.drupal.org/files/issues/3597808-9-fix.patch',
            'owner' => [
                'uri' => 'https://www.drupal.org/api-d7/user/3644742',
                'id' => '3644742',
                'resource' => 'user',
            ],
        ]]);

        self::assertInstanceOf(IssueFile::class, $file);
        self::assertSame(3644742, $file->ownerUid);
    }

    /** A flattened snapshot keeps working. */
    public function testAFlatUidIsAcceptedToo(): void
    {
        $file = IssueFile::fromApi(['file' => [
            'filename' => 'a.patch',
            'url' => 'https://example.com/a.patch',
            'uid' => '99',
        ]]);

        self::assertInstanceOf(IssueFile::class, $file);
        self::assertSame(99, $file->ownerUid);
    }

    public function testAFileWithNoOwnerAtAllHasNoOwnerUid(): void
    {
        $file = IssueFile::fromApi(['file' => [
            'filename' => 'a.patch',
            'url' => 'https://example.com/a.patch',
        ]]);

        self::assertInstanceOf(IssueFile::class, $file);
        self::assertNull($file->ownerUid);
    }

    /** It survives the round trip the snapshot cache puts it through. */
    public function testTheOwnerSurvivesToApiArrayAndBack(): void
    {
        $original = new IssueFile('a.patch', 'https://example.com/a.patch', 10, 20, 3644742);

        $restored = IssueFile::fromApi($original->toApiArray());

        self::assertInstanceOf(IssueFile::class, $restored);
        self::assertSame(3644742, $restored->ownerUid);
    }
}
