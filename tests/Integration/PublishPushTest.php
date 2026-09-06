<?php

declare(strict_types=1);

namespace Upkeep\Tests\Integration;

use PHPUnit\Framework\TestCase;
use Upkeep\Adapter\AdapterException;
use Upkeep\Adapter\DdevContribAdapter;
use Upkeep\Adapter\Environment;
use Upkeep\Adapter\GitRemote;
use Upkeep\Adapter\IssueBranch;
use Upkeep\Adapter\ProcessRunner;
use Upkeep\Security\SecretRedactor;

/**
 * `publish`'s git half, against a real remote.
 *
 * Pushing is the only outward-facing thing upkeep does, and it had never run
 * in a test — the unit suite scripts the runner, so the push that matters is
 * a string comparison. Here it is a real `git push` to a real repository.
 *
 * No credential, no network, and **no merge requests are opened anywhere**.
 * `publish` splits cleanly: the git half is plain git against whatever URL it
 * is handed, and the GitLab half is one `createMergeRequest` call the unit
 * suite already covers. A `file://` bare repository exercises the first half
 * completely, including the part that cannot be provoked on demand against a
 * real fork.
 *
 * That last part is the point. A fresh drupal.org issue fork rejects pushes
 * until a maintainer clicks a button on the issue page, and the resulting
 * `pre-receive hook declined` was reported here as an unexplained wall of git
 * output. A bare repository with a `pre-receive` hook reproduces it exactly,
 * on demand, offline — which is the only way anyone is going to keep
 * `PushRefusal`'s two diagnoses honest.
 */
final class PublishPushTest extends TestCase
{
    private const NID = 3559057;

    private string $dir;

