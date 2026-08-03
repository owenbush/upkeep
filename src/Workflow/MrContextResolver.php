<?php

declare(strict_types=1);

namespace Upkeep\Workflow;

use Upkeep\Cockpit\Module;
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
    ) {
    }

    /**
     * @throws WorkflowException when the context cannot be resolved
     */
    public function resolve(string $moduleName, int $iid, ?string $requestedCore): MrContext
    {
        $module = self::requireModule($this->modules, $moduleName);
        $coreMajor = self::selectCoreVersion($module, $requestedCore);

        $project = $this->client->project($module->project);
        if ($project instanceof ApiFailure) {
            throw new WorkflowException(sprintf(
                'Cannot resolve the GitLab project for module "%s" (%s): %s',
                $module->name,
                $module->project,
                $project->message,
            ));
        }

        return new MrContext($module, $project, $this->fetchOpenMr($module, $project, $iid), $coreMajor);
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
            throw new WorkflowException(sprintf(
                'Module "%s" does not track core version "%s". Its registry entry tracks: %s. Add it to '
                    . 'core_versions in registry.yml to check against it.',
                $module->name,
                $requestedCore,
                implode(', ', $module->coreVersions),
            ));
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
