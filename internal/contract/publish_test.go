package contract_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/proc"
)

// `publish`'s git half, against a real remote.
//
// Pushing is the only outward-facing thing upkeep does, and until now it had
// never run outside a fake: every other test here scripts the runner, so the
// push that matters is a string comparison. Here it is a real `git push` into
// a real repository.
//
// **No credential, no network, and no merge request is opened anywhere.**
// `publish` splits cleanly — the git half is plain git against whatever URL it
// is handed, and the GitLab half is one `CreateMergeRequest` call the unit
// suite already covers against a stub. A `file://` bare repository exercises
// the first half completely, including the part that cannot be provoked on
// demand against a real fork.
//
// That last part is the point. A fresh drupal.org issue fork rejects pushes
// until a maintainer clicks a button on the issue page, and the resulting
// `pre-receive hook declined` reached a user as an unexplained wall of git
// output. A bare repository with a `pre-receive` hook reproduces it exactly,
// on demand, offline — which is the only way anyone will keep the two push
// diagnoses honest.
//
// Needs git and nothing else, so unlike the ddev tests it runs everywhere.

// nid is the issue these tests publish for.
const nid = 3559057

// issueTitle is what the branch name is derived from.
const issueTitle = "Alter the subforms"

// pushFixture is a module working copy on a work branch with a commit to push,
// which is the state `publish` is reached in.
type pushFixture struct {
	t           *testing.T
	dir         string
	environment adapter.Environment
	branch      adapter.IssueBranch
}

func newPushFixture(t *testing.T) *pushFixture {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	dir := t.TempDir()
	fixture := &pushFixture{
		t:      t,
		dir:    dir,
		branch: adapter.IssueBranchFor(nid, issueTitle),
	}

	project := filepath.Join(dir, "project")
	module := filepath.Join(project, "module")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fixture.git(module, "init", "--initial-branch=2.0.x", ".")
	fixture.git(module, "config", "user.email", "test@localhost")
	fixture.git(module, "config", "user.name", "upkeep test")
	fixture.write(filepath.Join(module, "widget.info.yml"), "name: Widget\ntype: module\n")
	fixture.git(module, "add", "-A")
	fixture.git(module, "commit", "-m", "Initial")

	fixture.git(module, "checkout", "-b", fixture.branch.Name)
	fixture.write(filepath.Join(module, "widget.module"), "<?php\n")
	fixture.git(module, "add", "-A")
	fixture.git(module, "commit", "-m", "The work")

	fixture.environment = adapter.Environment{
		ModuleName:  "widget",
		CoreMajor:   "11",
		ProjectName: "upkeep-widget-d11",
		ProjectPath: project,
		PrimaryURL:  "https://localhost",
	}

	return fixture
}

func (f *pushFixture) module() string {
	return filepath.Join(f.environment.ProjectPath, "module")
}

func (f *pushFixture) runner() proc.Runner { return proc.New(nil, nil, nil) }

// adapterUnder is the real engine, pointed at scratch directories. Only its
// git half is exercised: nothing here starts a container.
func (f *pushFixture) adapterUnder() adapter.Engine {
	return adapter.NewDdevContrib(
		baseartifact.NewLayout(filepath.Join(f.dir, "artifacts")),
		filepath.Join(f.dir, "projects"),
		f.runner(),
		nil,
	)
}

func (f *pushFixture) git(dir string, args ...string) {
	f.t.Helper()

	captured := f.runner().Capture(append([]string{"git"}, args...), dir, time.Minute)
	if captured.ExitCode == nil || *captured.ExitCode != 0 {
		f.t.Fatalf("git %s: %s", strings.Join(args, " "), captured.Output)
	}
}

func (f *pushFixture) write(path, contents string) {
	f.t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", path, err)
	}
}

// fork is a bare repository standing in for the issue fork.
//
// preReceive, when given, is a hook body that reproduces a refusal.
func (f *pushFixture) fork(name string, preReceive string) string {
	f.t.Helper()

	path := filepath.Join(f.dir, name+".git")
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatalf("mkdir: %v", err)
	}
	f.git(path, "init", "--bare", ".")

	if preReceive != "" {
		hook := filepath.Join(path, "hooks", "pre-receive")
		f.write(hook, "#!/bin/sh\n"+preReceive+"\n")
		if err := os.Chmod(hook, 0o755); err != nil {
			f.t.Fatalf("chmod: %v", err)
		}
	}

	return "file://" + path
}

