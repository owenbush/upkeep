<?php

declare(strict_types=1);

/**
 * Generates the answers the Go port is held to, from the PHP implementation.
 *
 * The PHP side is the specification: it is the one that has been run against
 * real drupal.org and GitLab data. Rather than reimplement composer/semver and
 * hope, the Go implementation is compared against its answers case by case.
 *
 *   php testdata_gen.php                 # corpus.json — real and awkward cases, committed
 *   php testdata_gen.php --fuzz=20000    # corpus-fuzz.json — random cases, not committed
 *
 * The random pass is not decoration. Real core_version_requirement strings are
 * carets and nothing else, so they exercise one shape; 2,703 random ones found
 * a semantic difference that 161 real ones could not.
 */

require __DIR__ . '/../vendor/autoload.php';

use Composer\Semver\Constraint\Constraint;
use Composer\Semver\Constraint\MultiConstraint;
use Composer\Semver\Intervals;
use Composer\Semver\VersionParser;

const CORES = ['7', '8', '9', '10', '11', '12', '13'];

/** Constraints seen in the wild, on real module branches. */
const REAL = [
    '^10 || ^11 || ^12', '^10 || ^11', '^10.1 || ^11 || ^12', '^10.2 || ^11 || ^12',
    '^10.2', '^10.3 || ^11 || ^12', '^11', '^7', '^8 || ^9', '^9.2 || ^10',
    '^10.2 || ^11 || ^12 || ^13', '*', '^12', '^13', '11.x', '10.*', '^11 || ^12',
];

/**
 * Constraints bounded *inside* a major. These are what killed the first
 * attempt, which probed sample versions rather than computing intervals: every
 * one of them really does support the core, and probing said it did not.
 */
const NARROW = [
    '~10.1', '~10.1.0', '>=9.2', '>=10.2 <11', '>=10 <10.2', '>=11.1',
    '>=10.50 <10.51', '~10.4.0', '10.4.*', '>=10.7 <10.8', '~10.11.0',
    '>=10.123 <10.124', '^10.4.5', '>=11.2.1 <11.2.9', '10.6.*', '~10.50',
];

function intersects(VersionParser $parser, string $constraint, string $core): ?bool
{
    $major = new MultiConstraint([
        new Constraint('>=', $core . '.0.0.0-dev'),
        new Constraint('<', ((int) $core + 1) . '.0.0.0-dev'),
    ]);

    try {
        return Intervals::haveIntersections($parser->parseConstraints($constraint), $major);
    } catch (\Throwable) {
        // Composer refuses it, so the port may refuse it too; nothing to assert.
        return null;
    }
}

/** @param list<string> $constraints */
function rows(VersionParser $parser, array $constraints): array
{
    $rows = [];
    foreach ($constraints as $constraint) {
        foreach (CORES as $core) {
            $answer = intersects($parser, $constraint, $core);
            if ($answer !== null) {
                $rows[] = ['constraint' => $constraint, 'core' => $core, 'applies' => $answer];
            }
        }
    }

    return $rows;
}

function randomConstraint(): string
{
    $operators = ['^', '~', '>=', '<=', '>', '<', '', ''];
    $term = static function () use ($operators): string {
        $version = (string) mt_rand(7, 13);
        $depth = mt_rand(0, 2);
        if ($depth >= 1) {
            $version .= '.' . (mt_rand(0, 20) === 0 ? '*' : (string) mt_rand(0, 12));
        }
        if ($depth === 2 && !str_contains($version, '*')) {
            $version .= '.' . (mt_rand(0, 20) === 0 ? '*' : (string) mt_rand(0, 30));
        }

        return $operators[mt_rand(0, \count($operators) - 1)] . $version;
    };

    $alternatives = [];
    for ($a = 0, $alts = mt_rand(1, 3); $a < $alts; $a++) {
        $terms = [];
        for ($b = 0, $ands = mt_rand(1, 2); $b < $ands; $b++) {
            $terms[] = $term();
        }
        $alternatives[] = implode(' ', $terms);
    }

    return implode(' || ', $alternatives);
}

$parser = new VersionParser();
$fuzz = 0;
foreach ($argv as $argument) {
    if (str_starts_with($argument, '--fuzz=')) {
        $fuzz = (int) substr($argument, 7);
    }
}

if ($fuzz > 0) {
    // Seeded, so a failure can be reproduced exactly.
    mt_srand(777);
    $rows = [];
    for ($i = 0; $i < $fuzz; $i++) {
        $constraint = randomConstraint();
        $core = CORES[mt_rand(0, \count(CORES) - 1)];
        $answer = intersects($parser, $constraint, $core);
        if ($answer !== null) {
            $rows[] = ['constraint' => $constraint, 'core' => $core, 'applies' => $answer];
        }
    }
    file_put_contents(__DIR__ . '/corpus-fuzz.json', json_encode(['intersects' => $rows]) . "\n");
    printf("corpus-fuzz.json: %d parseable random cases\n", \count($rows));

    exit(0);
}

$stability = [];
foreach (['12.0.0-alpha1', '11.4.6', '13.0.0-beta2', '12.0.0-rc1', '12.x-dev', '1.0.2', '8.x-1.0-alpha3'] as $v) {
    $stability[$v] = VersionParser::parseStability($v);
}

$corpus = [
    'intersects' => array_merge(rows($parser, REAL), rows($parser, NARROW)),
    'stability' => $stability,
];

file_put_contents(__DIR__ . '/corpus.json', json_encode($corpus, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n");
printf("corpus.json: %d intersection cases, %d stability cases\n", \count($corpus['intersects']), \count($stability));
