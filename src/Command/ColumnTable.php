<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Output\OutputInterface;

/**
 * The wide fixed-width listing the report commands render: a gray header row,
 * columns padded to their widest cell, and per-cell colouring applied after
 * widths are measured so escape sequences never disturb the alignment.
 *
 * One implementation, because two hand-rolled copies had already begun to
 * differ in how they measured and padded.
 */
final readonly class ColumnTable
{
    private const GAP = 4;

    /**
     * @param list<string>       $headers
     * @param list<list<string>> $rows      raw, unformatted cells
     * @param \Closure(list<string>): list<string> $colorise decorates one row's
     *                                                      cells; must not change their visible length
     * @param list<string>       $groupKeys optional per-row grouping key; a
     *                                      blank line separates differing groups
     */
    public static function render(
        OutputInterface $output,
        array $headers,
        array $rows,
        \Closure $colorise,
        array $groupKeys = [],
    ): void {
        $widths = array_map('mb_strlen', $headers);
        foreach ($rows as $cells) {
            foreach ($cells as $i => $cell) {
                $widths[$i] = max($widths[$i] ?? 0, mb_strlen($cell));
            }
        }

        $headerLine = '';
        foreach ($headers as $i => $header) {
            $headerLine .= str_pad($header, $widths[$i] + self::GAP);
        }
        $output->writeln('<fg=gray>' . rtrim($headerLine) . '</>');
        $output->writeln('');

        $lastGroup = null;
        foreach ($rows as $index => $cells) {
            $group = $groupKeys[$index] ?? null;
            if ($group !== null && $lastGroup !== null && $group !== $lastGroup) {
                $output->writeln('');
            }
            $lastGroup = $group;

            $line = '';
            foreach ($colorise($cells) as $i => $formatted) {
                $line .= $formatted . str_repeat(' ', $widths[$i] - mb_strlen($cells[$i]) + self::GAP);
            }
            $output->writeln(rtrim($line));
        }
    }
}
