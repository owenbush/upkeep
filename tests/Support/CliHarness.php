<?php

declare(strict_types=1);

namespace Upkeep\Tests\Support;

use Symfony\Component\Console\Application;
use Symfony\Component\Console\Output\BufferedOutput;
use Upkeep\Adapter\CommandRunner;
use Symfony\Contracts\HttpClient\HttpClientInterface;
use Upkeep\Adapter\EngineAdapterFactory;
use Upkeep\Adapter\EngineAdapterInterface;
use Upkeep\Adapter\VolumeProbe;
use Upkeep\Dashboard\DashboardCache;
use Upkeep\Dashboard\ModuleSnapshot;
use Upkeep\Command\ApiProbeCommand;
use Upkeep\Command\BaseArtifactsBuildCommand;
use Upkeep\Command\BaseArtifactsStatusCommand;
use Upkeep\Command\CheckCommand;
use Upkeep\Command\DashboardCommand;
use Upkeep\Command\DevCommand;
use Upkeep\Command\EnvPathCommand;
use Upkeep\Command\ExecCommand;
use Upkeep\Command\ExplainCommand;
use Upkeep\Command\InitCommand;
use Upkeep\Command\IssuesCommand;
use Upkeep\Command\IssueCommand;
use Upkeep\Command\MergeCommand;
use Upkeep\Command\ModulesAddCommand;
use Upkeep\Command\ModulesCommand;
use Upkeep\Command\NeedsWorkCommand;
use Upkeep\Command\NotesCommand;
use Upkeep\Command\PatchApplyCommand;
use Upkeep\Command\PatchCheckCommand;
use Upkeep\Command\PatchPromoteCommand;
use Upkeep\Command\PatchesCommand;
use Upkeep\Command\PruneCommand;
use Upkeep\Command\PublishCommand;
use Upkeep\Command\ReviewCommand;
use Upkeep\Command\StartCommand;
use Upkeep\Command\StatusCommand;
use Upkeep\Command\UiCommand;
use Upkeep\Command\VersionOptionInput;
use Upkeep\Drupal\DrupalOrgClient;
use Upkeep\Gitlab\GitlabClient;

/**
 * Drives the real console application end-to-end against a throwaway cockpit.
 *
 * What it keeps real: the command registration list, the option definitions,
 * argv parsing (including the VersionOptionInput seam that makes `--version`
 * the target-core selector), the exit-code mapping in UpkeepCommand, and
 * every resolution rule the commands apply. Those are the things these tests
 * exist to verify, so none of them is stubbed.
 *
 * What it fakes, and only this: the engine (an EngineAdapterInterface double
 * rather than ddev + docker), GitLab and drupal.org HTTP (injected clients
 * over MockHttpClient), and the docker volume probe. The suite therefore
 * passes with docker stopped and networking unavailable, in milliseconds.
 *
 * Hermeticity is enforced, not assumed. On create() the harness repoints
 * $HOME and $XDG_CONFIG_HOME at a temporary tree and clears every UPKEEP_*
 * variable, so nothing a command does can reach the real ~/.upkeep, the real
 * ~/.config/upkeep/drupal-pat, or a real cockpit even if it ignores its
 * options entirely — and a token accidentally exported in the developer's
 * shell cannot make a test pass that would fail in CI. destroy() puts the
 * environment, the working directory and the filesystem back.
 *
 * Typical use:
 *
 *     $cli = CliHarness::create('exit-codes');
 *     $cli->registerModule('widget');
 *     $cli->withEngine(FakeEngineAdapter::withEnvPath('/tmp/env'));
 *     $exit = $cli->run('exec', 'widget', '--', 'true');
 *     // $cli->display() holds everything the command wrote.
 *
 * The default working directory is the harness cockpit, so a run with no
 * --cockpit and no $UPKEEP_COCKPIT resolves there through the documented cwd
 * fallback.
 */
final class CliHarness
{
    /** The temporary tree, and the $HOME every command sees. */
    public readonly string $home;

    /** The cockpit at <home>/cockpit, and the default working directory. */
    public readonly string $cockpit;

    private EngineAdapterFactory $engines;

    private ?GitlabClient $gitlab = null;

    private ?DrupalOrgClient $drupalOrg = null;

    private ?CommandRunner $commandRunner = null;

    private readonly EnvironmentGuard $environment;

    private readonly string $originalCwd;

