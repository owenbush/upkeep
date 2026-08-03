<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use PHPUnit\Event\Application\Finished;
use PHPUnit\Event\Application\FinishedSubscriber;
use PHPUnit\Runner\CodeCoverage;
use PHPUnit\Runner\Extension\Extension;
use PHPUnit\Runner\Extension\Facade;
use PHPUnit\Runner\Extension\ParameterCollection;
use PHPUnit\TextUI\Configuration\Configuration;

use function fwrite;
use function register_shutdown_function;
use function sprintf;

use const PHP_EOL;
use const STDERR;

/**
 * Fails the run when line coverage drops below a configured minimum.
 *
 * PHPUnit 11.5 has no built-in minimum-coverage threshold: neither the
 * `<coverage>` element in `phpunit.xsd` nor the CLI exposes one, so there is
 * nothing to switch on. This extension supplies the missing gate, and it is
 * registered in `phpunit.xml.dist` precisely so it applies to a bare
 * `vendor/bin/phpunit` exactly as it does in CI — a threshold that only
 * existed as a CI command-line flag would do nothing for a developer running
 * the suite by hand, which is the case that matters most.
 *
 * The exit code is set from a shutdown function rather than returned, because
 * a subscriber has no way to influence the shell exit code PHPUnit computes.
 * `Application\Finished` is emitted after the result summary and after the
 * coverage reports are generated, so by the time this runs the numbers are
 * final and the message lands at the very end of the output.
 *
 * When coverage collection is not active — `--no-coverage`, or no PCOV/Xdebug
 * driver present — there is nothing to measure. That case warns loudly rather
 * than failing, so that the absence of the gate is visible instead of silent,
 * without making the suite unrunnable for someone who has no coverage driver
 * installed.
 */
final class CoverageThresholdExtension implements Extension, FinishedSubscriber
{
    private const DEFAULT_MINIMUM = 100.0;

    private float $minimumLineCoverage = self::DEFAULT_MINIMUM;

    public function bootstrap(
        Configuration $configuration,
        Facade $facade,
        ParameterCollection $parameters,
    ): void {
        if ($parameters->has('minimumLineCoverage')) {
            $this->minimumLineCoverage = (float) $parameters->get('minimumLineCoverage');
        }

        $facade->registerSubscriber($this);
    }

    public function notify(Finished $event): void
    {
        $coverage = CodeCoverage::instance();

        if (!$coverage->isActive()) {
            fwrite(
                STDERR,
                PHP_EOL . sprintf(
                    'WARNING: the %.2f%% line-coverage threshold was NOT enforced: '
                    . 'no coverage data was collected.' . PHP_EOL,
                    $this->minimumLineCoverage,
                ),
            );

            return;
        }

        $report = $coverage->codeCoverage()->getReport();
        $executable = $report->numberOfExecutableLines();
        $executed = $report->numberOfExecutedLines();
        $percentage = $executable === 0 ? 0.0 : ($executed / $executable) * 100.0;

        if ($percentage >= $this->minimumLineCoverage) {
            return;
        }

        fwrite(
            STDERR,
            PHP_EOL . sprintf(
                'FAILURE: line coverage %.2f%% (%d/%d lines) is below the required minimum of %.2f%%.'
                . PHP_EOL,
                $percentage,
                $executed,
                $executable,
                $this->minimumLineCoverage,
            ),
        );

        register_shutdown_function(static function (): void {
            exit(1);
        });
    }
}