    protected function setUp(): void
    {
        if (trim((string) shell_exec('command -v git 2>/dev/null')) === '') {
            self::markTestSkipped('git is not on PATH.');
        }

        $this->dir = sys_get_temp_dir() . '/upkeep-push-' . bin2hex(random_bytes(4));
        mkdir($this->dir, 0o755, true);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    private function runner(): ProcessRunner
    {
        return new ProcessRunner(static function (): void {
        }, new SecretRedactor());
    }

    private function adapter(): DdevContribAdapter
    {
        return new DdevContribAdapter(
            new \Upkeep\BaseArtifact\ArtifactLayout($this->dir . '/artifacts'),
            $this->dir . '/projects',
            $this->runner(),
            static function (): void {
            },
        );
    }

    /** @param list<string> $command */
    private function git(array $command, string $cwd): void
    {
        $captured = $this->runner()->capture($command, $cwd, 60);
        self::assertSame(0, $captured->exitCode, implode(' ', $command) . ': ' . $captured->output);
    }

    /**
     * A module working copy sitting on a work branch with a commit to push,
     * which is the state `publish` is reached in.
     */
    private function workingCopy(): Environment
    {
        $project = $this->dir . '/project';
        $module = $project . '/module';
        mkdir($module, 0o755, true);

        $this->git(['git', 'init', '--initial-branch=2.0.x', '.'], $module);
        $this->git(['git', 'config', 'user.email', 'test@localhost'], $module);
        $this->git(['git', 'config', 'user.name', 'upkeep test'], $module);
        file_put_contents($module . '/widget.info.yml', "name: Widget\ntype: module\n");
        $this->git(['git', 'add', '-A'], $module);
        $this->git(['git', 'commit', '-m', 'Initial'], $module);

        $branch = IssueBranch::forIssue(self::NID, 'Alter the subforms');
        $this->git(['git', 'checkout', '-b', $branch->name], $module);
        file_put_contents($module . '/widget.module', "<?php\n");
        $this->git(['git', 'add', '-A'], $module);
        $this->git(['git', 'commit', '-m', 'The work'], $module);

        return new Environment('widget', '11', 'upkeep-widget-d11', $project, 'https://localhost', true);
    }

    /**
     * A bare repository standing in for the issue fork.
     *
     * @param ?string $preReceive a hook body, to reproduce a refusal
     */
    private function fork(string $name, ?string $preReceive = null): string
    {
        $path = $this->dir . '/' . $name . '.git';
        mkdir($path, 0o755, true);
        $this->git(['git', 'init', '--bare', '.'], $path);

        if ($preReceive !== null) {
            $hook = $path . '/hooks/pre-receive';
            file_put_contents($hook, "#!/bin/sh\n" . $preReceive . "\n");
            chmod($hook, 0o755);
        }

        return 'file://' . $path;
    }

    /** The happy path: the branch arrives, and the SHA that comes back is its head. */
    public function testTheWorkBranchIsPushedAndItsHeadReturned(): void
    {
        $environment = $this->workingCopy();
        $branch = IssueBranch::forIssue(self::NID, 'Alter the subforms');
        $remote = GitRemote::issueFork(self::NID, $this->fork('origin'));

        $sha = $this->adapter()->pushWork($environment, $branch, $remote);

        self::assertMatchesRegularExpression('/^[0-9a-f]{40}$/', $sha);

        $onRemote = $this->runner()->capture(
            ['git', 'ls-remote', $remote->url, 'refs/heads/' . $branch->name],
            $this->dir,
            60,
        );
        self::assertStringContainsString($sha, $onRemote->output, 'the branch is on the remote at that SHA');
    }

    /**
     * The refusal that arrived as an unexplained wall of git output.
     *
     * Creating a drupal.org issue fork does not grant push access to it — that
     * is a separate button on the issue page — so this is the *ordinary* state
     * of a fresh fork, not an exotic failure. It has to be told apart from "we
     * do not know who you are", because the recoveries are opposite: click a
     * button, versus add an SSH key.
     */
    public function testAPreReceiveRefusalIsDiagnosedAsMissingPushAccess(): void
    {
        $environment = $this->workingCopy();
        $branch = IssueBranch::forIssue(self::NID, 'Alter the subforms');
        $remote = GitRemote::issueFork(self::NID, $this->fork(
            'locked',
            'echo "GitLab: You are not allowed to push code to this project." >&2; exit 1',
        ));

        try {
            $this->adapter()->pushWork($environment, $branch, $remote);
            self::fail('Expected the push to be refused.');
        } catch (AdapterException $e) {
            self::assertStringContainsString('not allowed to push', $e->getMessage(), 'git\'s own words survive');
            self::assertMatchesRegularExpression(
                '/push access|grant|issue page/i',
                $e->getMessage(),
                'and the recovery is the button on the issue, not an SSH key',
            );
        }
    }

    /**
     * Authentication is the other diagnosis, and must not be confused with the
     * one above — its output can contain "denied" too, which is why the
     * authorization pattern is matched first.
     */
    public function testAnAuthenticationRefusalIsDiagnosedAsACredentialProblem(): void
    {
        $environment = $this->workingCopy();
        $branch = IssueBranch::forIssue(self::NID, 'Alter the subforms');
        $remote = GitRemote::issueFork(self::NID, $this->fork(
            'anon',
            'echo "git@git.drupal.org: Permission denied (publickey)." >&2; exit 1',
        ));

        try {
            $this->adapter()->pushWork($environment, $branch, $remote);
            self::fail('Expected the push to be refused.');
        } catch (AdapterException $e) {
            self::assertMatchesRegularExpression('/ssh|key/i', $e->getMessage());
        }
    }

    /**
     * Nothing is pushed from a branch the maintainer is not on. Publishing a
     * branch you are not looking at is how the wrong work reaches a fork.
     */
    public function testPublishingRefusesWhenTheWorkingCopyIsElsewhere(): void
    {
        $environment = $this->workingCopy();
        $this->git(['git', 'checkout', '2.0.x'], $environment->projectPath . '/module');

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('not "' . IssueBranch::forIssue(self::NID, 'Alter the subforms')->name . '"');

        $this->adapter()->pushWork(
            $environment,
            IssueBranch::forIssue(self::NID, 'Alter the subforms'),
            GitRemote::issueFork(self::NID, $this->fork('unused')),
        );
    }

    /** And nothing is pushed from a dirty tree: what is not committed cannot be. */
    public function testPublishingRefusesADirtyWorkingCopy(): void
    {
        $environment = $this->workingCopy();
        file_put_contents($environment->projectPath . '/module/widget.module', "<?php // uncommitted\n");

        $this->expectException(AdapterException::class);
        $this->expectExceptionMessage('uncommitted changes');

        $this->adapter()->pushWork(
            $environment,
            IssueBranch::forIssue(self::NID, 'Alter the subforms'),
            GitRemote::issueFork(self::NID, $this->fork('unused')),
        );
    }
}
