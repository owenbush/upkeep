package adapter

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/proc"
)

// A real git repository, because the whole value of this is reading git's
// actual porcelain rather than a string somebody wrote down.
func aRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("no git: %v", err)
	}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=2.0.x"},
		{"config", "user.email", "test@example.test"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(t, dir, "pathauto.info.yml", "name: Pathauto\n")
	commit(t, dir, "initial")

	return dir
}

func write(t *testing.T, dir, name, contents string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, dir, message string) {
	t.Helper()

	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", message)
}

func inspect(t *testing.T, dir string) WorkingCopyStatus {
	t.Helper()

	return InspectWorkingCopy(dir, proc.New(nil, nil, nil))
}

// A clean checkout on a base branch with nothing to push is the one state
// nothing guards against.
func TestACleanBaseBranchCheckoutHasNoLocalWork(t *testing.T) {
	dir := aRepo(t)

	// With an upstream, so "no upstream" is not what makes it interesting.
	upstream := t.TempDir()
	git(t, upstream, "init", "--bare")
	git(t, dir, "remote", "add", "origin", upstream)
	git(t, dir, "push", "-u", "origin", "2.0.x")

	status := inspect(t, dir)

	if status.IsDirty() {
		t.Errorf("a clean checkout read as dirty: %v", status.Describe())
	}
	if status.CommitsAhead != 0 {
		t.Errorf("commits ahead %d", status.CommitsAhead)
	}
	if status.CurrentBranch != "2.0.x" {
		t.Errorf("branch %q", status.CurrentBranch)
	}
	if status.IsOnCustomBranch() {
		t.Error("a release branch read as a developer branch")
	}
	if status.HasLocalWork() {
		t.Errorf("a clean checkout read as carrying work: %v", status.Describe())
	}
}

// The three hard signals, each on its own, because a checkout or a teardown
// would destroy any of them.
func TestEachDirtySignalIsReadSeparately(t *testing.T) {
	t.Run("staged", func(t *testing.T) {
		dir := aRepo(t)
		write(t, dir, "new.php", "<?php\n")
		git(t, dir, "add", "new.php")

		status := inspect(t, dir)
		if !status.HasStagedChanges {
			t.Errorf("staged changes not seen: %v", status.Describe())
		}
		if status.HasUntrackedFiles {
			t.Error("a staged new file read as untracked")
		}
		if !status.IsDirty() {
			t.Error("not dirty")
		}
	})

	t.Run("unstaged", func(t *testing.T) {
		dir := aRepo(t)
		write(t, dir, "pathauto.info.yml", "name: Changed\n")

		status := inspect(t, dir)
		if !status.HasUnstagedChanges {
			t.Errorf("unstaged changes not seen: %v", status.Describe())
		}
		if status.HasStagedChanges {
			t.Error("an unstaged change read as staged")
		}
	})

	t.Run("untracked", func(t *testing.T) {
		dir := aRepo(t)
		write(t, dir, "scratch.php", "<?php\n")

		status := inspect(t, dir)
		if !status.HasUntrackedFiles {
			t.Errorf("untracked files not seen: %v", status.Describe())
		}
		if status.HasStagedChanges || status.HasUnstagedChanges {
			t.Error("an untracked file read as a change to a tracked one")
		}
		// A checkout or a teardown would destroy it just the same.
		if !status.IsDirty() {
			t.Error("an untracked file did not make the copy dirty")
		}
	})

	t.Run("staged and unstaged at once", func(t *testing.T) {
		dir := aRepo(t)
		write(t, dir, "pathauto.info.yml", "name: Staged\n")
		git(t, dir, "add", "pathauto.info.yml")
		write(t, dir, "pathauto.info.yml", "name: And then changed again\n")

		status := inspect(t, dir)
		if !status.HasStagedChanges || !status.HasUnstagedChanges {
			t.Errorf("both halves not seen: %v", status.Describe())
		}
	})
}

