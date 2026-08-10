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
