<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * Whether the engine already knows this project name, and where it thinks it
 * lives.
 *
 * Engine project names are **global to the machine**, while upkeep's projects
 * root is configurable. `ProjectName::for()` is deterministic, so the moment a
 * maintainer moves their projects root — a new cockpit, a scratch directory
 * they were trying out, `UPKEEP_PROJECTS_ROOT` set for one run — every project
 * name collides with the registration the old root left behind.
 *
 * The engine refuses that collision, and it is right to: two directories
 * claiming one project name is exactly the ambiguity it exists to prevent.
 * What it cannot do is know that both of them are upkeep's, or which one the
 * operator meant. So this is checked *before* provisioning does any work,
 * rather than discovered after seeding a codebase and cloning a repository —
 * the failure is the same either way, but one of them wastes a minute and
 * leaves a torn-down directory behind.
 */
final readonly class ProjectRegistration
{
    private function __construct(
        public string $projectName,
        public ?string $registeredRoot,
    ) {
    }

    public static function of(string $projectName, ?EngineDescription $described): self
    {
        return new self($projectName, $described?->stringOrNull('approot'));
    }

    /**
     * Whether the engine has this name pointing somewhere other than where we
     * are about to provision.
     *
     * A registration the engine did not report — an older engine, a
     * description this tool cannot parse — is *not* treated as a conflict.
     * Refusing to provision on the strength of a question that could not be
     * asked would turn a diagnostic into an outage; the engine still gets to
     * refuse for itself a moment later, with its own wording.
     */
    public function conflictsWith(string $projectPath): bool
    {
        if ($this->registeredRoot === null || $this->registeredRoot === '') {
            return false;
        }

        return self::canonical($this->registeredRoot) !== self::canonical($projectPath);
    }

    /**
     * The refusal, naming both paths and the one command that resolves it.
     *
     * `ddev stop --unlist` deregisters; it does not delete. That distinction
     * is stated because the old directory may hold work, and an operator
     * deciding whether to run a command needs to know it will not lose any.
     */
    public function conflictException(string $projectPath): AdapterException
    {
        return new AdapterException(sprintf(
            "The engine already knows a project called \"%s\", at a different path:\n"
            . "  registered: %s\n"
            . "  wanted:     %s\n\n"
            . "That happens when the projects root moves — a new cockpit, or a --projects-root or "
            . "UPKEEP_PROJECTS_ROOT that differs from the one used before. Project names are global to the "
            . "machine, so the old registration still claims this one.\n\n"
            . "If the registered path is stale, deregister it and re-run:\n"
            . "  ddev stop --unlist %s\n\n"
            . "That removes the engine's record of it; it does not delete the directory or anything in it. "
            . "If the registered path is the one you actually want, point --projects-root at it instead.",
            $this->projectName,
            $this->registeredRoot ?? '(unknown)',
            $projectPath,
            $this->projectName,
        ));
    }

    /**
     * Whether an engine failure is this collision, told from its own wording.
     *
     * Matched on the phrase the engine uses rather than on an exit code,
     * because every provisioning failure shares that code. Narrow on purpose:
     * a message this does not recognise stays exactly as the engine wrote it,
     * which is the right default for a failure nobody has thought about yet.
     */
    public static function isRootConflict(string $engineFailure): bool
    {
        return str_contains($engineFailure, 'project root is already set to');
    }

    /**
     * The same guidance as conflictException(), for the case the pre-flight
     * could not see: the engine's record survives but the directory it names
     * is gone, so `describe` had nothing to report.
     */
    public function rootConflictException(string $projectPath, \Throwable $engineFailure): AdapterException
    {
        return new AdapterException(sprintf(
            "The engine already knows a project called \"%s\" at a different path, so it refused to configure "
            . "this one at:\n  %s\n\n"
            . "Project names are global to the machine, so a projects root that has moved — a new cockpit, or a "
            . "--projects-root or UPKEEP_PROJECTS_ROOT that differs from the one used before — collides with "
            . "whatever the old one registered.\n\n"
            . "Deregister the stale record and re-run:\n"
            . "  ddev stop --unlist %s\n\n"
            . "That removes the engine's record of it; it does not delete any directory or anything in one.\n\n"
            . "The engine said:\n%s",
            $this->projectName,
            $projectPath,
            $this->projectName,
            trim($engineFailure->getMessage()),
        ), 0, $engineFailure);
    }

    /**
     * Compared on the resolved path, so a symlinked or trailing-slashed root
     * is not mistaken for a different one. A path that does not exist yet —
     * which the wanted one usually does not — canonicalises to itself.
     */
    private static function canonical(string $path): string
    {
        $resolved = realpath($path);

        return rtrim($resolved === false ? $path : $resolved, '/');
    }
}
