<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\EngineDescription;
use Upkeep\Adapter\ProjectRegistration;

/**
 * The collision between a global engine project name and a movable projects
 * root.
 *
 * `ProjectName::for()` is deterministic and the engine's namespace is the
 * whole machine, so moving the projects root — a new cockpit, a scratch
 * directory, a one-off `UPKEEP_PROJECTS_ROOT` — makes every name collide with
 * the registration the old root left behind. The engine is right to refuse it;
 * what it cannot know is that both paths are upkeep's.
 */
final class ProjectRegistrationTest extends TestCase
{
    private static function described(?string $approot): ?EngineDescription
    {
        if ($approot === null) {
            return null;
        }

        return EngineDescription::fromJson(json_encode(
            ['raw' => ['approot' => $approot]],
            \JSON_THROW_ON_ERROR,
        ));
    }

    public function testANameRegisteredElsewhereIsAConflict(): void
    {
        $registration = ProjectRegistration::of(
            'upkeep-widget-d11',
            self::described('/Users/owen/.upkeep-scratch/projects/upkeep-widget-d11'),
        );

        self::assertTrue($registration->conflictsWith('/Users/owen/contrib/upkeep/projects/upkeep-widget-d11'));
    }

    public function testTheSamePathIsNotAConflict(): void
    {
        $path = sys_get_temp_dir();
        $registration = ProjectRegistration::of('upkeep-widget-d11', self::described($path));

        self::assertFalse($registration->conflictsWith($path));
        // A trailing slash is the same directory, not a different one.
        self::assertFalse($registration->conflictsWith($path . '/'));
    }

    /**
     * A question that could not be asked is not an answer. An engine too old
     * to describe the project, or output this tool cannot parse, must not
     * become a refusal to provision — the engine still gets to refuse for
     * itself a moment later.
     */
    public function testAnUnknownRegistrationIsNotTreatedAsAConflict(): void
    {
        self::assertFalse(
            ProjectRegistration::of('upkeep-widget-d11', null)->conflictsWith('/anywhere'),
        );
        self::assertFalse(
            ProjectRegistration::of('upkeep-widget-d11', self::described(''))->conflictsWith('/anywhere'),
        );
    }

    /**
     * The refusal has to be actionable: both paths, the one command that
     * resolves it, and — because the old directory may hold work — what that
     * command does not do.
     */
    public function testTheRefusalNamesBothPathsAndTheRecoveryCommand(): void
    {
        $message = ProjectRegistration::of(
            'upkeep-widget-d11',
            self::described('/Users/owen/.upkeep-scratch/projects/upkeep-widget-d11'),
        )->conflictException('/Users/owen/contrib/upkeep/projects/upkeep-widget-d11')->getMessage();

        self::assertStringContainsString('/Users/owen/.upkeep-scratch/projects/upkeep-widget-d11', $message);
        self::assertStringContainsString('/Users/owen/contrib/upkeep/projects/upkeep-widget-d11', $message);
        self::assertStringContainsString('ddev stop --unlist upkeep-widget-d11', $message);
        self::assertStringContainsString('does not delete', $message);
        self::assertStringContainsString('--projects-root', $message);
    }

    /**
     * The engine's own wording, verbatim from the failure a maintainer hit.
     * Matched on the phrase rather than an exit code, because every
     * provisioning failure shares that code.
     */
    public function testTheEnginesOwnRefusalIsRecognised(): void
    {
        $engineSaid = "could not write DDEV config file /Users/owen/contrib/upkeep/projects/"
            . "upkeep-entity-type-access-conditions-d11/.ddev/config.yaml: project "
            . "upkeep-entity-type-access-conditions-d11 project root is already set to "
            . "/Users/owen/.upkeep-task10-scratch/projects/upkeep-entity-type-access-conditions-d11, "
            . "refusing to change it";

        self::assertTrue(ProjectRegistration::isRootConflict($engineSaid));
    }

    /** Narrow on purpose: an unrecognised failure stays as the engine wrote it. */
    public function testAnUnrelatedEngineFailureIsNotClaimedAsThisOne(): void
    {
        self::assertFalse(ProjectRegistration::isRootConflict('docker: daemon not running'));
        self::assertFalse(ProjectRegistration::isRootConflict('port 8080 is already in use'));
        self::assertFalse(ProjectRegistration::isRootConflict(''));
    }

    /**
     * The path the pre-flight cannot see: the engine's record survives but the
     * directory it names is gone, so `describe` had nothing to report. Same
     * guidance, and the engine's own words kept underneath it.
     */
    public function testTheTranslatedRefusalKeepsTheEnginesWordsAndAddsTheFix(): void
    {
        $engineFailure = new AdapterException('project root is already set to /old/path, refusing to change it');

        $translated = ProjectRegistration::of('upkeep-widget-d11', null)
            ->rootConflictException('/new/path', $engineFailure);

        self::assertStringContainsString('ddev stop --unlist upkeep-widget-d11', $translated->getMessage());
        self::assertStringContainsString('does not delete', $translated->getMessage());
        self::assertStringContainsString('/new/path', $translated->getMessage());
        // The engine's own text is kept, not replaced: it names the old path,
        // which is the thing the operator has to recognise.
        self::assertStringContainsString('/old/path', $translated->getMessage());
        self::assertSame($engineFailure, $translated->getPrevious());
    }
}
