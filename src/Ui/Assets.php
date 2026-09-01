<?php

declare(strict_types=1);

namespace Upkeep\Ui;

/**
 * The page, its script and its stylesheet, read from disk.
 *
 * Kept as real files under `assets/ui/` rather than embedded in PHP strings so
 * they can be edited as what they are, and so the coverage floor measures PHP
 * rather than counting a heredoc of JavaScript as a covered line.
 *
 * Nothing is fetched from anywhere: the response's content-security policy
 * forbids it, and a maintainer's credential-holding localhost port is the last
 * place a CDN belongs.
 */
final readonly class Assets
{
    public function __construct(private string $dir)
    {
    }

    public static function bundled(): self
    {
        return new self(\dirname(__DIR__, 2) . '/assets/ui');
    }

    public function page(): string
    {
        return $this->read('index.html');
    }

    public function script(): string
    {
        return $this->read('app.js');
    }

    public function style(): string
    {
        return $this->read('app.css');
    }

    /**
     * What a browser holding a token from a previous run is shown.
     *
     * Its own file rather than a string in PHP for the same reason as the
     * others: it is a page, and it is edited as one.
     */
    public function expiredPage(): string
    {
        return $this->read('expired.html');
    }

    /**
     * A missing asset is the empty string, not an exception: it can only mean
     * a broken install, and a blank page that still serves its error JSON is
     * more diagnosable than a server that will not start.
     */
    private function read(string $name): string
    {
        $path = $this->dir . '/' . $name;

        return is_file($path) ? (string) file_get_contents($path) : '';
    }
}
