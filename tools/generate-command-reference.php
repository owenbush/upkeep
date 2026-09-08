<?php

declare(strict_types=1);

/**
 * Generates docs/commands.md from the CLI itself.
 *
 * A hand-written command reference is a second copy of the truth, and the copy
 * loses: the project site's table listed 11 of 27 commands, and had done for
 * long enough that the issue loop, the patch surface and the browser UI were
 * all missing from it. Symfony Console already knows every command, argument,
 * option, default and help text, so the reference is derived and gated in CI
 * (`composer docs:check`) rather than remembered.
 *
 * Not part of `src/`: this is build tooling, outside the coverage floor and
 * the adapter boundary, in the same category as tests/Integration/fixture/.
 *
 * Usage: php tools/generate-command-reference.php [--check]
 */

const INTERNAL = ['_complete', 'help', 'list'];

$root = \dirname(__DIR__);
$target = $root . '/docs/commands.md';
$check = \in_array('--check', $argv, true);

exec(escapeshellarg(PHP_BINARY) . ' ' . escapeshellarg($root . '/bin/upkeep') . ' list --format=json', $out, $status);
if ($status !== 0) {
    fwrite(STDERR, "Could not list the commands: `upkeep list --format=json` exited {$status}.\n");
    exit(1);
}

/** @var array{commands: list<array<string, mixed>>} $listing */
$listing = json_decode(implode("\n", $out), true, 512, JSON_THROW_ON_ERROR);

$commands = array_values(array_filter(
    $listing['commands'],
    static fn (array $command): bool => !\in_array($command['name'], INTERNAL, true),
));
usort($commands, static fn (array $a, array $b): int => strcmp($a['name'], $b['name']));

// Global options are the ones every command carries. Computed rather than
// listed, so a Symfony release that adds or drops one needs no edit here.
$global = array_keys($commands[0]['definition']['options']);
foreach ($commands as $command) {
    $global = array_intersect($global, array_keys($command['definition']['options']));
}
$globalDefinitions = array_intersect_key($commands[0]['definition']['options'], array_flip($global));
ksort($globalDefinitions);

$md = preamble($commands);
$md .= globalOptions($globalDefinitions);
foreach ($commands as $command) {
    $md .= renderCommand($command, $global);
}

if ($check) {
    $current = is_file($target) ? (string) file_get_contents($target) : '';
    if ($current === $md) {
        echo "docs/commands.md is up to date.\n";
        exit(0);
    }
    fwrite(STDERR, "docs/commands.md is out of date. Run `composer docs:commands` and commit the result.\n");
    exit(1);
}

file_put_contents($target, $md);
printf("Wrote %s (%d commands, %d lines).\n", $target, \count($commands), substr_count($md, "\n"));

/**
 * @param list<array<string, mixed>> $commands
 */
function preamble(array $commands): string
{
    $rows = '';
    foreach ($commands as $command) {
        $rows .= sprintf(
            "| [`%s`](#%s) | %s |\n",
            $command['name'],
            anchor($command['name']),
            cell((string) $command['description']),
        );
    }

    return "# Command reference\n\n"
        . "<!-- GENERATED FILE. Run `composer docs:commands` to regenerate; do not edit by hand. -->\n\n"
        . "Every command upkeep ships, generated from the CLI itself so it cannot drift from what\n"
        . "the binary actually does. `composer docs:check` fails the build when this file and the\n"
        . "commands disagree.\n\n"
        . "Conventions worth knowing before the list:\n\n"
        . "- **`--version` is always the target Drupal core major**, never an application version.\n"
        . "  The application-level `-V` is deliberately removed.\n"
        . "- **Exit codes are a contract**: `0` the command did what was asked, `1` the work it\n"
        . "  supervised failed, `2` upkeep could not do the job.\n"
        . "- **Reading needs no credential.** A token is required only to merge, comment or publish.\n\n"
        . "| Command | What it does |\n| --- | --- |\n" . $rows . "\n";
}

/**
 * @param array<string, array<string, mixed>> $options
 */
function globalOptions(array $options): string
{
    $md = "## Global options\n\n"
        . "Accepted by every command, so they are listed once rather than repeated below.\n\n"
        . "| Option | What it does |\n| --- | --- |\n";
    foreach ($options as $option) {
        $md .= sprintf("| `%s` | %s |\n", cell(optionName($option)), cell((string) $option['description']));
    }

    return $md . "\n";
}

