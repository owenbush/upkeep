<?php

declare(strict_types=1);

namespace Upkeep\Tests\Workflow;

use PHPUnit\Framework\TestCase;

/**
 * That the nightly full check tolerates refusals upkeep can actually make.
 *
 * `.github/workflows/full-check.yml` names live drupal.org state — one issue's
 * patch, one merge request — and both move. A patch stops applying when the
 * branch changes; a merge request gets merged. Both are exit 2, and both are
 * the tool working rather than failing, so the workflow matches the refusal
 * text and warns instead of going red.
 *
 * Which puts a copy of upkeep's own wording in a YAML file, where nothing
 * would notice it going stale. Silently, too: a tolerance that no longer
 * matches does not error, it just stops tolerating, and the next legitimate
 * refusal wakes somebody at eight in the morning for a tool that is fine. That
 * is what happened to make this necessary in the first place, so it gets a
 * guard rather than a comment.
 */
final class NightlyToleranceTest extends TestCase
{
    private const WORKFLOW = __DIR__ . '/../../.github/workflows/full-check.yml';

    /**
     * Every pattern the workflow forgives must match something upkeep says.
     */
    public function testEachToleratedRefusalMatchesAMessageUpkeepCanProduce(): void
    {
        $messages = self::refusalStrings();
        $patterns = self::tolerances();

        self::assertNotEmpty($patterns, 'The workflow declares at least one tolerated refusal.');

        // Each alternative separately. A pattern like `a|b` is satisfied by
        // either half, so checking it whole lets one branch rot behind the
        // other — which is exactly what happened the first time this test was
        // written: the merge-request wording was changed and the test stayed
        // green on the unrelated half.
        foreach ($patterns as $pattern) {
            foreach (self::alternatives($pattern) as $alternative) {
                $matched = array_filter(
                    $messages,
                    static fn (string $m): bool => preg_match('/' . $alternative . '/i', $m) === 1,
                );

                self::assertNotEmpty($matched, sprintf(
                    'The nightly forgives "%s" (from "%s"), but no message in src/ says anything of the '
                    . 'kind. Either the wording moved and the tolerance stopped working, or it was never '
                    . 'right.',
                    $alternative,
                    $pattern,
                ));
            }
        }
    }

    /**
     * Top-level alternatives of a pattern, leaving grouped ones alone.
     *
     * `a|b` is two things to check; `(a|b) c` is one.
     *
     * @return list<string>
     */
    private static function alternatives(string $pattern): array
    {
        $parts = [];
        $current = '';
        $depth = 0;

        foreach (str_split($pattern) as $character) {
            if ($character === '(' || $character === '[') {
                ++$depth;
            } elseif ($character === ')' || $character === ']') {
                --$depth;
            } elseif ($character === '|' && $depth === 0) {
                $parts[] = $current;
                $current = '';
                continue;
            }
            $current .= $character;
        }
        $parts[] = $current;

        return array_values(array_filter($parts, static fn (string $p): bool => trim($p) !== ''));
    }

    /**
     * The refusal patterns declared in the workflow's expect-upkeep.sh calls.
     *
     * @return list<string>
     */
    private static function tolerances(): array
    {
        $workflow = (string) file_get_contents(self::WORKFLOW);
        preg_match_all("/expect-upkeep\.sh\s+'[^']*'\s+'([^']*)'/", $workflow, $matches);

        return array_values(array_filter($matches[1], static fn (string $p): bool => $p !== '-'));
    }

    /**
     * Every string literal in src/.
     *
     * Tokenised rather than matched with a regex. The obvious
     * `'([^']+)'` desynchronises on the first apostrophe in a comment — "the
     * module's own" — and from there pairs the closing quote of one string
     * with the opening quote of the next, which is how the first version of
     * this test managed to miss a message sitting in plain sight.
     *
     * Comments are excluded by construction, so prose describing a message
     * cannot stand in for the message.
     *
     * @return list<string>
     */
    private static function refusalStrings(): array
    {
        $found = [];
        $files = new \RecursiveIteratorIterator(
            new \RecursiveDirectoryIterator(__DIR__ . '/../../src', \FilesystemIterator::SKIP_DOTS),
        );

        foreach ($files as $file) {
            if (!$file instanceof \SplFileInfo || $file->getExtension() !== 'php') {
                continue;
            }

            foreach (token_get_all((string) file_get_contents($file->getPathname())) as $token) {
                if (\is_array($token) && $token[0] === \T_CONSTANT_ENCAPSED_STRING) {
                    $found[] = trim($token[1], "'\"");
                }
            }
        }

        return $found;
    }
}