    /** $PATH as the process had it, so repeated prepends do not accumulate. */
    private readonly string $originalPath;

    private string $errorDisplay = '';

    /** @var array<string, array{project: string, core_versions: list<string>}> */
    private array $modules = [];

    /** @var list<string> scripted answers for interactive prompts */
    private array $inputs = [];

    private string $display = '';

    private ?HttpClientInterface $patchDownloader = null;

    private function __construct(string $home, string $cockpit, string $originalCwd)
    {
        $this->home = $home;
        $this->cockpit = $cockpit;
        $this->originalCwd = $originalCwd;
        $path = getenv('PATH');
        $this->originalPath = $path === false ? '/usr/bin:/bin' : $path;
        $this->environment = new EnvironmentGuard();
        $this->engines = new StubEngineAdapterFactory(FakeEngineAdapter::withEnvPath(null));
    }

    /**
     * @param string $label appears in the temporary directory name, so a
     *                      leaked directory names the test that leaked it
     */
    public static function create(string $label = 'cli'): self
    {
        $root = sys_get_temp_dir() . '/upkeep-' . $label . '-' . bin2hex(random_bytes(4));
        if (!mkdir($root . '/cockpit', 0o700, true)) {
            throw new \RuntimeException('Could not create the temporary cockpit at ' . $root);
        }

        $home = realpath($root);
        $cwd = getcwd();
        if ($home === false || $cwd === false) {
            throw new \RuntimeException('Could not resolve the temporary cockpit at ' . $root);
        }

        $harness = new self($home, $home . '/cockpit', $cwd);
        $harness->isolateEnvironment();
        $harness->inDirectory($harness->cockpit);

        return $harness;
    }

    // ------------------------------------------------------------- wiring

    /** Uses this engine double for every command that takes an engine. */
    public function withEngine(EngineAdapterInterface $adapter): self
    {
        $this->engines = new StubEngineAdapterFactory($adapter);

        return $this;
    }

    /** Uses this factory, for tests observing what the commands ask it for. */
    public function withEngineFactory(EngineAdapterFactory $factory): self
    {
        $this->engines = $factory;

        return $this;
    }

    /**
     * Writes a dashboard snapshot holding these merge requests, for the
     * commands that read cached MRs rather than fetching them.
     *
     * @param list<array<string, mixed>> $mrs raw merge-request API payloads
     */
    public function saveSnapshotWithMrs(string $module, array $mrs): self
    {
        (new DashboardCache($this->cockpit . '/cache/dashboard'))->save($module, new ModuleSnapshot(
            new \DateTimeImmutable(),
            ['id' => 42, 'path_with_namespace' => 'project/' . $module, 'path' => $module],
            $mrs,
            [],
        ));

        return $this;
    }

    /** Injects a GitLab client (build one over MockHttpClient with MockGitlab). */
    public function withGitlab(GitlabClient $client): self
    {
        $this->gitlab = $client;

        return $this;
    }

    public function withDrupalOrg(DrupalOrgClient $client): self
    {
        $this->drupalOrg = $client;

        return $this;
    }

    /**
     * Injects the HTTP client the patch commands download diff files with.
     *
     * Separate from the drupal.org client on purpose: that one speaks api-d7
     * and is mocked per endpoint, while this one fetches a file from whatever
     * URL the attachment (or --url) names, including hosts that are not
     * drupal.org at all.
     */
    public function withPatchDownloader(HttpClientInterface $client): self
    {
        $this->patchDownloader = $client;

        return $this;
    }

    /** Uses this shell-out seam for the base-artifact build. */
    public function withCommandRunner(CommandRunner $runner): self
    {
        $this->commandRunner = $runner;

        return $this;
    }

    /**
     * Puts a stub platform browser opener first on $PATH.
     *
     * `issue` and `needs-work` end by handing a URL to the operator's browser
     * and print different things depending on whether that worked, so both
     * outcomes have to be reachable. A stub binary rather than a mocked class
     * keeps Command\BrowserOpener's real Process call inside the path under
     * test while guaranteeing that no browser is ever launched: the opener
     * this run finds is a shell script in the temporary tree.
     *
     * Both platform names are written, so the test means the same thing on
     * Linux (xdg-open) and macOS (open).
     *
     * @param bool $succeeds whether the stub exits 0, i.e. whether the
     *                       operator's browser accepted the URL
     */
    public function withBrowserOpener(bool $succeeds): self
    {
        $bin = $this->makeDirectory('fake-bin');
        $log = $this->path('opened-urls');

        foreach (['xdg-open', 'open'] as $name) {
            $script = sprintf(
                "#!/bin/sh\nprintf '%%s\\n' \"$1\" >> %s\nexit %d\n",
                escapeshellarg($log),
                $succeeds ? 0 : 1,
            );
            if (file_put_contents($bin . '/' . $name, $script) === false) {
                throw new \RuntimeException('Could not write the stub browser opener to ' . $bin);
            }
            chmod($bin . '/' . $name, 0o700);
        }

        $this->environment->set('PATH', $bin . ':' . $this->originalPath);

        return $this;
    }