// The happy path: the branch arrives on the remote, and the SHA that comes
// back is its head.
func TestTheWorkBranchIsPushedAndItsHeadReturned(t *testing.T) {
	fixture := newPushFixture(t)
	remote := adapter.IssueForkRemote(nid, fixture.fork("origin", ""))

	sha, err := fixture.adapterUnder().PushWork(fixture.environment, fixture.branch, remote)
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		t.Errorf("that is not a commit SHA: %q", sha)
	}

	// Asked of the remote rather than the working copy: what matters is that
	// the branch is *there*, at that commit.
	listed := fixture.runner().Capture(
		[]string{"git", "ls-remote", remote.URL, "refs/heads/" + fixture.branch.Name},
		fixture.dir, time.Minute)
	if !strings.Contains(listed.Output, sha) {
		t.Errorf("the branch is not on the remote at %s:\n%s", sha, listed.Output)
	}
}

// The refusal that arrived as an unexplained wall of git output.
//
// Creating a drupal.org issue fork does not grant push access to it — that is
// a separate button on the issue page — so this is the *ordinary* state of a
// fresh fork, not an exotic failure. The recovery is that button, and saying
// "check your SSH key" instead sends somebody to fix something that is not
// broken.
func TestAPreReceiveRefusalIsDiagnosedAsMissingPushAccess(t *testing.T) {
	fixture := newPushFixture(t)
	remote := adapter.IssueForkRemote(nid, fixture.fork("locked",
		`echo "GitLab: You are not allowed to push code to this project." >&2; exit 1`))

	_, err := fixture.adapterUnder().PushWork(fixture.environment, fixture.branch, remote)
	if err == nil {
		t.Fatal("the push was accepted by a remote that refuses it")
	}

	if !strings.Contains(err.Error(), "not allowed to push") {
		t.Errorf("git's own words did not survive:\n%v", err)
	}
	if !regexp.MustCompile(`(?i)push access|grant|issue page`).MatchString(err.Error()) {
		t.Errorf("the recovery is the button on the issue, not an SSH key:\n%v", err)
	}
}

// The other diagnosis: GitLab does not know you at all.
//
// Matched second, because an authorization refusal can carry the word "denied"
// too and is the more specific reading.
func TestAnAuthenticationRefusalIsDiagnosedAsACredentialProblem(t *testing.T) {
	fixture := newPushFixture(t)
	remote := adapter.IssueForkRemote(nid, fixture.fork("anon",
		`echo "git@git.drupal.org: Permission denied (publickey)." >&2; exit 1`))

	_, err := fixture.adapterUnder().PushWork(fixture.environment, fixture.branch, remote)
	if err == nil {
		t.Fatal("the push was accepted by a remote that refuses it")
	}

	if !regexp.MustCompile(`(?i)ssh|key`).MatchString(err.Error()) {
		t.Errorf("an authentication refusal was not diagnosed as one:\n%v", err)
	}
}

// Nothing is pushed from a branch the maintainer is not on.
//
// Publishing a branch you are not looking at is how the wrong work reaches a
// fork — and once it is there, it is there.
func TestPublishingRefusesWhenTheWorkingCopyIsElsewhere(t *testing.T) {
	fixture := newPushFixture(t)
	fixture.git(fixture.module(), "checkout", "2.0.x")
	remote := adapter.IssueForkRemote(nid, fixture.fork("unused", ""))

	_, err := fixture.adapterUnder().PushWork(fixture.environment, fixture.branch, remote)
	if err == nil {
		t.Fatal("it published from the wrong branch")
	}
	if !strings.Contains(err.Error(), fixture.branch.Name) || !strings.Contains(err.Error(), "2.0.x") {
		t.Errorf("the refusal names neither where it is nor where it should be:\n%v", err)
	}

	// And the remote is untouched.
	listed := fixture.runner().Capture(
		[]string{"git", "ls-remote", remote.URL}, fixture.dir, time.Minute)
	if strings.TrimSpace(listed.Output) != "" {
		t.Errorf("something reached the remote anyway:\n%s", listed.Output)
	}
}

