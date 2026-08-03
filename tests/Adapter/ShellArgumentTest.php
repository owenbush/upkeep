<?php

declare(strict_types=1);

namespace Upkeep\Tests\Adapter;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\ShellArgument;

final class ShellArgumentTest extends TestCase
{
    public function testQuotesAPlainValue(): void
    {
        $this->assertSame("'token_or'", ShellArgument::quote('token_or'));
    }

    /**
     * @return list<array{string}>
     */
    public static function metacharacterProvider(): array
    {
        return [
            ['a; rm -rf /'],
            ['a && whoami'],
            ['a | tee /tmp/x'],
            ['$(id)'],
            ['`id`'],
            ['a > /tmp/x'],
            ['*'],
            ["a\nb"],
            ['a b'],
            ['$HOME'],
        ];
    }

    /**
     * The quoted form must survive `bash -c` as one literal word — the point
     * of the guard is that a value interpolated into a script body cannot
     * become script.
     */
    #[\PHPUnit\Framework\Attributes\DataProvider('metacharacterProvider')]
    public function testMetacharactersSurviveBashAsALiteralWord(string $value): void
    {
        $quoted = ShellArgument::quote($value);

        $out = shell_exec('bash -c ' . escapeshellarg('printf %s ' . $quoted));

        $this->assertSame($value, $out);
    }

    /** Single quotes are the one character that has to break out and back in. */
    public function testEmbeddedSingleQuotesAreEscaped(): void
    {
        $quoted = ShellArgument::quote("it's a 'value'");

        $this->assertSame("it's a 'value'", shell_exec('bash -c ' . escapeshellarg('printf %s ' . $quoted)));
    }

    public function testEmptyValueQuotesToAnEmptyWord(): void
    {
        $this->assertSame("''", ShellArgument::quote(''));
    }
}
