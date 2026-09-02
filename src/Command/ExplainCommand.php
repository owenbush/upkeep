<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Workflow\ExitCode;

/**
 * What a word upkeep printed actually means.
 *
 * Every other command answers a question about a module. This one answers a
 * question about the output itself, which until now had no answer anywhere: a
 * maintainer who saw `patch↑` on a dashboard row could read the source or
 * guess, and those were the options.
 *
 * Needs no cockpit, no token and no network — it is a glossary — so it works
 * from anywhere, including in the middle of being confused by something.
 */
#[AsCommand(
    name: 'explain',
    description: 'Explain a term from upkeep\'s output (run bare to list them all).',
)]
final class ExplainCommand extends UpkeepCommand
{
    protected function configure(): void
    {
        $this->addArgument(
            'term',
            InputArgument::OPTIONAL,
            'The word to explain, e.g. "patch↑", "stale", "unclaimed". Matches partially.',
        );
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $query = self::stringArgument($input, 'term');
        $matches = Glossary::search($query);

        if ($matches === []) {
            $io->writeln(sprintf('<fg=yellow>Nothing in upkeep\'s output is called "%s".</>', $query));
            $io->writeln('');
            $io->writeln('<fg=gray>upkeep explain — with no argument — lists every term.</>');

            // Not knowing a word is not a failure: the command did what was
            // asked and reported the answer, which happens to be "no such
            // term". A script asking about an unknown word wants to read that,
            // not to handle an error.
            return ExitCode::OK;
        }

        if ($query === '') {
            $io->writeln('<fg=gray>Every term upkeep prints. Narrow it with: upkeep explain <term></>');
            $io->writeln('');
        }

        $rows = [];
        foreach ($matches as $term => [$meaning, $where]) {
            $rows[] = [$term, $where, $meaning];
        }

        self::renderWrapped($output, $rows);

        return ExitCode::OK;
    }

    /**
     * Definitions are sentences, not cells, so they are wrapped under their
     * term rather than squeezed into a column — a table would put a paragraph
     * in a box eight characters wide on a narrow terminal.
     *
     * @param list<array{string, string, string}> $rows
     */
    private static function renderWrapped(OutputInterface $output, array $rows): void
    {
        foreach ($rows as [$term, $where, $meaning]) {
            $output->writeln(sprintf('<fg=cyan>%s</>  <fg=gray>%s</>', $term, $where));
            foreach (explode("\n", wordwrap($meaning, 74)) as $line) {
                $output->writeln('    ' . $line);
            }
            $output->writeln('');
        }
    }
}
