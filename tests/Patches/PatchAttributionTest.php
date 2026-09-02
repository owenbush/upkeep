<?php

declare(strict_types=1);

namespace Upkeep\Tests\Patches;

use PHPUnit\Framework\TestCase;
use Upkeep\Drupal\DrupalUser;
use Upkeep\Drupal\Issue;
use Upkeep\Drupal\IssueFile;
use Upkeep\Drupal\IssueReference;
use Upkeep\Drupal\IssueStatus;
use Upkeep\Patches\PatchAttribution;

/**
 * The commit message a promoted patch travels under.
 *
 * This is the one place in the tool where somebody else's work moves into
 * history under whoever pushes it, and the message is the only durable record
 * of whose work it was. So what is pinned here is not formatting — it is that
 * the author is named, that the promoter is not credited with the change, and
 * that no identity is ever invented.
 */
final class PatchAttributionTest extends TestCase
{
    private static function issue(): Issue
    {
        return new Issue(
            nid: 3597808,
            title: 'Fix the widget on PHP 8.4',
            status: IssueStatus::NeedsReview,
            url: 'https://www.drupal.org/node/3597808',
            project: 'widget',
            priority: null,
            version: null,
            component: null,
            category: null,
        );
    }

    private static function patch(?int $ownerUid = 3644742): IssueFile
    {
        return new IssueFile(
            '3597808-9-fix.patch',
            'https://www.drupal.org/files/issues/3597808-9-fix.patch',
            1660,
            1705400000,
            $ownerUid,
        );
    }

    private static function author(): DrupalUser
    {
        return new DrupalUser(3644742, 'hebatelhayah', 'https://www.drupal.org/u/hebatelhayah');
    }

    /**
     * drupal.org's own commit convention, which is also — not by accident —
     * a string extractOwning() reads, so the merge request that follows pairs
     * with its issue by the ordinary rule rather than a special one.
     */
    public function testTheSubjectIsTheDrupalOrgConventionAndStillParsesAsOwning(): void
    {
        $subject = PatchAttribution::forPatch(self::issue(), self::patch(), self::author(), null)->subject();

        self::assertSame('Issue #3597808 by hebatelhayah: Fix the widget on PHP 8.4', $subject);
        self::assertSame(3597808, IssueReference::extractOwning($subject, 'some-branch'));
    }

    public function testTheAuthorIsNamedInTheBodyAndInATrailer(): void
    {
        $message = PatchAttribution::forPatch(self::issue(), self::patch(), self::author(), null)->message();

        self::assertStringContainsString('posted to the issue by hebatelhayah', $message);
        self::assertStringContainsString(
            'Patch-author: hebatelhayah <https://www.drupal.org/u/hebatelhayah>',
            $message,
        );
        self::assertStringContainsString('Patch-file: 3597808-9-fix.patch', $message);
        self::assertStringContainsString(
            'Patch-source: https://www.drupal.org/files/issues/3597808-9-fix.patch',
            $message,
        );
        self::assertStringContainsString('Issue: https://www.drupal.org/node/3597808', $message);
    }

    /**
     * The sentence that makes this an attribution rather than a citation: the
     * person pushing is explicitly not the person who wrote it.
     */
    public function testTheBodySaysThePromoterIsNotTheAuthor(): void
    {
        $message = PatchAttribution::forPatch(self::issue(), self::patch(), self::author(), null)->message();

        self::assertStringContainsString("the change is hebatelhayah's", strtolower($message));
        self::assertStringContainsString('not authoring it', $message);
    }

    public function testThePromoterIsRecordedWhenKnown(): void
    {
        $message = PatchAttribution::forPatch(self::issue(), self::patch(), self::author(), 'owenbush')->message();

        self::assertStringContainsString('Promoted-by: owenbush', $message);
    }

    public function testAnUnknownPromoterAddsNoTrailer(): void
    {
        $message = PatchAttribution::forPatch(self::issue(), self::patch(), self::author(), null)->message();

        self::assertStringNotContainsString('Promoted-by:', $message);
    }

    /**
     * An unattributable patch is still somebody's work. The message says so
     * and points at the issue rather than falling silent, because a commit
     * that simply omitted the author reads exactly like one that had none.
     */
    public function testAnUnknownAuthorIsSaidOutLoudRatherThanOmitted(): void
    {
        $attribution = PatchAttribution::forPatch(self::issue(), self::patch(null), null, null);

        self::assertSame('Issue #3597808: Fix the widget on PHP 8.4', $attribution->subject());
        $message = $attribution->message();
        self::assertStringContainsString('drupal.org records no account for the file', $message);
        self::assertStringContainsString('credit', strtolower($message));
        self::assertStringContainsString("patch author's work", $message);
        self::assertStringNotContainsString('Patch-author:', $message);
    }

    /**
     * The rule that keeps this honest: git's own attribution mechanisms take
     * an email address, drupal.org publishes none, and a synthesised one would
     * be a claim about somebody's identity that nothing here can support.
     */
    public function testNoGitAuthorshipHeaderIsEverSynthesised(): void
    {
        foreach ([self::author(), null] as $author) {
            $message = PatchAttribution::forPatch(self::issue(), self::patch(), $author, 'owenbush')->message();

            self::assertStringNotContainsString('Co-authored-by:', $message);
            // The only address-shaped thing permitted is the profile URL.
            self::assertDoesNotMatchRegularExpression('/<[^>@]+@[^>]+>/', $message);
        }
    }

    /** Trailers are only trailers if they end the message. */
    public function testTheMessageEndsWithItsTrailerBlockAndANewline(): void
    {
        $message = PatchAttribution::forPatch(self::issue(), self::patch(), self::author(), 'owenbush')->message();

        self::assertStringEndsWith("Promoted-by: owenbush\n", $message);
    }
}