// Unpushed commits are local work even when the tree is clean.
func TestUnpushedCommitsCountAsLocalWork(t *testing.T) {
	dir := aRepo(t)
	upstream := t.TempDir()
	git(t, upstream, "init", "--bare")
	git(t, dir, "remote", "add", "origin", upstream)
	git(t, dir, "push", "-u", "origin", "2.0.x")

	write(t, dir, "new.php", "<?php\n")
	commit(t, dir, "local work")

	status := inspect(t, dir)
	if status.IsDirty() {
		t.Error("a committed change read as dirty")
	}
	if status.CommitsAhead != 1 {
		t.Errorf("commits ahead %d, want 1", status.CommitsAhead)
	}
	if !status.HasLocalWork() {
		t.Error("an unpushed commit did not read as local work")
	}
	if !slices.Contains(status.Describe(), "1 commit(s) ahead of origin (unpushed)") {
		t.Errorf("reasons %v", status.Describe())
	}
}

// "Nothing to push" and "nothing knows where this would push to" are
// different, and the second is a reason to treat the copy as carrying work.
func TestNoUpstreamIsNotTheSameAsNothingToPush(t *testing.T) {
	dir := aRepo(t)

	status := inspect(t, dir)
	if status.CommitsAhead != NoUpstream {
		t.Errorf("commits ahead %d, want the no-upstream marker", status.CommitsAhead)
	}
	if !status.HasLocalWork() {
		t.Error("a copy with no upstream did not read as carrying work")
	}
	if !slices.Contains(status.Describe(), "No upstream tracking branch configured") {
		t.Errorf("reasons %v", status.Describe())
	}
}

// A detached HEAD is a state nothing can reason about, so it counts as work.
func TestADetachedHeadCountsAsLocalWork(t *testing.T) {
	dir := aRepo(t)
	git(t, dir, "checkout", "--detach")

	status := inspect(t, dir)
	if status.CurrentBranch != "" {
		t.Errorf("branch %q, want nothing", status.CurrentBranch)
	}
	if !status.IsOnCustomBranch() {
		t.Error("a detached HEAD did not read as a custom branch")
	}
	if !slices.Contains(status.Describe(), "HEAD is detached") {
		t.Errorf("reasons %v", status.Describe())
	}
}

// A base branch and an upkeep-managed one are both expected; anything else is
// somebody's own.
//
// Note "8.x-1.x": the legacy Drupal contrib convention is NOT recognised as a
// base branch, so a working copy on one always reads as carrying local work
// and every guard that keys on that refuses. Faithful to the PHP, and
// verified against it — pathauto's own branch is this shape, so it is not
// hypothetical. Recorded here rather than quietly fixed, because changing it
// loosens a guard on destructive operations and that is a decision to take
// deliberately.
func TestWhichBranchesCountAsSomebodysOwn(t *testing.T) {
	for branch, own := range map[string]bool{
		"2.0.x": false,
		"1.0.x": false,
		// Legacy contrib convention, and deliberately not matched. See above.
		"8.x-1.x":                         true,
		"7.x-2.x":                         true,
		"2.x":                             false,
		"11.x":                            false,
		"mr-12":                           false,
		"mr-3597808":                      false,
		"patch-3603341":                   true,
		"main":                            true,
		"3603341-drupal-12-compatibility": true,
		"":                                true,
	} {
		status := WorkingCopyStatus{CurrentBranch: branch}
		if got := status.IsOnCustomBranch(); got != own {
			t.Errorf("%q: custom=%v, want %v", branch, got, own)
		}
	}
}

// A branch nobody else would have made is local work on its own, even with a
// clean tree and nothing to push.
func TestACleanCopyOnSomebodysOwnBranchIsStillLocalWork(t *testing.T) {
	status := WorkingCopyStatus{CurrentBranch: "3603341-drupal-12-compatibility", CommitsAhead: 0}

	if status.IsDirty() {
		t.Fatal("the fixture is not clean")
	}
	if !status.HasLocalWork() {
		t.Errorf("a clean copy on a work branch read as safe to destroy: %v", status.Describe())
	}
}