// And nothing is pushed from a dirty tree: what is not committed cannot be.
//
// The failure mode this prevents is quiet — the push succeeds, carries the
// last commit, and the maintainer's actual edit stays on their disk while the
// merge request says it is ready.
func TestPublishingRefusesADirtyWorkingCopy(t *testing.T) {
	fixture := newPushFixture(t)
	fixture.write(filepath.Join(fixture.module(), "widget.module"), "<?php // uncommitted\n")
	remote := adapter.IssueForkRemote(nid, fixture.fork("unused", ""))

	_, err := fixture.adapterUnder().PushWork(fixture.environment, fixture.branch, remote)
	if err == nil {
		t.Fatal("it published a dirty working copy")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("the refusal does not say what is wrong:\n%v", err)
	}

	listed := fixture.runner().Capture(
		[]string{"git", "ls-remote", remote.URL}, fixture.dir, time.Minute)
	if strings.TrimSpace(listed.Output) != "" {
		t.Errorf("something reached the remote anyway:\n%s", listed.Output)
	}
}

// Publishing twice is how somebody adds a commit to an open merge request, so
// the second push has to work — and must not need a force.
func TestPublishingAgainAfterAnotherCommitUpdatesTheBranch(t *testing.T) {
	fixture := newPushFixture(t)
	remote := adapter.IssueForkRemote(nid, fixture.fork("origin", ""))

	first, err := fixture.adapterUnder().PushWork(fixture.environment, fixture.branch, remote)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}

	fixture.write(filepath.Join(fixture.module(), "widget.module"), "<?php // more\n")
	fixture.git(fixture.module(), "add", "-A")
	fixture.git(fixture.module(), "commit", "-m", "More work")

	second, err := fixture.adapterUnder().PushWork(fixture.environment, fixture.branch, remote)
	if err != nil {
		t.Fatalf("second push: %v", err)
	}

	if second == first {
		t.Error("the second push carried the same commit")
	}
	listed := fixture.runner().Capture(
		[]string{"git", "ls-remote", remote.URL, "refs/heads/" + fixture.branch.Name},
		fixture.dir, time.Minute)
	if !strings.Contains(listed.Output, second) {
		t.Errorf("the remote did not move to %s:\n%s", second, listed.Output)
	}
}

// A push that would overwrite work on the remote is refused, not forced.
//
// `pushWork` uses no `--force` and no lease, deliberately: a rejected push
// means the remote moved, which is a thing to look at rather than to
// overwrite. On an issue fork the thing that moved is somebody else's commit.
//
// Written because a mutation showed nothing caught it — adding `--force` to
// the push left every other test in this file passing, which is exactly the
// shape of a guard that is only a comment.
func TestAPushThatWouldOverwriteTheRemoteIsRefused(t *testing.T) {
	fixture := newPushFixture(t)
	remote := adapter.IssueForkRemote(nid, fixture.fork("origin", ""))

	if _, err := fixture.adapterUnder().PushWork(
		fixture.environment, fixture.branch, remote); err != nil {
		t.Fatalf("first push: %v", err)
	}

	// Somebody else pushes to the fork: a second clone commits and pushes, so
	// the remote branch is now ahead of ours by a commit we do not have.
	other := filepath.Join(fixture.dir, "other")
	fixture.git(fixture.dir, "clone", remote.URL, other)
	fixture.git(other, "config", "user.email", "other@localhost")
	fixture.git(other, "config", "user.name", "someone else")
	fixture.git(other, "checkout", fixture.branch.Name)
	fixture.write(filepath.Join(other, "theirs.txt"), "their work\n")
	fixture.git(other, "add", "-A")
	fixture.git(other, "commit", "-m", "Their work")
	fixture.git(other, "push", "origin", fixture.branch.Name)

	theirs := strings.Fields(fixture.runner().Capture(
		[]string{"git", "ls-remote", remote.URL, "refs/heads/" + fixture.branch.Name},
		fixture.dir, time.Minute).Output)

	// Now we commit on top of our older tip and try to publish again.
	fixture.write(filepath.Join(fixture.module(), "widget.module"), "<?php // ours\n")
	fixture.git(fixture.module(), "add", "-A")
	fixture.git(fixture.module(), "commit", "-m", "Our work")

	_, err := fixture.adapterUnder().PushWork(fixture.environment, fixture.branch, remote)
	if err == nil {
		t.Fatal("the push overwrote a remote that had moved")
	}

	// And their commit is still the tip: refusing is only worth anything if
	// nothing was destroyed on the way.
	after := strings.Fields(fixture.runner().Capture(
		[]string{"git", "ls-remote", remote.URL, "refs/heads/" + fixture.branch.Name},
		fixture.dir, time.Minute).Output)
	if len(theirs) == 0 || len(after) == 0 || theirs[0] != after[0] {
		t.Errorf("the remote tip changed: was %v, now %v", theirs, after)
	}
}