/**
 * @param array<string, mixed> $command
 * @param list<string>         $global
 */
function renderCommand(array $command, array $global): string
{
    $md = sprintf("## `upkeep %s`\n\n%s\n\n", $command['name'], escape((string) $command['description']));

    foreach ($command['usage'] as $usage) {
        $md .= sprintf("```\nupkeep %s\n```\n\n", $usage);
    }

    $help = helpBody((string) $command['help'], (string) $command['description']);
    if ($help !== '') {
        $md .= $help . "\n";
    }

    $arguments = $command['definition']['arguments'];
    if ($arguments !== []) {
        $md .= "**Arguments**\n\n| Argument | Required | What it is |\n| --- | --- | --- |\n";
        foreach ($arguments as $argument) {
            $md .= sprintf(
                "| `%s` | %s | %s |\n",
                $argument['name'],
                $argument['is_required'] ? 'yes' : 'no',
                cell((string) $argument['description']),
            );
        }
        $md .= "\n";
    }

    $options = array_diff_key($command['definition']['options'], array_flip($global));
    if ($options !== []) {
        $md .= "**Options**\n\n| Option | What it does |\n| --- | --- |\n";
        foreach ($options as $option) {
            $md .= sprintf(
                "| `%s` | %s%s |\n",
                cell(optionName($option)),
                cell((string) $option['description']),
                defaultSuffix($option),
            );
        }
        $md .= "\n";
    }

    return $md;
}

/**
 * The rich help text, re-flowed for a page rather than a terminal.
 *
 * Console help is hard-wrapped at terminal width with examples indented two
 * spaces; rendered raw, the wrapping survives as ragged prose and the examples
 * read as ordinary text. Indented runs become fenced blocks and wrapped
 * paragraphs are rejoined.
 */
function helpBody(string $help, string $description): string
{
    $help = strip($help);
    $lines = explode("\n", rtrim($help));

    // Symfony falls back to the description when a command sets no help.
    if (trim($help) === trim($description)) {
        return '';
    }

    $blocks = [];
    $paragraph = [];
    $code = [];
    $flush = static function () use (&$blocks, &$paragraph, &$code): void {
        if ($paragraph !== []) {
            $blocks[] = escape(implode(' ', $paragraph));
            $paragraph = [];
        }
        if ($code !== []) {
            $blocks[] = "```\n" . implode("\n", $code) . "\n```";
            $code = [];
        }
    };

    foreach ($lines as $line) {
        if (trim($line) === '') {
            $flush();
            continue;
        }
        if (str_starts_with($line, '  ')) {
            if ($paragraph !== []) {
                $flush();
            }
            $code[] = ltrim($line);
            continue;
        }
        if ($code !== []) {
            $flush();
        }
        $paragraph[] = trim($line);
    }
    $flush();

    return $blocks === [] ? '' : implode("\n\n", $blocks) . "\n";
}

/**
 * @param array<string, mixed> $option
 */
function optionName(array $option): string
{
    $name = $option['shortcut'] !== '' ? $option['name'] . '|' . $option['shortcut'] : $option['name'];

    return $option['accept_value'] === true ? $name . '=' . strtoupper(ltrim($option['name'], '-')) : $name;
}

/**
 * @param array<string, mixed> $option
 */
function defaultSuffix(array $option): string
{
    $default = $option['default'];
    if ($default === null || $default === false || $default === '' || $default === []) {
        return '';
    }

    return sprintf(' Default: `%s`.', \is_array($default) ? implode(', ', $default) : (string) $default);
}

function anchor(string $command): string
{
    return 'upkeep-' . str_replace(':', '', $command);
}

/** Console output carries Symfony style tags; a page must not. */
function strip(string $text): string
{
    return preg_replace('/<\/?(?:info|comment|question|error|href=[^>]*)>|<\/>/', '', $text) ?? $text;
}

function escape(string $text): string
{
    return str_replace(['<', '>'], ['&lt;', '&gt;'], strip($text));
}

/**
 * A table cell. The pipe needs escaping even inside backticks — GitHub ends
 * the cell at it regardless — and `--help|-h` would otherwise split the row.
 */
function cell(string $text): string
{
    return str_replace('|', '\\|', escape($text));
}
