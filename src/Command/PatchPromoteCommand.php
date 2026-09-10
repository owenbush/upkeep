<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\PatchPromotion;
use Upkeep\Drupal\DrupalUser;
use Upkeep\Patches\PatchAttribution;
use Upkeep\Workflow\ExitCode;
use Upkeep\Workflow\PatchContext;

/**
 * Turns a patch contribution into a branch a merge request can be opened from.
 *
 * The gap this closes: a patch and a merge request carry the same work, but
 * only one of them gets CI, review threads, or a fast lane. Re-rolling a patch
 * into an MR by hand is a dozen git commands nobody enjoys, so patches sit on
 * issues long after the project has moved to merge requests.
 *
 * **It stops at the commit.** The branch is made locally and nothing leaves
 * the machine; `upkeep publish` is the outward-facing half and already knows
 * how to push a work branch, open the MR, and report one that is already open.
 * Splitting there is not squeamishness — it is the same seam every other verb
 * here uses, and it means the step that publishes somebody else's work under
 * your account is one a human types.
 *
 * **Attribution is the feature, not decoration.** Promoting moves another
 * person's change into history under whoever pushes it. What the commit says
 * is the only durable record of whose work it was, so the message is built by
 * Patches\PatchAttribution and the author is resolved from drupal.org before
 * anything is applied — an unattributable patch is reported, never quietly
 * promoted as if it were yours. See that class for why there is no
 * `--author` line.
 *
 * Exit codes: 0 promoted, 2 the patch could not be applied or the context
 * could not be resolved. There is no 1 — nothing here supervises a check.
 */
#[AsCommand(
    name: 'patch:promote',
    description: 'Apply a drupal.org patch onto an issue work branch, credited to its author, ready to publish.',
)]
final class PatchPromoteCommand extends AbstractPatchCommand
{
    protected function configure(): void
    {
        $this->setHelp(<<<'HELP'
            Converts a patch contribution into a branch you can open a merge request from.

              <info>upkeep patch:promote pathauto 3597857</info>
              <info>upkeep patch:promote pathauto 3597857 --latest</info>
              <info>upkeep patch:promote pathauto 3597857 --file=NAME</info>
              <info>upkeep patch:promote pathauto 3597857 --partial</info>

            The commit credits whoever posted the patch, by name, in the message — promoting
            moves somebody else's work into history, and the commit is the durable record of
            whose it is. Nothing is pushed: run <info>upkeep publish</info> afterwards, which is
            the step that puts it on drupal.org.

            Afterwards, <info>upkeep check <module> --working-copy</info> runs the suite against the branch
            it made, and <info>upkeep dev <module></info> prints the site URL. A patch that only applied
            with reduced context is a weaker guarantee than a merge request implies, so
            checking before you publish is worth the minutes.

            <info>--partial</info> is for a patch that will not apply at all. Every hunk that still fits
            lands on the branch and the rest is left as <info><file>.rej</info> beside the file it could
            not change, which is where a re-roll starts. Nothing is committed — the commit
            carries the patch author's name, and half their patch is not what they wrote — so
            resolve the rejects, delete the .rej files, commit, and publish. It exits 1,
            because the patch did not apply.
            HELP);

        $this->configurePatchSurface();
        $this->addOption(
            'branch',
            null,
            InputOption::VALUE_REQUIRED,
            'Work branch to promote onto (default: the drupal.org <nid>-<slug> convention)',
        );
        // The re-roll door. Without it a patch that no longer applies is a
        // dead end: the report says which files are stale and stops, while
        // the work of re-rolling is exactly the work the failed apply was
        // doing.
        $this->addOption(
            'partial',
            null,
            InputOption::VALUE_NONE,
            'When the patch will not apply, keep the hunks that still fit and leave the rest as .rej files to '
            . 'resolve by hand — the start of a re-roll rather than a refusal',
        );
    }