// Not a directory git knows about: every probe fails, and the answer is the
// most cautious reading rather than a crash.
func TestANonRepositoryReadsAsCarryingWork(t *testing.T) {
	status := inspect(t, t.TempDir())

	if status.HasLocalWork() != true {
		t.Errorf("a non-repository read as safe to destroy: %v", status.Describe())
	}
	if status.CommitsAhead != NoUpstream {
		t.Errorf("commits ahead %d", status.CommitsAhead)
	}
}

// A clean copy on a base branch with an upstream is the only thing that reads
// as safe, so the guard's default is to refuse.
func TestTheDescriptionSaysEveryReasonNotJustTheFirst(t *testing.T) {
	status := WorkingCopyStatus{
		HasStagedChanges:   true,
		HasUnstagedChanges: true,
		HasUntrackedFiles:  true,
		CommitsAhead:       3,
		CurrentBranch:      "my-branch",
	}

	reasons := status.Describe()
	if len(reasons) != 5 {
		t.Errorf("got %d reasons: %v", len(reasons), reasons)
	}
	for _, want := range []string{"Staged", "Unstaged", "Untracked", "3 commit(s)", `"my-branch"`} {
		if !slices.ContainsFunc(reasons, func(r string) bool { return strings.Contains(r, want) }) {
			t.Errorf("reasons %v do not mention %q", reasons, want)
		}
	}
}

func TestACleanCopyDescribesNothing(t *testing.T) {
	status := WorkingCopyStatus{CurrentBranch: "2.0.x"}

	if reasons := status.Describe(); len(reasons) != 0 {
		t.Errorf("got %v", reasons)
	}
}

// git having said something this does not understand is the same "cannot tell"
// as having no upstream at all — not zero.
func TestAnUnreadableAheadCountIsNotZero(t *testing.T) {
	runner := &scriptedRunner{answers: map[string]string{
		"status":       "",
		"symbolic-ref": "2.0.x\n",
		"rev-list":     "not a number\n",
	}}

	status := InspectWorkingCopy("/wherever", runner)
	if status.CommitsAhead != NoUpstream {
		t.Errorf("commits ahead %d, want the no-upstream marker", status.CommitsAhead)
	}
}

// scriptedRunner answers by the git subcommand, so the probes can be driven
// without a repository.
type scriptedRunner struct {
	answers map[string]string
}

func (r *scriptedRunner) Run([]string, string, time.Duration) (string, error) { return "", nil }

func (r *scriptedRunner) TryRun(command []string, _ string, _ time.Duration) (string, bool) {
	for _, arg := range command {
		if answer, known := r.answers[arg]; known {
			return answer, true
		}
	}

	return "", false
}

func (r *scriptedRunner) Capture([]string, string, time.Duration) proc.Captured {
	return proc.Captured{}
}

// A partial promotion is deliberately not committed: a commit carrying the
// patch author's attribution should say what the author wrote, and half of it
// plus rejects is not that yet.
func TestAPartialPromotionHasNoCommitToAskFor(t *testing.T) {
	partial := PartialPromotion([]string{"a.php"}, []string{"b.php"})

	if partial.IsComplete() {
		t.Error("a partial promotion read as complete")
	}
	if _, err := partial.RequireSHA(); err == nil {
		t.Error("a partial promotion handed back a commit")
	}
	if !slices.Equal(partial.Applied, []string{"a.php"}) {
		t.Errorf("applied %v", partial.Applied)
	}
	if !slices.Equal(partial.Rejected, []string{"b.php"}) {
		t.Errorf("rejected %v", partial.Rejected)
	}
}

