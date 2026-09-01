<?php

declare(strict_types=1);

namespace Upkeep\Ui\Jobs;

use Upkeep\Adapter\ProjectName;

/**
 * The complete set of things the browser may ask upkeep to do, and the only
 * place a request turns into an argument vector.
 *
 * **The browser never supplies argv.** It names an action and supplies
 * parameters, each of which is validated into a shape it already had to have —
 * a registry module name, a core major, a positive integer. Nothing else
 * reaches a command line. A UI that accepted `{"argv": [...]}` would be a
 * remote shell wearing a dashboard, and the loopback interface is not a
 * meaningful barrier to that.
 *
 * Merging is deliberately absent. The Drupal Association stance is one human
 * approval per merge, and `merge --fast-lane` earns that with an interactive
 * per-MR prompt. A button that posts an action name is not that prompt, and a
 * table of checkboxes next to a "merge selected" control is precisely the
 * batch mode the CLI refuses to have. Merging from the UI needs its own design
 * before it needs code.
 *
 * `publish` is the one action that reaches outside this machine, and it is
 * *not* the same call: it proposes work for review, which is what the merge
 * policy exists to protect rather than to restrict. The page still confirms it
 * before asking, because a merge request is public the moment it exists.
 */
final readonly class JobAction
{
    /**
     * @param list<string> $argv  upkeep arguments, without the binary
     */
    private function __construct(
        public string $label,
        public array $argv,
    ) {
    }

    /**
     * Builds the action a request names, or null when it names none — an
     * unknown action is not an error to describe in detail, because describing
     * it would confirm which names exist.
     *
     * @param array<string, string> $params
     */
    public static function build(string $action, array $params): ?self
    {
        $module = self::module($params['module'] ?? null);
        if ($module === null) {
            return null;
        }

        $core = self::core($params['core'] ?? null);

        return match ($action) {
            'check' => self::check($module, $core, self::positive($params['mr'] ?? null)),
            'patch-check' => self::patchCheck($module, $core, self::positive($params['issue'] ?? null)),
            'refresh' => new self(
                sprintf('refresh %s', $module),
                ['dashboard', '--refresh=' . $module, '--no-interaction'],
            ),
            'start' => self::start($module, $core, self::positive($params['issue'] ?? null)),
            'publish' => self::publish($module, $core, self::positive($params['issue'] ?? null)),
            default => null,
        };
    }

    private static function check(string $module, ?string $core, ?int $mr): ?self
    {
        if ($mr === null) {
            return null;
        }

        return new self(
            sprintf('check %s !%d', $module, $mr),
            array_merge(['check', $module, (string) $mr], self::coreOption($core), ['--no-interaction']),
        );
    }

    private static function patchCheck(string $module, ?string $core, ?int $issue): ?self
    {
        if ($issue === null) {
            return null;
        }

        // --latest, because there is nobody at a terminal to answer the
        // picker. The UI names which patch it took in the job label, so the
        // choice is still stated rather than hidden.
        return new self(
            sprintf('patch:check %s #%d', $module, $issue),
            array_merge(
                ['patch:check', $module, (string) $issue],
                self::coreOption($core),
                ['--latest', '--no-interaction'],
            ),
        );
    }

    /**
     * Open a work branch for an issue. Local and non-destructive: `start`
     * resumes an existing branch as it stands rather than resetting it, so a
     * button that fires twice cannot lose anything.
     */
    private static function start(string $module, ?string $core, ?int $issue): ?self
    {
        if ($issue === null) {
            return null;
        }

        return new self(
            sprintf('start %s #%d', $module, $issue),
            array_merge(['start', $module, (string) $issue], self::coreOption($core), ['--no-interaction']),
        );
    }

    /**
     * Push a work branch and open its merge request.
     *
     * The only action here that reaches outside this machine, so the page
     * confirms it before asking — a merge request is public the moment it
     * exists, and closing one is not the same as never having opened it.
     * Still not a merge: proposing work for review is the opposite of the risk
     * the one-approval-per-merge stance manages, and no action name maps onto
     * `merge` at all.
     */
    private static function publish(string $module, ?string $core, ?int $issue): ?self
    {
        if ($issue === null) {
            return null;
        }

        return new self(
            sprintf('publish %s #%d', $module, $issue),
            array_merge(['publish', $module, (string) $issue], self::coreOption($core), ['--no-interaction']),
        );
    }

    /** @return list<string> */
    private static function coreOption(?string $core): array
    {
        return $core === null ? [] : ['--version=' . $core];
    }

    /**
     * Module names are validated against the same rule the registry enforces,
     * so a name that reaches a command line is one that could have named a
     * directory anyway.
     */
    private static function module(?string $value): ?string
    {
        return $value !== null && ProjectName::isModuleName($value) ? $value : null;
    }

    private static function core(?string $value): ?string
    {
        return $value !== null && ProjectName::isCoreMajor($value) ? $value : null;
    }

    private static function positive(?string $value): ?int
    {
        return $value !== null && preg_match('/^[1-9]\d{0,9}$/', $value) === 1 ? (int) $value : null;
    }
}