    protected function perform(InputInterface $input, OutputInterface $output, SymfonyStyle $io): int
    {
        $context = $this->resolveContext($input, $io);
        self::describeContext($io, $context, self::stringOption($input, 'version'));

        $io->section('Attribution');
        $attribution = $this->attribute($io, $context);
        $io->writeln($attribution->subject());

        $branchName = self::stringOption($input, 'branch');
        $branch = $branchName !== null
            ? IssueBranch::named($context->issue->nid, $branchName)
            : IssueBranch::forIssue($context->issue->nid, $context->issue->title);

        $adapter = $this->adapter($input, $output);

        $io->section('Environment');
        $environment = $adapter->ensureEnv($context->module, $context->coreMajor);

        $io->section('Promote');
        $promotion = $adapter->promotePatch(
            $environment,
            $context->application(),
            $branch,
            $attribution->message(),
            self::baseRefresh($input),
            $input->getOption('partial') === true,
        );

        if (!$promotion->isComplete()) {
            return $this->reportPartial($io, $context, $branch, $promotion);
        }

        $io->success(sprintf(
            '%s now carries the patch at %s.',
            $branch->name,
            substr($promotion->requireSha(), 0, 8),
        ));
        $io->writeln($attribution->message());

        $io->writeln('<fg=gray>Nothing has been pushed. Run the checks against it, look at the site, then publish:</>');
        $io->writeln(sprintf('  <info>upkeep check %s --working-copy</info>', $context->module->name));
        $io->writeln(sprintf('  <info>upkeep dev %s</info>', $context->module->name));
        $io->writeln(sprintf(
            '  <info>upkeep publish %s %d</info>',
            $context->module->name,
            $context->issue->nid,
        ));

        return ExitCode::OK;
    }

    /**
     * A partial promotion: what landed, what did not, and what to do about it.
     *
     * Exit 1 rather than 0. The patch did not apply, which is the supervised
     * work failing — the same answer `patch:check` gives — and a script that
     * treated this as success would push a half-applied patch. Nothing here
     * is an upkeep failure, so it is not exit 2 either.
     */
    private function reportPartial(
        SymfonyStyle $io,
        PatchContext $context,
        IssueBranch $branch,
        PatchPromotion $promotion,
    ): int {
        $io->warning(sprintf(
            'The patch did not apply cleanly. %d file(s) landed on %s; %d still need doing by hand.',
            \count($promotion->applied),
            $branch->name,
            \count($promotion->rejected),
        ));

        if ($promotion->applied !== []) {
            $io->writeln('<fg=gray>Applied:</>');
            foreach ($promotion->applied as $file) {
                $io->writeln('  ' . $file);
            }
        }

        $io->writeln('<fg=gray>Rejected — each has a .rej file beside it holding the hunks that did not fit:</>');
        foreach ($promotion->rejected as $file) {
            $io->writeln(sprintf('  %s  <fg=gray>(%s.rej)</>', $file, $file));
        }

        $io->writeln('');
        $io->writeln('<fg=gray>Nothing is committed: the commit carries the patch author\'s name, and this is not</>');
        $io->writeln('<fg=gray>their work yet. Resolve the rejects, delete the .rej files, then:</>');
        $io->writeln(sprintf('  <info>upkeep dev %s</info>', $context->module->name));
        $io->writeln(sprintf('  <info>upkeep check %s --working-copy</info>', $context->module->name));
        $io->writeln(sprintf(
            '  <info>upkeep publish %s %d</info>',
            $context->module->name,
            $context->issue->nid,
        ));

        return ExitCode::FAILED;
    }

    /**
     * Resolves the patch's author, and says plainly when it cannot.
     *
     * A miss is not fatal — the patch is still somebody's work and the commit
     * still says so — but it is never silent, because a commit that dropped
     * the name is indistinguishable from one that never had a name to drop.
     */
    private function attribute(SymfonyStyle $io, PatchContext $context): PatchAttribution
    {
        $author = $this->resolveAuthor($context);

        if ($author === null) {
            $io->warning(
                'drupal.org records no readable account for this patch file, so the commit cannot name its '
                . 'author. It will say the work is not the promoter\'s and point at the issue — credit them '
                . 'there, on the issue, which is where drupal.org allocates credit anyway.',
            );
        } else {
            $io->writeln(sprintf('Patch posted by %s (%s).', $author->name, $author->profileUrl));
        }

        return PatchAttribution::forPatch($context->issue, $context->patch, $author, $this->promoter());
    }

    private function resolveAuthor(PatchContext $context): ?DrupalUser
    {
        $uid = $context->patch->ownerUid;

        return $uid === null ? null : $this->drupal()->user($uid);
    }

    /**
     * Who is doing the carrying. Best-effort and unauthenticated — it is a
     * courtesy line in a commit message, so an unset git identity omits it
     * rather than failing the promotion.
     */
    private function promoter(): ?string
    {
        $name = getenv('UPKEEP_PROMOTER');

        return $name !== false && trim($name) !== '' ? trim($name) : null;
    }
}