    /**
     * Every URL handed to the stub browser opener, in order.
     *
     * @return list<string>
     */
    public function openedUrls(): array
    {
        $log = $this->path('opened-urls');
        if (!is_file($log)) {
            return [];
        }

        return array_values(array_filter(explode("\n", (string) file_get_contents($log))));
    }

    // ------------------------------------------------------ cockpit state

    /**
     * Adds a module to the harness cockpit's registry.
     *
     * @param list<string> $coreVersions in registry order — the first is the
     *                                   documented default target core
     */
    public function registerModule(string $name, ?string $project = null, array $coreVersions = ['11']): self
    {
        $this->modules[$name] = [
            'project' => $project ?? 'project/' . $name,
            'core_versions' => $coreVersions,
        ];

        $quote = static fn (string $core): string => "'" . $core . "'";

        $yaml = "modules:\n";
        foreach ($this->modules as $module => $entry) {
            $yaml .= sprintf(
                "  %s:\n    project: %s\n    core_versions: [%s]\n",
                $module,
                $entry['project'],
                implode(', ', array_map($quote, $entry['core_versions'])),
            );
        }

        return $this->writeRegistry($yaml);
    }

    /** Writes the harness cockpit's registry verbatim (malformed YAML included). */
    public function writeRegistry(string $yaml): self
    {
        file_put_contents($this->cockpit . '/registry.yml', $yaml);

        return $this;
    }

    /** An absolute path inside the temporary tree, whether or not it exists. */
    public function path(string $relative): string
    {
        return $this->home . '/' . ltrim($relative, '/');
    }

    /** Creates a directory inside the temporary tree and returns its path. */
    public function makeDirectory(string $relative): string
    {
        $path = $this->path($relative);
        if (!is_dir($path) && !mkdir($path, 0o700, true)) {
            throw new \RuntimeException('Could not create ' . $path);
        }

        return $path;
    }

    // ------------------------------------------------------------- runtime

    /** Sets an environment variable for this run, restored by destroy(). */
    public function setEnv(string $name, ?string $value): self
    {
        $this->environment->set($name, $value);

        return $this;
    }

    /** Runs subsequent commands from this working directory. */
    public function inDirectory(string $directory): self
    {
        if (!chdir($directory)) {
            throw new \RuntimeException('Could not change directory to ' . $directory);
        }

        return $this;
    }

    /**
     * Scripted answers for interactive prompts, consumed by the next run().
     *
     * @param list<string> $answers
     */
    public function setInputs(array $answers): self
    {
        $this->inputs = $answers;

        return $this;
    }

    /**
     * Runs one invocation, exactly as `bin/upkeep` would run it.
     *
     * ApplicationTester is deliberately not used: it builds an ArrayInput,
     * which reports `--version` to Application::doRun()'s hijack probe and so
     * would print the application version instead of running the command.
     * The VersionOptionInput seam only exists at the argv layer, so the argv
     * layer is what the harness drives.
     *
     * @param string ...$argv the command name and its arguments, as typed
     *
     * @return int the process exit code
     */
    public function run(string ...$argv): int
    {
        return $this->dispatch(new BufferedOutput(), ...$argv);
    }

    /**
     * Runs one invocation against an output that really has two streams.
     *
     * The commands whose stdout carries a machine-readable payload route
     * every diagnostic to stderr, and that split only exists when the output
     * is a ConsoleOutputInterface — under the single-buffer run() above the
     * two are indistinguishable. Tests asserting what a shell pipeline sees
     * use this and read display() and errorDisplay() separately.
     */
    public function runSplittingStreams(string ...$argv): int
    {
        $output = new SplitConsoleOutput();
        $exitCode = $this->dispatch($output, ...$argv);
        $this->errorDisplay = $output->fetchErrors();

        return $exitCode;
    }