func TestACommittedPromotionHasOne(t *testing.T) {
	committed := CommittedPromotion("abc1234", []string{"a.php"})

	if !committed.IsComplete() {
		t.Error("a committed promotion read as partial")
	}
	sha, err := committed.RequireSHA()
	if err != nil || sha != "abc1234" {
		t.Errorf("got %q, %v", sha, err)
	}
	if len(committed.Rejected) != 0 {
		t.Errorf("a complete promotion carries rejects: %v", committed.Rejected)
	}
}

// Project names are global to the machine, so the old registration still
// claims one after the projects root moves.
func TestARegistrationAtAnotherPathIsAConflict(t *testing.T) {
	registration := ProjectRegistration{
		ProjectName: "upkeep-pathauto-d11", RegisteredRoot: "/old/projects/upkeep-pathauto-d11",
	}

	if !registration.ConflictsWith("/new/projects/upkeep-pathauto-d11") {
		t.Error("a different path was not a conflict")
	}
	if registration.ConflictsWith("/old/projects/upkeep-pathauto-d11") {
		t.Error("the same path was a conflict")
	}
	// Trailing slashes are the same path.
	if registration.ConflictsWith("/old/projects/upkeep-pathauto-d11/") {
		t.Error("a trailing slash made it a different path")
	}
}

// Refusing to provision on the strength of a question that could not be asked
// would turn a diagnostic into an outage.
func TestARegistrationTheEngineDidNotReportIsNotAConflict(t *testing.T) {
	unknown := ProjectRegistration{ProjectName: "upkeep-pathauto-d11"}

	if unknown.ConflictsWith("/anywhere") {
		t.Error("an unreported registration was treated as a conflict")
	}
}

// The refusal names both paths and the one command that resolves it — and says
// that command deregisters rather than deletes, because the old directory may
// hold work.
func TestTheConflictRefusalNamesBothPathsAndTheRecovery(t *testing.T) {
	registration := ProjectRegistration{
		ProjectName: "upkeep-pathauto-d11", RegisteredRoot: "/old/projects/upkeep-pathauto-d11",
	}

	message := registration.ConflictError("/new/projects/upkeep-pathauto-d11").Error()
	for _, want := range []string{
		"/old/projects/upkeep-pathauto-d11",
		"/new/projects/upkeep-pathauto-d11",
		"ddev stop --unlist upkeep-pathauto-d11",
		"does not delete the directory",
		"UPKEEP_PROJECTS_ROOT",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, message)
		}
	}
}

// Matched on the phrase the engine uses rather than on an exit code, because
// every provisioning failure shares that code.
func TestOnlyTheEnginesOwnWordingIsReadAsARootConflict(t *testing.T) {
	if !IsRootConflict("Failed to start: project root is already set to /old/path") {
		t.Error("the engine's own wording was not recognised")
	}
	for _, other := range []string{
		"Failed to start: port 80 already in use",
		"docker daemon not running",
		"",
		// Carries the word and is not the collision.
		"project upkeep-pathauto-d11 is not running",
		"could not find a project at this directory",
	} {
		if IsRootConflict(other) {
			t.Errorf("%q was read as a root conflict", other)
		}
	}
}

// The case the pre-flight could not see: the engine's record survives but the
// directory it names is gone, so the description had nothing to report.
func TestTheLateConflictKeepsTheEnginesOwnWords(t *testing.T) {
	registration := ProjectRegistration{ProjectName: "upkeep-pathauto-d11"}

	message := registration.RootConflictError(
		"/new/projects/upkeep-pathauto-d11",
		errors.New("  project root is already set to /old/path  "),
	).Error()

	if !strings.Contains(message, "project root is already set to /old/path") {
		t.Errorf("the engine's words were dropped:\n%s", message)
	}
	if !strings.Contains(message, "ddev stop --unlist upkeep-pathauto-d11") {
		t.Errorf("the recovery is missing:\n%s", message)
	}
	if !strings.Contains(message, "does not delete any directory") {
		t.Errorf("it does not say the command is safe:\n%s", message)
	}
}
