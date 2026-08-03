<?php

declare(strict_types=1);

namespace Upkeep\Command;

use Symfony\Component\Console\Input\ArgvInput;

/**
 * ArgvInput variant that keeps the application from hijacking --version.
 *
 * Symfony's Application::doRun() short-circuits into printing the app
 * version whenever any argv token matches --version/-V — including
 * `check ... --version=11`, where the option is the check/review commands'
 * target-core selector. That probe is the only hasParameterOption() caller
 * passing '--version', so answering "no" to exactly that probe disables the
 * hijack while normal option binding still parses --version=<N> for the
 * command. Trade-off (documented): `upkeep --version` no longer prints the
 * app version; the app header on `upkeep list` still shows it.
 *
 * bin/upkeep must pass an instance to Application::run() when it registers
 * CheckCommand/ReviewCommand, and must also drop the app definition's
 * default --version option (see AbstractMrCommand::configureMrSurface()).
 */
final class VersionOptionInput extends ArgvInput
{
    /**
     * @param string|string[] $values
     */
    public function hasParameterOption(string|array $values, bool $onlyParams = false): bool
    {
        if (\in_array('--version', (array) $values, true)) {
            return false;
        }

        return parent::hasParameterOption($values, $onlyParams);
    }
}
