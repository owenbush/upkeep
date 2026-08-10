<?php

declare(strict_types=1);

namespace Upkeep\Ui;

use Symfony\Component\Process\Process;
use Upkeep\Cockpit\Cockpit;
use Upkeep\Gitlab\TokenResolver;

/**
 * How the UI server is started: PHP's own built-in server, with the router
 * script as its front controller.
 *
 * No new runtime dependency — `php -S` ships with the interpreter this tool
 * already requires, and symfony/process is already here to launch it. For a
 * single operator on their own machine that is the whole requirement.
 *
 * Two things travel in the environment rather than on the command line,
 * because a command line is readable by every process on the machine: the
 * launch token, and the GitLab credential.
 */
final readonly class UiServer
{
    public const HOST = '127.0.0.1';
    public const DEFAULT_PORT = 8721;

    /**
     * More than one worker, or a poll for a running job's output would queue
     * behind whatever else the page asked for. Modest, because this serves one
     * operator and each worker is a PHP process.
     */
    private const WORKERS = '4';

    public function __construct(
        private LaunchToken $token,
        private Cockpit $cockpit,
        private int $port,
    ) {
    }

    /** @return list<string> */
    public function arguments(): array
    {
        return [
            \PHP_BINARY,
            '-d',
            'variables_order=EGPCS',
            '-S',
            self::HOST . ':' . $this->port,
            '-t',
            $this->documentRoot(),
            $this->router(),
        ];
    }

    /**
     * The document root is the assets directory, but every request is routed
     * through the front controller anyway — the built-in server only serves a
     * file directly when the router returns false, and this one never does.
     */
    public function documentRoot(): string
    {
        return \dirname(__DIR__, 2) . '/assets/ui';
    }

    public function router(): string
    {
        return \dirname(__DIR__, 2) . '/bin/upkeep-ui-router.php';
    }

    /**
     * The child's environment.
     *
     * The GitLab credential is forwarded deliberately, and it is the one place
     * this tool does that. `Security\CredentialEnvironment` strips it from
     * child processes because an arbitrary child might echo its environment
     * into output that gets logged — but these children are `upkeep` itself,
     * which needs the token to do the work being asked of it and never prints
     * it. The captured output is redacted again on its way to the browser
     * (see Api::jobResponse) rather than trusted.
     *
     * @return array<string, string>
     */
    public function environment(): array
    {
        $env = [
            'UPKEEP_UI_TOKEN' => $this->token->value,
            'UPKEEP_UI_COCKPIT' => $this->cockpit->root,
            'UPKEEP_UI_BINARY' => self::binary(),
            'PHP_CLI_SERVER_WORKERS' => self::WORKERS,
        ];

        $credential = getenv(TokenResolver::DEFAULT_ENV_VAR);
        if (\is_string($credential) && trim($credential) !== '') {
            $env[TokenResolver::DEFAULT_ENV_VAR] = $credential;
        }

        return $env;
    }

    /** The `upkeep` binary a job should run: this one. */
    public static function binary(): string
    {
        return \dirname(__DIR__, 2) . '/bin/upkeep';
    }

    /**
     * The default way to run it: in the foreground, streaming nothing, until
     * interrupted. Separated from the command so a test can drive the command
     * without binding a port.
     *
     * @return \Closure(string, list<string>, array<string, string>): int
     */
    public function runner(): \Closure
    {
        return self::run(...);
    }

    /**
     * No timeout and no idle timeout: this runs until the operator interrupts
     * it, which is the whole shape of the command.
     *
     * @param list<string>          $arguments
     * @param array<string, string> $environment
     */
    public static function run(string $cwd, array $arguments, array $environment): int
    {
        $process = new Process($arguments, $cwd, $environment, null, null);
        $process->run();

        return $process->getExitCode() ?? 1;
    }
}
