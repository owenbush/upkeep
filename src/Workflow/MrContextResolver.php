<?php

declare(strict_types=1);

namespace Upkeep\Workflow;

use Upkeep\Cockpit\Module;
use Upkeep\Drupal\CoreCompatibility;
use Upkeep\Cockpit\ModuleResolution;
use Upkeep\Gitlab\ApiFailure;
use Upkeep\Gitlab\GitlabClient;
use Upkeep\Gitlab\MergeRequest;
use Upkeep\Gitlab\Project;

/**
 * Shared context resolution for the single-MR commands: registry lookup →
 * core-version selection → GitLab MR fetch → open-state validation.
 *
 * Core-version rule: an explicit --version must be one of the module's
 * tracked core versions; when omitted, the default is the FIRST core version
 * listed in the module's registry entry (the registry order is the
 * maintainer's priority order).
 *
 * Cheap local validation (module known, core tracked) runs before any
 * network request. The native-base rule (no backport testing) needs the
 * environment's working copy and therefore stays in the adapter's applyMr;
 * its rejection surfaces as an infrastructure outcome, not a check verdict.
 */
final readonly class MrContextResolver
{
    /**
     * @param array<string, Module> $modules registry entries keyed by machine name
     */
    public function __construct(
        private array $modules,
        private GitlabClient $client,
        /**
         * Base artifact versions on disk, ascending, for a module the registry
         * does not carry.
         *
         * Empty means the caller has no cockpit to ask — then only registered
         * modules resolve, which is the behaviour that predates the watchlist
         * split rather than a new refusal.
         *
         * @var list<string>
         */
        private array $coresOnDisk = [],
    ) {
    }

    /**
     * @throws WorkflowException when the context cannot be resolved
     */
    public function resolve(string $moduleName, int $iid, ?string $requestedCore): MrContext
    {
        $module = ModuleResolution::resolve($this->modules, $moduleName, $this->coresOnDisk);
        $coreMajor = self::selectCoreVersion($module, $requestedCore);

        $project = $this->client->project($module->project);
        if ($project instanceof ApiFailure) {
            throw new WorkflowException(ModuleResolution::projectFailure($this->modules, $module, $project->message));
        }

        $mergeRequest = $this->fetchOpenMr($module, $project, $iid);
        $this->assertBranchSupports($project, $module, $mergeRequest->targetBranch, $coreMajor);

        return new MrContext(
            $module,
            $project,
            $mergeRequest,
            $coreMajor,
            $this->client->mergeRefSha($project, $iid),
        );
    }

    /**
     * Why a requested core is not available — which is a different sentence
     * depending on where the module's core list came from.
     *
     * A watched module's `core_versions` is a line somebody wrote in
     * registry.yml, so that is the thing to edit. A *derived* module has no
     * entry at all: its list is the base artifacts on this machine, and
     * telling a maintainer to "add it to core_versions in registry.yml" sends
     * them to edit a file that does not mention the module. Reported from a
     * real run — `--version=12` on an unregistered module answered "Its
     * registry entry tracks: 11, 10", naming a registry entry that does not
     * exist and listing the contents of a directory.
     *
     * Listed ascending here whatever the internal order: newest-first exists
     * so `core_versions[0]` is the default, and it reads as a mistake in prose.
     */
    private static function untrackedCore(Module $module, string $requestedCore): string
    {
        $available = $module->coreVersions;
        usort($available, static fn (string $a, string $b): int => (int) $a <=> (int) $b);

        if ($module->watched) {
            return sprintf(
                'Module "%s" does not track core version "%s". Its registry entry tracks: %s. Add it to '
                    . 'core_versions in registry.yml to check against it.',
                $module->name,
                $requestedCore,
                implode(', ', $available),
            );
        }

        return sprintf(
            "No base artifacts for core %s, so \"%s\" cannot be checked against it.\n"
            . "Built here: %s.\n"
            . 'Build another with: upkeep base-artifacts:build --version=%s',
            $requestedCore,
            $module->name,
            implode(', ', $available),
            $requestedCore,
        );
    }

    /**
     * Refuse a core the merge request's target branch does not declare.
     *
     * Checking a branch on a core it never claimed produces a failure that
     * says nothing about the module — composer refuses to resolve, and the
     * report reads as though the contribution is broken. The same reasoning
     * removed the core multiplier from the dashboard: evidence gathered
     * against a core the branch does not support is not evidence.
     *
     * It matters more now the core can be *inferred*. A module the registry
     * does not carry takes the newest core with base artifacts on this
     * machine, which is a fact about the disk and knows nothing about the
     * branch — so without this, upkeep would pick a core and then blame the
     * module for it.
     *
     * **Silence is the answer whenever the branch cannot be read.** No
     * info.yml at that path, a constraint nobody can parse, a closed endpoint:
     * each of them means upkeep does not know, and refusing on not-knowing
     * would block work over a file it merely failed to fetch.
     *
     * @throws WorkflowException when the branch declares cores and this is not one
     */
    private function assertBranchSupports(Project $project, Module $module, string $branch, string $core): void
    {
        $info = $this->client->fileContents($project, $module->name . '.info.yml', $branch);
        $constraint = $info === null ? null : CoreCompatibility::constraintIn($info);

        // The cores worth *suggesting* are the ones this machine could run:
        // what is built, or failing that what the registry entry tracks.
        // Naming a core with no base artifacts would answer one refusal with
        // another.
        $usable = $this->coresOnDisk !== [] ? $this->coresOnDisk : $module->coreVersions;
        $declared = $constraint === null ? null : CoreCompatibility::fromConstraint($constraint, $usable);

        if ($declared === null || $declared->declares($core)) {
            return;
        }

        throw new WorkflowException(sprintf(
            "%s %s declares core_version_requirement \"%s\", which does not include core %s.\n"
            . "Checking it there would fail for reasons that say nothing about the module.\n"
            . '%s',
            $module->name,
            $branch,
            $constraint,
            $core,
            $declared->cores === []
                ? 'Build base artifacts for a core it declares: upkeep base-artifacts:build --version=<core>'
                : 'Pass --version=' . implode(' or --version=', $declared->cores) . '.',
        ));
    }

    /**
     * The one registry lookup: every command that takes a module name resolves
     * it here, so "not registered" has a single wording — and one that lists
     * what *is* registered.
     *
     * @param array<string, Module> $modules
     *
     * @throws WorkflowException when the name is not in the registry
     */
    public static function requireModule(array $modules, string $moduleName): Module
    {
        return $modules[$moduleName] ?? throw new WorkflowException(sprintf(
            'Module "%s" is not registered in the cockpit. Registered modules: %s.',
            $moduleName,
            implode(', ', array_keys($modules)) ?: '(none)',
        ));
    }

    /**
     * @throws WorkflowException when the requested core version is not tracked
     */
    public static function selectCoreVersion(Module $module, ?string $requestedCore): string
    {
        if ($requestedCore === null || $requestedCore === '') {
            // Documented default: the first core version listed in the
            // module's registry entry.
            return $module->coreVersions[0];
        }

        if (!\in_array($requestedCore, $module->coreVersions, true)) {
            throw new WorkflowException(self::untrackedCore($module, $requestedCore));
        }

        return $requestedCore;
    }

    private function fetchOpenMr(Module $module, Project $project, int $iid): MergeRequest
    {
        $mr = $this->client->mergeRequest($project, $iid);
        if ($mr instanceof ApiFailure) {
            throw new WorkflowException(sprintf(
                'MR !%d of module "%s" could not be resolved (not found or inaccessible): %s',
                $iid,
                $module->name,
                $mr->message,
            ));
        }

        if ($mr->state !== 'opened') {
            throw new WorkflowException(sprintf(
                'MR !%d ("%s") is %s — only open MRs can be checked or reviewed. %s',
                $mr->iid,
                $mr->title,
                $mr->state,
                $mr->webUrl,
            ));
        }

        return $mr;
    }
}