    /** Everything the last run wrote to stdout (or to both, under run()). */
    public function display(): string
    {
        return $this->display;
    }

    /** Everything the last runSplittingStreams() wrote to stderr. */
    public function errorDisplay(): string
    {
        return $this->errorDisplay;
    }

    /**
     * The console application, wired the way `bin/upkeep` wires it: the same
     * commands in the same order, and the application-level --version option
     * dropped so the commands' target-core selector owns the name.
     */
    public function application(): Application
    {
        $engines = $this->engines;
        $probe = new VolumeProbe(static fn (array $command): ?string => null);

        $application = new Application('Upkeep', 'dev');
        $application->setAutoExit(false);
        $application->addCommands([
            new ApiProbeCommand($this->gitlab),
            new BaseArtifactsBuildCommand($this->commandRunner),
            new BaseArtifactsStatusCommand(),
            new CheckCommand($engines, $this->gitlab),
            new DashboardCommand($this->gitlab, $this->drupalOrg),
            new DevCommand($engines),
            new EnvPathCommand($engines),
            new ExecCommand($engines),
            new ExplainCommand(),
            new InitCommand(),
            new IssueCommand($this->gitlab, $this->drupalOrg),
            new IssuesCommand($this->drupalOrg),
            new MergeCommand($this->gitlab),
            new NeedsWorkCommand($this->gitlab),
            new ModulesAddCommand($this->gitlab),
            new ModulesCommand(),
            new NotesCommand($this->gitlab),
            new PatchApplyCommand($engines, $this->drupalOrg, $this->patchDownloader),
            new PatchCheckCommand($engines, $this->drupalOrg, $this->patchDownloader),
            new PatchPromoteCommand($engines, $this->drupalOrg, $this->patchDownloader),
            new PatchesCommand($this->drupalOrg, $this->gitlab),
            new PruneCommand($engines, $probe),
            new PublishCommand($engines, $this->gitlab, $this->drupalOrg),
            new ReviewCommand($engines, $this->gitlab),
            new StartCommand($engines, $this->drupalOrg),
            new StatusCommand($probe),
            new UiCommand(static fn (): int => 0),
        ]);

        $definition = $application->getDefinition();
        $definition->setOptions(array_filter(
            $definition->getOptions(),
            static fn ($option): bool => $option->getName() !== 'version',
        ));

        return $application;
    }

    /**
     * The shared argv → application → exit-code path both run methods take.
     *
     * @param BufferedOutput $output where the run's stdout is collected
     */
    private function dispatch(BufferedOutput $output, string ...$argv): int
    {
        // Built by appending rather than spreading: argv is a positional list
        // and ArgvInput is typed as one.
        $tokens = ['upkeep'];
        foreach ($argv as $token) {
            $tokens[] = $token;
        }

        $input = new VersionOptionInput($tokens);
        if ($this->inputs !== []) {
            $input->setStream(self::stream($this->inputs));
            $this->inputs = [];
        }

        $this->errorDisplay = '';
        $exitCode = $this->application()->run($input, $output);
        $this->display = $output->fetch();

        return $exitCode;
    }

    /** Restores the environment and the working directory, and removes the tree. */
    public function destroy(): void
    {
        chdir($this->originalCwd);
        $this->environment->restore();
        exec('rm -rf ' . escapeshellarg($this->home));
    }

    /**
     * Points every source of user state at the temporary tree. $HOME covers
     * ~/.upkeep/projects, $XDG_CONFIG_HOME covers the token file, and the
     * UPKEEP_* variables are cleared so a developer's exported cockpit or
     * token cannot reach a test.
     */
    private function isolateEnvironment(): void
    {
        $this->environment->set('HOME', $this->home);
        $this->environment->set('XDG_CONFIG_HOME', $this->home . '/.config');
        $this->environment->set('UPKEEP_COCKPIT', null);
        $this->environment->set('UPKEEP_PROJECTS_ROOT', null);
        $this->environment->set('UPKEEP_GITLAB_TOKEN', null);
    }

    /**
     * @param list<string> $answers
     *
     * @return resource
     */
    private static function stream(array $answers)
    {
        $stream = fopen('php://memory', 'r+');
        if ($stream === false) {
            throw new \RuntimeException('Could not open an input stream.');
        }

        fwrite($stream, implode(\PHP_EOL, $answers) . \PHP_EOL);
        rewind($stream);

        return $stream;
    }
}
