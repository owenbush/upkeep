package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/proc"
)

// recordingRunner records every command and answers from a script keyed on a
// substring of the command line.
//
// Keyed on a substring rather than the whole line because the point of these
// tests is the *sequence* and the *shape* of what runs — which commands, in
// which order, with which flags — and a script that had to spell every
// invocation exactly would be a second copy of the implementation.
type recordingRunner struct {
	ran      []string
	timeouts []time.Duration
	// via is how each command was invoked: Run means it must succeed, TryRun
	// and Capture mean its failure is an answer rather than a problem.
	via     []string
	keys    []string
	answers map[string]string
	fail    map[string]bool
	stall   map[string]bool
	// effects are side effects a command has on the filesystem, so a test of
	// a sequence can be a test of what each step leaves for the next.
	effects []struct {
		key string
		run func()
	}
}

func newRunner() *recordingRunner {
	return &recordingRunner{
		answers: map[string]string{}, fail: map[string]bool{}, stall: map[string]bool{},
	}
}

func (r *recordingRunner) answer(key, value string) *recordingRunner {
	if _, seen := r.answers[key]; !seen {
		r.keys = append(r.keys, key)
	}
	r.answers[key] = value

	return r
}

func (r *recordingRunner) fails(key string) *recordingRunner {
	r.fail[key] = true

	return r
}

// stalls scripts a command that runs past its timebox.
func (r *recordingRunner) stalls(key string) *recordingRunner {
	r.stall[key] = true

	return r
}

func (r *recordingRunner) stalling(line string) bool {
	for key := range r.stall {
		if strings.Contains(line, key) {
			return true
		}
	}

	return false
}

func (r *recordingRunner) match(line string) (string, bool) {
	for _, key := range r.keys {
		if strings.Contains(line, key) {
			return r.answers[key], true
		}
	}

	return "", false
}

func (r *recordingRunner) failing(line string) bool {
	for key := range r.fail {
		if strings.Contains(line, key) {
			return true
		}
	}

	return false
}

// does scripts what a command leaves behind on disk.
func (r *recordingRunner) does(key string, effect func()) *recordingRunner {
	r.effects = append(r.effects, struct {
		key string
		run func()
	}{key, effect})

	return r
}

func (r *recordingRunner) record(command []string, timeout time.Duration, via string) string {
	line := strings.Join(command, " ")
	r.ran = append(r.ran, line)
	r.timeouts = append(r.timeouts, timeout)
	r.via = append(r.via, via)

	if !r.failing(line) {
		for _, effect := range r.effects {
			if strings.Contains(line, effect.key) {
				effect.run()
			}
		}
	}

	return line
}

// mustSucceed is every command the run made through Run — the ones whose
// failure is fatal, as opposed to the probes whose failure is an answer.
func (r *recordingRunner) mustSucceed() []string {
	var required []string
	for i, line := range r.ran {
		if r.via[i] == "Run" {
			required = append(required, line)
		}
	}

	return required
}

// timeoutOf is the timebox the given command was run under.
func (r *recordingRunner) timeoutOf(fragments ...string) time.Duration {
	at := r.indexOfCommand(fragments...)
	if at < 0 {
		return -1
	}

	return r.timeouts[at]
}

func (r *recordingRunner) Run(command []string, _ string, timeout time.Duration) (string, error) {
	line := r.record(command, timeout, "Run")
	answer, _ := r.match(line)
	if r.failing(line) {
		// With what it said, as the real runner does: a failure's output is
		// what several diagnoses are read out of, and a fake that dropped it
		// would make those unreachable here and only here.
		return "", fmt.Errorf("command failed: %s\n%s", line, answer)
	}

	return answer, nil
}

func (r *recordingRunner) TryRun(command []string, _ string, timeout time.Duration) (string, bool) {
	line := r.record(command, timeout, "TryRun")
	if r.failing(line) {
		return "", false
	}

	return r.match(line)
}

func (r *recordingRunner) Capture(command []string, _ string, timeout time.Duration) proc.Captured {
	line := r.record(command, timeout, "Capture")
	if r.stalling(line) {
		answer, _ := r.match(line)

		return proc.Captured{TimedOut: true, Output: answer, Duration: timeout}
	}
	status := 0
	if r.failing(line) {
		status = 1
	}
	answer, _ := r.match(line)

	return proc.Captured{ExitCode: &status, Output: answer}
}

func (r *recordingRunner) indexOfCommand(fragments ...string) int {
	for i, line := range r.ran {
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(line, fragment) {
				matched = false

				break
			}
		}
		if matched {
			return i
		}
	}

	return -1
}

func (r *recordingRunner) didRun(fragments ...string) bool {
	return r.indexOfCommand(fragments...) >= 0
}

func (r *recordingRunner) transcript() string { return "  " + strings.Join(r.ran, "\n  ") }

// cleanOn scripts a clean working copy on the given branch, with an upstream
// and nothing to push.
func cleanOn(branch string) *recordingRunner {
	return newRunner().
		answer("status --porcelain", "").
		answer("symbolic-ref --short HEAD", branch+"\n").
		answer("rev-list --count @{upstream}..HEAD", "0\n")
}

// anEnvironment is a project laid out on disk the way a provisioned one is,
// with web/modules/contrib/<module> a symlink into the working copy.
//
// Real rather than faked, because the ownership constraint — composer must
// symlink to the checkout and never mirror it — is checked against the actual
// filesystem after every branch switch, and a test that could not satisfy it
// would be testing a path production never takes.
func anEnvironment(t *testing.T) Environment {
	t.Helper()

	root := t.TempDir()
	projectPath := filepath.Join(root, "upkeep-pathauto-d11")
	contrib := filepath.Join(projectPath, "web", "modules", "contrib")
	if err := os.MkdirAll(contrib, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectPath, moduleDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(projectPath, moduleDir), filepath.Join(contrib, "pathauto")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	return Environment{
		ModuleName:  "pathauto",
		CoreMajor:   "11",
		ProjectName: "upkeep-pathauto-d11",
		ProjectPath: projectPath,
	}
}

// aMirroredEnvironment is the same layout with a real directory where the
// symlink should be — what composer leaves behind when it mirrors instead.
func aMirroredEnvironment(t *testing.T) Environment {
	t.Helper()

	environment := Environment{
		ModuleName:  "pathauto",
		CoreMajor:   "11",
		ProjectName: "upkeep-pathauto-d11",
		ProjectPath: filepath.Join(t.TempDir(), "upkeep-pathauto-d11"),
	}
	mirrored := filepath.Join(environment.ProjectPath, "web", "modules", "contrib", "pathauto")
	if err := os.MkdirAll(mirrored, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	return environment
}

func engineWith(runner proc.Runner) *DdevContrib {
	return NewDdevContrib(nil, "/projects", runner, nil)
}

func anMr(iid int, target string) gitlab.MergeRequest {
	return gitlab.MergeRequest{
		IID: iid, State: "opened", SourceBranch: "project-update-bot-only", TargetBranch: target,
		HeadSHA: "head111",
	}
}

// git refuses to fetch into the currently checked-out branch, which mr-<iid>
// is on a re-apply — so the working copy steps back onto the base first.
func TestApplyingAMergeRequestStepsBackOntoTheBaseBeforeFetching(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("ls-remote origin", "abc\t"+MergeRef(12)+"\ndef\t"+HeadRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
		answer("rev-parse HEAD", "merge222\n")

	_ = engineWith(runner).ApplyMr(anEnvironment(t), anMr(12, "2.0.x"))

	checkout := runner.indexOfCommand("checkout 2.0.x")
	fetch := runner.indexOfCommand("fetch origin +refs/merge-requests/12/merge")
	if checkout < 0 || fetch < 0 {
		t.Fatalf("missing steps:\n%s", runner.transcript())
	}
	if checkout > fetch {
		t.Errorf("fetched before stepping back onto the base:\n%s", runner.transcript())
	}
}

// CI analyses the merge ref, so that is what is checked out when GitLab
// publishes one.
func TestTheMergeRefIsFetchedWhenItExists(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("ls-remote origin", "abc\t"+MergeRef(12)+"\ndef\t"+HeadRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
		answer("rev-parse HEAD", "merge222\n")

	_ = engineWith(runner).ApplyMr(anEnvironment(t), anMr(12, "2.0.x"))

	if !runner.didRun("fetch origin +refs/merge-requests/12/merge:mr-12") {
		t.Errorf("the merge ref was not fetched:\n%s", runner.transcript())
	}
	if runner.didRun("fetch origin +refs/merge-requests/12/head") {
		t.Errorf("the head ref was fetched as well:\n%s", runner.transcript())
	}
}

// No merge ref means GitLab could not merge it into its target, and the
// fallback to the head is loud.
func TestWithNoMergeRefTheHeadIsFetchedAndSaidSo(t *testing.T) {
	var logged []string
	runner := cleanOn("2.0.x").
		answer("ls-remote origin", "def\t"+HeadRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
		answer("rev-parse HEAD", "head111\n")

	engine := NewDdevContrib(nil, "/projects", runner, func(line string) { logged = append(logged, line) })
	_ = engine.ApplyMr(anEnvironment(t), anMr(12, "2.0.x"))

	if !runner.didRun("fetch origin +refs/merge-requests/12/head:mr-12") {
		t.Errorf("the head ref was not fetched:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(logged, "\n"), "no merge ref") {
		t.Errorf("the fallback was silent:\n%s", strings.Join(logged, "\n"))
	}
}

// A merge request that publishes nothing is a wrong iid, not a state to guess
// about.
func TestAMergeRequestWithNoRefsAtAllIsARefusal(t *testing.T) {
	runner := cleanOn("2.0.x").answer("ls-remote origin", "")

	err := engineWith(runner).ApplyMr(anEnvironment(t), anMr(12, "2.0.x"))
	if err == nil {
		t.Fatal("a merge request publishing no refs was applied")
	}
	if !strings.Contains(err.Error(), "Check the merge request number") {
		t.Errorf("the refusal does not say what to look at: %v", err)
	}
}

// The base is recorded so the next apply can validate the native base even
// though the working copy now sits on an mr-* branch.
func TestApplyingAMergeRequestRecordsTheBaseItCameFrom(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("ls-remote origin", "abc\t"+MergeRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
		answer("rev-parse HEAD", "merge222\n")

	_ = engineWith(runner).ApplyMr(anEnvironment(t), anMr(12, "2.0.x"))

	if !runner.didRun("config upkeep.base-branch 2.0.x") {
		t.Errorf("the base was not recorded:\n%s", runner.transcript())
	}
}

// A verdict from the wrong base is about code nobody proposed.
func TestAMergeRequestTargetingAnotherBranchIsRefusedBeforeAnyFetch(t *testing.T) {
	runner := cleanOn("1.0.x")

	err := engineWith(runner).ApplyMr(anEnvironment(t), anMr(12, "2.0.x"))
	if err == nil {
		t.Fatal("backport testing was allowed")
	}
	if !strings.Contains(err.Error(), "upkeep dev pathauto --branch=2.0.x") {
		t.Errorf("the refusal names no way forward: %v", err)
	}
	if runner.didRun("fetch") {
		t.Errorf("it fetched before refusing:\n%s", runner.transcript())
	}
}

// Uncommitted changes would be destroyed by the checkout that follows.
func TestApplyingOntoADirtyWorkingCopyIsRefused(t *testing.T) {
	runner := newRunner().
		answer("status --porcelain", " M src/PathautoGenerator.php\n").
		answer("symbolic-ref --short HEAD", "2.0.x\n").
		answer("rev-list --count @{upstream}..HEAD", "0\n")

	err := engineWith(runner).ApplyMr(anEnvironment(t), anMr(12, "2.0.x"))
	if err == nil {
		t.Fatal("a dirty working copy was checked out over")
	}
	if !strings.Contains(err.Error(), "Unstaged changes") {
		t.Errorf("the refusal does not say what is dirty: %v", err)
	}
	if runner.didRun("checkout") {
		t.Errorf("it checked out before refusing:\n%s", runner.transcript())
	}
}

func aPatchApplication() PatchApplication {
	return PatchApplication{
		IssueNid:  3603341,
		Name:      "3603341-3-d12.patch",
		LocalPath: "/cache/patches/3603341/3603341-3-d12.patch",
	}
}

// A re-roll must be tested on its own, not stacked on whatever was applied
// last time — so the branch is reset from the base on every apply.
func TestApplyingAPatchResetsItsBranchFromTheBase(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("rev-list --count HEAD..FETCH_HEAD", "0\n").
		answer("rev-parse HEAD", "abc1234\n")

	patch := aPatchApplication()
	patch.BaseBranch = "2.0.x"
	_ = engineWith(runner).ApplyPatch(anEnvironment(t), patch, RefreshUpdate)

	if !runner.didRun("checkout -B patch-3603341 FETCH_HEAD") {
		t.Errorf("the branch was not reset from the fetched base:\n%s", runner.transcript())
	}
}

// The branch the patch was cut from wins over whatever the working copy
// happens to sit on: applying a 2.0.x patch to the default branch is how a
// good patch comes to read as stale.
func TestThePatchesOwnBaseOutranksTheWorkingCopys(t *testing.T) {
	runner := cleanOn("1.0.x").
		answer("rev-list --count HEAD..FETCH_HEAD", "0\n").
		answer("rev-parse HEAD", "abc1234\n")

	patch := aPatchApplication()
	patch.BaseBranch = "2.0.x"
	_ = engineWith(runner).ApplyPatch(anEnvironment(t), patch, RefreshUpdate)

	if !runner.didRun("fetch origin 2.0.x") {
		t.Errorf("it refreshed the wrong base:\n%s", runner.transcript())
	}
	if !runner.didRun("config upkeep.base-branch 2.0.x") {
		t.Errorf("it recorded the wrong base:\n%s", runner.transcript())
	}
}

// The checks must run against a clean tree, so what was applied is committed.
func TestAnAppliedPatchIsCommittedUnderUpkeepsIdentity(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("rev-list --count HEAD..FETCH_HEAD", "0\n").
		answer("rev-parse HEAD", "abc1234\n")

	patch := aPatchApplication()
	patch.BaseBranch = "2.0.x"
	_ = engineWith(runner).ApplyPatch(anEnvironment(t), patch, RefreshUpdate)

	if !runner.didRun("commit --no-verify") {
		t.Fatalf("nothing was committed:\n%s", runner.transcript())
	}
	if !runner.didRun("user.name=upkeep") {
		t.Errorf("the commit was not made under upkeep's identity:\n%s", runner.transcript())
	}
}

// Straight, then three-way, then reduced context — and nothing further once
// one of them works.
func TestTheApplyEscalatesOnlyAsFarAsItNeedsTo(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("rev-list --count HEAD..FETCH_HEAD", "0\n").
		answer("rev-parse HEAD", "abc1234\n").
		fails("apply --index -p1 /cache")

	patch := aPatchApplication()
	patch.BaseBranch = "2.0.x"
	_ = engineWith(runner).ApplyPatch(anEnvironment(t), patch, RefreshUpdate)

	if !runner.didRun("apply --index -p1 --3way") {
		t.Errorf("it did not escalate to a three-way merge:\n%s", runner.transcript())
	}
	if runner.didRun("apply --index -p1 -C1") {
		t.Errorf("it escalated past the rung that worked:\n%s", runner.transcript())
	}
}

// Only when all three fail is the patch genuinely stale — and the working copy
// is left on the base rather than half-patched.
func TestAnUnappliablePatchLeavesTheWorkingCopyOnTheBase(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("rev-list --count HEAD..FETCH_HEAD", "0\n").
		answer("apply --stat", " pathauto.info.yml | 2 +-\n 1 file changed\n").
		answer("apply --check", "error: patch failed: pathauto.info.yml:1\n").
		fails("apply --index")

	patch := aPatchApplication()
	patch.BaseBranch = "2.0.x"

	err := engineWith(runner).ApplyPatch(anEnvironment(t), patch, RefreshUpdate)
	if err == nil {
		t.Fatalf("an unappliable patch was accepted:\n%s", runner.transcript())
	}
	if !strings.Contains(err.Error(), "pathauto.info.yml") {
		t.Errorf("the refusal does not name the stale file: %v", err)
	}
	if !runner.didRun("reset --hard") {
		t.Errorf("the half-patched tree was left behind:\n%s", runner.transcript())
	}
	for _, rung := range []string{"--3way", "-C1"} {
		if !runner.didRun("apply --index -p1 " + rung) {
			t.Errorf("it gave up before trying %s:\n%s", rung, runner.transcript())
		}
	}
	if runner.didRun("commit") {
		t.Errorf("it committed a patch that did not apply:\n%s", runner.transcript())
	}
}

// `checkout -B` would silently discard commits that may exist nowhere else, so
// an existing work branch is resumed exactly as it stands.
func TestStartingWorkOnAnExistingBranchResumesAndNeverResets(t *testing.T) {
	runner := cleanOn("2.0.x").answer("rev-parse --verify 3603341-fix", "abc1234\n")

	resumed, err := engineWith(runner).StartWork(
		anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), "", RefreshUpdate,
	)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, runner.transcript())
	}
	if !resumed {
		t.Error("an existing branch was not reported as resumed")
	}
	if runner.didRun("checkout -B") {
		t.Errorf("an existing work branch was reset:\n%s", runner.transcript())
	}
	if runner.didRun("fetch") {
		t.Errorf("resuming fetched, which could move the branch under the work:\n%s", runner.transcript())
	}
}

// Started on another machine, or in another environment for another core.
func TestAWorkBranchThatOnlyExistsOnOriginIsResumedFromThere(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("ls-remote --exit-code --heads origin 3603341-fix", "abc1234\trefs/heads/3603341-fix\n")
	runner.fails("rev-parse --verify")

	resumed, err := engineWith(runner).StartWork(
		anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), "", RefreshUpdate,
	)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, runner.transcript())
	}
	if !resumed {
		t.Error("a branch on origin was not reported as resumed")
	}
	if !runner.didRun("checkout -b 3603341-fix FETCH_HEAD") {
		t.Errorf("it did not resume from origin:\n%s", runner.transcript())
	}
	if runner.didRun("checkout -B") {
		t.Errorf("it reset rather than created:\n%s", runner.transcript())
	}
}

// A new branch is cut from a freshly fetched base, because CI checks work
// merged into the current tip.
func TestANewWorkBranchIsCutFromTheFetchedBase(t *testing.T) {
	runner := cleanOn("2.0.x").answer("rev-list --count HEAD..FETCH_HEAD", "3\n")
	runner.fails("rev-parse --verify")
	runner.fails("ls-remote --exit-code")

	resumed, err := engineWith(runner).StartWork(
		anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), "", RefreshUpdate,
	)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, runner.transcript())
	}
	if resumed {
		t.Error("a new branch was reported as resumed")
	}
	if !runner.didRun("fetch origin 2.0.x") {
		t.Errorf("the base was not refreshed:\n%s", runner.transcript())
	}
	if !runner.didRun("checkout -b 3603341-fix FETCH_HEAD") {
		t.Errorf("it was not cut from the fetched base:\n%s", runner.transcript())
	}
	if !runner.didRun("merge --ff-only") {
		t.Errorf("the local base was not fast-forwarded:\n%s", runner.transcript())
	}
}

// Everything else here degrades and says so; this one would produce a verdict
// indistinguishable from a good one.
func TestAFetchThatFailsRefusesRatherThanCuttingFromAStaleBase(t *testing.T) {
	runner := cleanOn("2.0.x")
	runner.fails("rev-parse --verify")
	runner.fails("ls-remote --exit-code")
	runner.fails("fetch origin 2.0.x")

	_, err := engineWith(runner).StartWork(
		anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), "", RefreshUpdate,
	)
	if err == nil {
		t.Fatalf("it cut from a base it could not refresh:\n%s", runner.transcript())
	}
	if !strings.Contains(err.Error(), "--no-update") {
		t.Errorf("the refusal does not name the flag that makes it a choice: %v", err)
	}
	if runner.didRun("checkout -b 3603341-fix") {
		t.Errorf("it cut the branch anyway:\n%s", runner.transcript())
	}
}

// --no-update cuts from the base exactly as it stands, and says what that
// costs.
func TestSkippingTheRefreshCutsFromTheLocalBaseAndSaysSo(t *testing.T) {
	var logged []string
	runner := cleanOn("2.0.x")
	runner.fails("rev-parse --verify")
	runner.fails("ls-remote --exit-code")

	engine := NewDdevContrib(nil, "/projects", runner, func(line string) { logged = append(logged, line) })
	if _, err := engine.StartWork(
		anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), "", RefreshSkip,
	); err != nil {
		t.Fatalf("start: %v", err)
	}

	if runner.didRun("fetch origin 2.0.x") {
		t.Errorf("it fetched despite the skip:\n%s", runner.transcript())
	}
	if !runner.didRun("checkout -b 3603341-fix 2.0.x") {
		t.Errorf("it did not cut from the local base:\n%s", runner.transcript())
	}
	if !strings.Contains(strings.Join(logged, "\n"), "may not be what CI merges into") {
		t.Errorf("the cost was not said:\n%s", strings.Join(logged, "\n"))
	}
}

// Promotion goes through the same resume-never-reset rule, because the branch
// it lands on can be the only place the work exists.
func TestPromotingGoesThroughTheWorkBranchRules(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("rev-parse --verify 3603341-fix", "abc1234\n").
		answer("rev-parse HEAD", "def5678\n")

	promotion, err := engineWith(runner).PromotePatch(
		anEnvironment(t), aPatchApplication(), NamedIssueBranch(3603341, "3603341-fix"),
		"Issue #3603341 by somebody: Fix", RefreshUpdate, false,
	)
	if err != nil {
		t.Fatalf("promote: %v\n%s", err, runner.transcript())
	}
	if runner.didRun("checkout -B") {
		t.Errorf("promoting reset the work branch:\n%s", runner.transcript())
	}
	if !promotion.IsComplete() {
		t.Error("a clean promotion did not report a commit")
	}
	sha, err := promotion.RequireSHA()
	if err != nil || sha != "def5678" {
		t.Errorf("got %q, %v", sha, err)
	}
}

// A partial promotion is deliberately not committed: half the patch plus a
// pile of rejects is not what the author wrote.
func TestAPartialPromotionCommitsNothing(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("rev-parse --verify 3603341-fix", "abc1234\n").
		answer("apply --stat", " a.php | 2 +-\n 2 files changed\n").
		answer("apply --check", "error: patch failed: b.php:1\n").
		answer("apply -p1 --reject", "Applied patch a.php cleanly.\nApplying patch b.php with 1 reject...\n")
	runner.fails("apply --index")

	promotion, err := engineWith(runner).PromotePatch(
		anEnvironment(t), aPatchApplication(), NamedIssueBranch(3603341, "3603341-fix"),
		"Issue #3603341 by somebody: Fix", RefreshUpdate, true,
	)
	if err != nil {
		t.Fatalf("promote: %v\n%s", err, runner.transcript())
	}

	if promotion.IsComplete() {
		t.Error("a partial promotion reported a commit")
	}
	if runner.didRun("commit") {
		t.Errorf("a partial promotion committed:\n%s", runner.transcript())
	}
	if !slices.Equal(promotion.Applied, []string{"a.php"}) {
		t.Errorf("applied %v", promotion.Applied)
	}
	if !slices.Equal(promotion.Rejected, []string{"b.php"}) {
		t.Errorf("rejected %v", promotion.Rejected)
	}
	if runner.didRun("reset --hard") {
		t.Errorf("the partial apply was thrown away:\n%s", runner.transcript())
	}
}

// Nothing fitting at all is no head start, so it falls through to the ordinary
// refusal rather than reporting an empty partial success.
func TestAPartialPromotionThatFitsNothingIsStillARefusal(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("rev-parse --verify 3603341-fix", "abc1234\n").
		answer("apply --stat", " a.php | 2 +-\n 1 file changed\n").
		answer("apply --check", "error: patch failed: a.php:1\n").
		answer("apply -p1 --reject", "Applying patch a.php with 3 rejects...\n")
	runner.fails("apply --index")

	_, err := engineWith(runner).PromotePatch(
		anEnvironment(t), aPatchApplication(), NamedIssueBranch(3603341, "3603341-fix"),
		"Issue #3603341 by somebody: Fix", RefreshUpdate, true,
	)
	if err == nil {
		t.Fatalf("an empty partial was reported as success:\n%s", runner.transcript())
	}
	if !runner.didRun("clean -fd") {
		t.Errorf("the rejects were left behind:\n%s", runner.transcript())
	}
}

// A rejected push means the remote moved, which is a thing to look at rather
// than to overwrite.
func TestPushingNeverForces(t *testing.T) {
	runner := cleanOn("3603341-fix").
		answer("remote get-url --push issue-3603341", "git@git.drupal.org:issue/pathauto-3603341.git\n").
		answer("rev-parse HEAD", "abc1234\n")

	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")
	sha, err := engineWith(runner).PushWork(anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), remote)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, runner.transcript())
	}
	if sha != "abc1234" {
		t.Errorf("got %q", sha)
	}
	for _, forbidden := range []string{"--force", "--force-with-lease"} {
		if runner.didRun("push", forbidden) {
			t.Errorf("the push used %s:\n%s", forbidden, runner.transcript())
		}
	}
}

// Publishing a branch you are not looking at is not what anyone meant.
func TestPushingRefusesWhenTheWorkingCopyIsElsewhere(t *testing.T) {
	runner := cleanOn("2.0.x")

	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")
	_, err := engineWith(runner).PushWork(anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), remote)
	if err == nil {
		t.Fatal("it pushed a branch the working copy was not on")
	}
	if !strings.Contains(err.Error(), "switch to it first") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
	if runner.didRun("push") {
		t.Errorf("it pushed anyway:\n%s", runner.transcript())
	}
}

// A refused push is diagnosed rather than reported raw.
func TestARefusedPushIsDiagnosed(t *testing.T) {
	runner := cleanOn("3603341-fix").
		answer("remote get-url --push issue-3603341", "git@git.drupal.org:issue/pathauto-3603341.git\n").
		answer("push --set-upstream", "remote: GitLab: You are not allowed to push code to this project.\n")
	runner.fails("push --set-upstream")

	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")
	_, err := engineWith(runner).PushWork(anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), remote)
	if err == nil {
		t.Fatal("a refused push was reported as success")
	}
	if !strings.Contains(err.Error(), "Get push access") {
		t.Errorf("the refusal was not diagnosed as authorization: %v", err)
	}
}

// Origin is never touched: fetch stays anonymous over HTTPS, so checking a
// merge request or a patch still needs no key at all.
func TestPushingAddsItsOwnRemoteAndLeavesOriginAlone(t *testing.T) {
	runner := cleanOn("3603341-fix").answer("rev-parse HEAD", "abc1234\n")
	runner.fails("remote get-url --push issue-3603341")

	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")
	if _, err := engineWith(runner).PushWork(
		anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), remote,
	); err != nil {
		t.Fatalf("push: %v\n%s", err, runner.transcript())
	}

	if !runner.didRun("remote add issue-3603341") {
		t.Errorf("the fork remote was not added:\n%s", runner.transcript())
	}
	for _, line := range runner.ran {
		if strings.Contains(line, "remote set-url origin") || strings.Contains(line, "remote add origin") {
			t.Errorf("origin was altered: %s", line)
		}
	}
}

// A remote pointing somewhere else is re-pointed rather than left wrong.
func TestAStaleForkRemoteIsRepointed(t *testing.T) {
	runner := cleanOn("3603341-fix").
		answer("remote get-url --push issue-3603341", "git@git.drupal.org:issue/pathauto-9999999.git\n").
		answer("rev-parse HEAD", "abc1234\n")

	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")
	if _, err := engineWith(runner).PushWork(
		anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), remote,
	); err != nil {
		t.Fatalf("push: %v", err)
	}

	if !runner.didRun("remote set-url issue-3603341 " + remote.URL) {
		t.Errorf("the stale remote was not re-pointed:\n%s", runner.transcript())
	}
}

// What is not committed cannot be pushed.
func TestPushingADirtyWorkingCopyIsRefused(t *testing.T) {
	runner := newRunner().
		answer("status --porcelain", "?? scratch.php\n").
		answer("symbolic-ref --short HEAD", "3603341-fix\n").
		answer("rev-list --count @{upstream}..HEAD", "0\n")

	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")
	_, err := engineWith(runner).PushWork(anEnvironment(t), NamedIssueBranch(3603341, "3603341-fix"), remote)
	if err == nil {
		t.Fatal("a dirty working copy was pushed")
	}
	if !strings.Contains(err.Error(), "cannot be pushed") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// requiring makes the module working copy declare a dependency, which is what
// a branch switch has to pick up.
func requiring(t *testing.T, environment Environment, requires string) {
	t.Helper()

	manifest := `{"name":"drupal/pathauto","require":{"drupal/core":"^10 || ^11",` + requires + `}}`
	path := filepath.Join(environment.ProjectPath, moduleDir, "composer.json")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// Every branch switch resolves what that branch declares it needs.
//
// A merge request or a patch can add a dependency, and without this the checks
// fail on a missing class rather than on anything the contribution got wrong.
// It is the half of the old composer pin worth keeping — the pin itself only
// existed to make the module resolvable as a package, which is what made an
// environment for an undeclared core impossible to build.
func TestEveryBranchSwitchResolvesWhatTheBranchRequires(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("ls-remote origin", "abc\t"+MergeRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
		answer("rev-parse HEAD", "merge222\n")
	environment := anEnvironment(t)
	requiring(t, environment, `"drupal/token":"^1.0"`)

	if err := engineWith(runner).ApplyMr(environment, anMr(12, "2.0.x")); err != nil {
		t.Fatalf("apply: %v\n%s", err, runner.transcript())
	}

	if !runner.didRun("composer require drupal/token") {
		t.Errorf("the branch's dependency was not resolved:\n%s", runner.transcript())
	}
	// And never the module itself: installing it as a package is what enforced
	// its declared core against the seeded one.
	if runner.didRun("composer require drupal/pathauto") {
		t.Errorf("the module was required as a package:\n%s", runner.transcript())
	}
	// drupal/core is dropped, or a module declaring ^10 || ^11 could not be
	// wired into a core 12 environment at all.
	if runner.didRun("composer require drupal/core") {
		t.Errorf("the module's core constraint was imposed on the site:\n%s", runner.transcript())
	}
}

// A module requiring nothing but core costs no composer call at all.
func TestABranchWithNoDependenciesRunsNoComposer(t *testing.T) {
	runner := cleanOn("2.0.x")
	environment := anEnvironment(t)
	requiring(t, environment, "")

	if err := engineWith(runner).CheckoutBranch(environment, "3.0.x"); err != nil {
		t.Fatalf("checkout: %v\n%s", err, runner.transcript())
	}

	if runner.didRun("composer require") {
		t.Errorf("composer ran for an empty requirement list:\n%s", runner.transcript())
	}
}

// The module must be reached through a symlink and never a copy: a copy is a
// tree git does not own, so the next apply would mutate one and the checks
// would read the other. Held on every branch switch, whether or not anything
// was installed — composer puts the module's dependencies into the same
// directory, and composer/installers is capable of writing over a path it
// thinks it owns.
func TestAMirroredModuleIsARefusal(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("ls-remote origin", "abc\t"+MergeRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
		answer("rev-parse HEAD", "merge222\n")

	err := engineWith(runner).ApplyMr(aMirroredEnvironment(t), anMr(12, "2.0.x"))
	if err == nil {
		t.Fatal("a module that is not a symlink was accepted")
	}
	if !strings.Contains(err.Error(), "ownership constraint") {
		t.Errorf("the refusal does not say what was violated: %v", err)
	}
}

// The base a previous apply recorded, so a later command can cut from the same
// branch without asking again.
func TestTheRecordedBaseBranchIsReadFromTheWorkingCopy(t *testing.T) {
	runner := newRunner().answer("config --get upkeep.base-branch", "2.0.x\n")

	if got := engineWith(runner).RecordedBaseBranch(anEnvironment(t)); got != "2.0.x" {
		t.Errorf("got %q", got)
	}
}

// Nothing recorded is "" rather than a guess: the caller falls back to
// resolving one, and a wrong answer here would cut a branch from the wrong
// place.
func TestNoRecordedBaseBranchIsEmptyRatherThanAGuess(t *testing.T) {
	runner := newRunner()
	runner.fails("config --get upkeep.base-branch")

	if got := engineWith(runner).RecordedBaseBranch(anEnvironment(t)); got != "" {
		t.Errorf("got %q, want none", got)
	}
}

// Dependencies are resolved after the branch moves, never before: the
// requirements being installed are the ones the new branch declares, and the
// manifest is only on disk once the checkout has happened.
func TestSwitchingBranchesResolvesAfterTheBranchMoves(t *testing.T) {
	runner := cleanOn("2.0.x")
	environment := anEnvironment(t)
	requiring(t, environment, `"drupal/token":"^1.0"`)

	if err := engineWith(runner).CheckoutBranch(environment, "3.0.x"); err != nil {
		t.Fatalf("checkout: %v\n%s", err, runner.transcript())
	}

	checkout := runner.indexOfCommand("checkout 3.0.x")
	resolve := runner.indexOfCommand("composer require", "drupal/token")
	if checkout < 0 || resolve < 0 {
		t.Fatalf("missing steps:\n%s", runner.transcript())
	}
	if checkout > resolve {
		t.Errorf("dependencies were resolved before the branch moved:\n%s", runner.transcript())
	}
}

// Uncommitted changes stop the switch: git would carry them across, and the
// checks that follow would then be judging a branch plus somebody's work.
func TestSwitchingBranchesRefusesOverUncommittedChanges(t *testing.T) {
	runner := cleanOn("2.0.x").answer("status --porcelain", " M src/PathautoGenerator.php\n")

	err := engineWith(runner).CheckoutBranch(anEnvironment(t), "3.0.x")
	if err == nil {
		t.Fatal("it switched branches over uncommitted changes")
	}
	if !strings.Contains(err.Error(), "Unstaged changes") {
		t.Errorf("the refusal does not say what is in the way: %v", err)
	}
	if runner.didRun("checkout 3.0.x") {
		t.Errorf("it switched anyway:\n%s", runner.transcript())
	}
}

// A branch that does not exist is git's refusal, passed through rather than
// followed by a pin to a branch nothing is on.
func TestSwitchingToABranchThatIsNotThereDoesNotSyncThePin(t *testing.T) {
	runner := cleanOn("2.0.x")
	runner.fails("checkout 4.0.x")

	if err := engineWith(runner).CheckoutBranch(anEnvironment(t), "4.0.x"); err == nil {
		t.Fatal("it reported success for a branch that does not exist")
	}
	if runner.didRun("composer require", "drupal/pathauto:4.0.x-dev") {
		t.Errorf("it pinned to a branch it never reached:\n%s", runner.transcript())
	}
}

// A checkout that did not stick is a refusal, not a check run against whatever
// the working copy happens to hold.
func TestAnMrCheckoutThatDidNotStickIsARefusal(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("ls-remote origin", "abc\t"+MergeRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "2.0.x\n")

	err := engineWith(runner).ApplyMr(anEnvironment(t), anMr(12, "2.0.x"))
	if err == nil {
		t.Fatal("it reported an MR applied while the working copy was elsewhere")
	}
	if !strings.Contains(err.Error(), "did not stick") {
		t.Errorf("the refusal does not say what happened: %v", err)
	}
}

// A head that moved since the model was fetched is worth saying — on the head
// ref, which is the only place the comparison means anything.
func TestAMovedHeadIsNotedWhenTheHeadRefWasUsed(t *testing.T) {
	runner := cleanOn("2.0.x").
		// No merge ref: GitLab computes none for an MR that conflicts.
		answer("ls-remote origin", "def\t"+HeadRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
		answer("rev-parse HEAD", "moved999\n")

	engine, said := logging(nil, runner)
	if err := engine.ApplyMr(anEnvironment(t), anMr(12, "2.0.x")); err != nil {
		t.Fatalf("apply: %v\n%s", err, runner.transcript())
	}

	if !strings.Contains(strings.Join(*said, "\n"), "may have moved") {
		t.Errorf("a moved head was not noted:\n%s", strings.Join(*said, "\n"))
	}
}

// And never on the merge ref: the checked-out commit there is one GitLab made
// by merging the branch into the target, so it is never the MR's head SHA and
// comparing them would warn on every healthy run.
func TestAMergeRefCheckoutIsNeverReportedAsAMovedHead(t *testing.T) {
	runner := cleanOn("2.0.x").
		answer("ls-remote origin", "abc\t"+MergeRef(12)+"\ndef\t"+HeadRef(12)+"\n").
		answer("rev-parse --abbrev-ref HEAD", "mr-12\n").
		answer("rev-parse HEAD", "merge222\n")

	engine, said := logging(nil, runner)
	if err := engine.ApplyMr(anEnvironment(t), anMr(12, "2.0.x")); err != nil {
		t.Fatalf("apply: %v\n%s", err, runner.transcript())
	}

	if strings.Contains(strings.Join(*said, "\n"), "may have moved") {
		t.Errorf("every healthy merge-ref run would warn:\n%s", strings.Join(*said, "\n"))
	}
}

// Applying a patch resets its branch from the base, so uncommitted work in the
// way is a refusal rather than something to carry across.
func TestApplyingAPatchRefusesOverUncommittedChanges(t *testing.T) {
	runner := cleanOn("2.0.x").answer("status --porcelain", "?? notes.txt\n")

	err := engineWith(runner).ApplyPatch(
		anEnvironment(t), PatchApplication{IssueNid: 3601234, LocalPath: "/tmp/p.patch"}, RefreshUpdate,
	)
	if err == nil {
		t.Fatal("it reset a branch over uncommitted work")
	}
	if !strings.Contains(err.Error(), "Untracked files") {
		t.Errorf("the refusal does not say what is in the way: %v", err)
	}
}

// An existing work branch is resumed exactly as it stands: `checkout -B`, which
// the disposable paths use, would silently discard commits held nowhere else.
func TestAnExistingWorkBranchIsResumedAndNeverReset(t *testing.T) {
	branch := IssueBranchFor(3601234, "Fix the thing")
	runner := cleanOn("2.0.x").answer("rev-parse --verify "+branch.Name, "abc\n")

	resumed, err := engineWith(runner).StartWork(anEnvironment(t), branch, "", RefreshUpdate)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, runner.transcript())
	}

	if !resumed {
		t.Error("resuming an existing branch did not report itself as a resume")
	}
	if !runner.didRun("checkout " + branch.Name) {
		t.Errorf("the branch was not checked out:\n%s", runner.transcript())
	}
	if runner.didRun("checkout -B") || runner.didRun("checkout -b") {
		t.Errorf("an existing work branch was reset:\n%s", runner.transcript())
	}
}

// A branch pushed from another machine — or from the environment for another
// core — is fetched and resumed rather than cut afresh over the top of it.
func TestAWorkBranchThatExistsOnlyOnOriginIsResumedFromThere(t *testing.T) {
	branch := IssueBranchFor(3601234, "Fix the thing")
	runner := cleanOn("2.0.x").
		answer("ls-remote --exit-code --heads origin "+branch.Name, "abc\trefs/heads/"+branch.Name+"\n")

	resumed, err := engineWith(runner).StartWork(anEnvironment(t), branch, "", RefreshUpdate)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, runner.transcript())
	}

	if !resumed {
		t.Error("resuming from origin did not report itself as a resume")
	}
	if !runner.didRun("fetch origin "+branch.Name) ||
		!runner.didRun("checkout -b "+branch.Name+" FETCH_HEAD") {
		t.Errorf("it did not resume from origin:\n%s", runner.transcript())
	}
}

// Starting work on a dirty working copy would mix somebody's changes into the
// new branch.
func TestStartingWorkRefusesOverUncommittedChanges(t *testing.T) {
	branch := IssueBranchFor(3601234, "Fix the thing")
	runner := cleanOn("2.0.x").answer("status --porcelain", "M  src/Thing.php\n")

	if _, err := engineWith(runner).StartWork(anEnvironment(t), branch, "", RefreshUpdate); err == nil {
		t.Fatal("it started a branch over uncommitted changes")
	}
	if runner.didRun("checkout -b") {
		t.Errorf("it cut the branch anyway:\n%s", runner.transcript())
	}
}

// Publishing pushes the branch you are looking at, so a working copy sitting
// somewhere else is a refusal rather than a surprise push.
func TestPublishingFromTheWrongPlaceIsARefusalThatSaysWhereYouAre(t *testing.T) {
	branch := IssueBranchFor(3601234, "Fix the thing")
	remote := IssueForkRemote(3601234, "git@git.drupal.org:issue/pathauto-3601234.git")

	for where, expected := range map[string]string{"2.0.x\n": `"2.0.x"`, "": "a detached HEAD"} {
		runner := cleanOn("2.0.x").answer("symbolic-ref --short HEAD", where)
		if where == "" {
			runner.fails("symbolic-ref --short HEAD")
		}

		_, err := engineWith(runner).PushWork(anEnvironment(t), branch, remote)
		if err == nil {
			t.Fatalf("%q: it pushed a branch nobody was on", where)
		}
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("%q: the refusal does not say where you are: %v", where, err)
		}
		if runner.didRun("push") {
			t.Errorf("%q: it pushed anyway:\n%s", where, runner.transcript())
		}
	}
}

// anUnwiredEnvironment is a project whose module working copy exists but which
// has nothing in web/modules/contrib yet — a fresh provision, before wiring.
func anUnwiredEnvironment(t *testing.T) Environment {
	t.Helper()

	projectPath := filepath.Join(t.TempDir(), "upkeep-pathauto-d11")
	if err := os.MkdirAll(filepath.Join(projectPath, moduleDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	return Environment{
		ModuleName: "pathauto", CoreMajor: "11",
		ProjectName: "upkeep-pathauto-d11", ProjectPath: projectPath,
	}
}

// The link is made, and made relative, so the project tree stays relocatable
// — an absolute link breaks the moment the projects root moves, which
// --projects-root invites.
func TestTheModuleIsLinkedRelativelyIntoTheSite(t *testing.T) {
	environment := anUnwiredEnvironment(t)

	if err := engineWith(newRunner()).linkWorkingCopy(
		environment.ProjectPath, environment.ModuleName,
	); err != nil {
		t.Fatalf("link: %v", err)
	}

	link := moduleSymlink(environment.ProjectPath, environment.ModuleName)
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("not a symlink: %v", err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("the link is absolute: %q", target)
	}
	// And it resolves to the working copy rather than merely existing.
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("the link does not resolve: %v", err)
	}
	expected, err := filepath.EvalSymlinks(moduleWorkingCopy(environment.ProjectPath))
	if err != nil {
		t.Fatalf("working copy: %v", err)
	}
	if resolved != expected {
		t.Errorf("the link points at %q, want %q", resolved, expected)
	}
}

// A link left by an earlier layout is replaced rather than refused: it is not
// a tree anybody owns, and refusing would make an existing environment
// unusable over a detail re-provisioning would fix anyway.
func TestAStaleModuleLinkIsReplaced(t *testing.T) {
	environment := anUnwiredEnvironment(t)
	link := moduleSymlink(environment.ProjectPath, environment.ModuleName)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("/somewhere/else", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := engineWith(newRunner()).linkWorkingCopy(
		environment.ProjectPath, environment.ModuleName,
	); err != nil {
		t.Fatalf("link: %v", err)
	}

	if target, _ := os.Readlink(link); strings.Contains(target, "somewhere/else") {
		t.Errorf("the stale link survived: %q", target)
	}
}

// A real directory there is refused, and this is the one case that must be:
// it is a copy git does not own, so an apply would mutate the working copy
// while the checks read the copy, and the two would disagree in silence.
func TestARealDirectoryWhereTheLinkGoesIsARefusal(t *testing.T) {
	environment := anUnwiredEnvironment(t)
	if err := os.MkdirAll(
		moduleSymlink(environment.ProjectPath, environment.ModuleName), 0o755,
	); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := engineWith(newRunner()).linkWorkingCopy(
		environment.ProjectPath, environment.ModuleName,
	)
	if err == nil {
		t.Fatal("a real directory was overwritten")
	}
	if !strings.Contains(err.Error(), "real directory") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

// A failed dependency install fails the operation: the checks would otherwise
// run against a site missing something the branch says it needs, and report
// the contribution as broken.
func TestAFailedDependencyInstallIsNotSwallowed(t *testing.T) {
	runner := newRunner()
	runner.fails("composer require")
	environment := anEnvironment(t)
	requiring(t, environment, `"drupal/token":"^1.0"`)

	if err := engineWith(runner).syncModuleDependencies(
		environment.ProjectPath, environment.ModuleName,
	); err == nil {
		t.Fatalf("it carried on:\n%s", runner.transcript())
	}
}

// A link that cannot be made fails the wiring rather than leaving an
// environment whose checks would read nothing.
//
// Each of the three ways it can fail is reachable, and all three matter: the
// directory, the replacement of what is there, and the link itself.
func TestALinkThatCannotBeMadeFailsTheWiring(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into every directory regardless of its mode")
	}

	locked := func(t *testing.T, stale bool) Environment {
		t.Helper()

		environment := anUnwiredEnvironment(t)
		contrib := filepath.Dir(moduleSymlink(environment.ProjectPath, environment.ModuleName))
		if err := os.MkdirAll(contrib, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if stale {
			if err := os.Symlink("/elsewhere", moduleSymlink(
				environment.ProjectPath, environment.ModuleName,
			)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}
		if err := os.Chmod(contrib, 0o555); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(contrib, 0o755) })

		return environment
	}

	// The link cannot be created.
	fresh := locked(t, false)
	if err := engineWith(newRunner()).linkWorkingCopy(
		fresh.ProjectPath, fresh.ModuleName,
	); err == nil {
		t.Error("a link that could not be created was reported as made")
	}

	// What is already there cannot be removed to make way for it.
	occupied := locked(t, true)
	if err := engineWith(newRunner()).linkWorkingCopy(
		occupied.ProjectPath, occupied.ModuleName,
	); err == nil {
		t.Error("a link that could not be replaced was reported as replaced")
	}

	// And the directory it goes in cannot be made at all, which is the case
	// that reaches wiring through provisioning rather than directly.
	environment := anUnwiredEnvironment(t)
	web := filepath.Join(environment.ProjectPath, "web")
	if err := os.MkdirAll(web, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(web, 0o755) })

	if err := engineWith(newRunner()).wireModule(
		cockpit.Module{Name: environment.ModuleName}, environment.ProjectPath,
	); err == nil {
		t.Error("wiring reported success over a directory it could not create")
	}
}

// aFork is where a merge request's branch lives, and the remote that pushes
// back to it.
func aFork() GitRemote {
	return IssueForkRemote(3628056, "git@git.drupal.org:issue/jumplinks-3628056.git")
}

const forkHTTPS = "https://git.drupalcode.org/issue/jumplinks-3628056.git"

// The merge request's own branch is fetched from the fork and checked out,
// with the push remote added — the manual sequence this replaces.
func TestAMergeRequestBranchIsFetchedFromItsForkAndCheckedOut(t *testing.T) {
	runner := cleanOn("1.0.x")
	// No local branch of that name yet.
	runner.fails("rev-parse --verify refs/heads/project-update-bot-only")

	if err := engineWith(runner).CheckoutMergeRequestBranch(
		anEnvironment(t), aFork(), forkHTTPS, "project-update-bot-only",
	); err != nil {
		t.Fatalf("checkout: %v\n%s", err, runner.transcript())
	}

	// The remote is added with the SSH URL GitLab advertises, under the name
	// publish uses, so pushing back needs no re-pointing.
	if !runner.didRun("remote add issue-3628056 git@git.drupal.org:issue/jumplinks-3628056.git") {
		t.Errorf("the push remote was not added:\n%s", runner.transcript())
	}
	// The fetch is the anonymous HTTPS URL, so taking over somebody's merge
	// request needs no key.
	if !runner.didRun("fetch " + forkHTTPS + " project-update-bot-only") {
		t.Errorf("it did not fetch from the fork anonymously:\n%s", runner.transcript())
	}
	// Tracking, so a later plain `git push` goes to the right place.
	if !runner.didRun("checkout -b project-update-bot-only --track issue-3628056/project-update-bot-only") {
		t.Errorf("the branch was not checked out tracking the fork:\n%s", runner.transcript())
	}
}

// An existing local branch is resumed exactly as it stands.
//
// It may hold commits that exist nowhere else — somebody's half-finished work
// on this very merge request — and `checkout -B` on it would be the
// destruction this command exists to avoid.
func TestAnExistingLocalBranchIsResumedNotReset(t *testing.T) {
	runner := cleanOn("1.0.x").
		answer("rev-parse --verify refs/heads/project-update-bot-only", "abc123\n")

	if err := engineWith(runner).CheckoutMergeRequestBranch(
		anEnvironment(t), aFork(), forkHTTPS, "project-update-bot-only",
	); err != nil {
		t.Fatalf("checkout: %v\n%s", err, runner.transcript())
	}

	if !runner.didRun("checkout project-update-bot-only") {
		t.Errorf("it did not check the branch out:\n%s", runner.transcript())
	}
	for _, destructive := range []string{"checkout -B", "checkout -b", "reset"} {
		if runner.didRun(destructive) {
			t.Errorf("%q ran against an existing branch:\n%s", destructive, runner.transcript())
		}
	}
}

// A dirty working copy is refused before anything is fetched: switching branch
// under uncommitted work either fails or carries it somewhere it was not
// meant to go.
func TestADirtyWorkingCopyRefusesAMergeRequestCheckout(t *testing.T) {
	runner := newRunner().
		answer("status --porcelain", " M jumplinks.info.yml\n").
		answer("symbolic-ref --short HEAD", "1.0.x\n").
		answer("rev-list --count @{upstream}..HEAD", "0\n")

	err := engineWith(runner).CheckoutMergeRequestBranch(
		anEnvironment(t), aFork(), forkHTTPS, "project-update-bot-only",
	)
	if err == nil {
		t.Fatalf("a dirty working copy was switched:\n%s", runner.transcript())
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if runner.didRun("fetch") {
		t.Errorf("it fetched anyway:\n%s", runner.transcript())
	}
}

// A failed fetch stops the checkout: carrying on would put the working copy on
// a stale branch and report it as the merge request's.
func TestAFailedForkFetchStopsTheCheckout(t *testing.T) {
	runner := cleanOn("1.0.x")
	runner.fails("fetch")

	if err := engineWith(runner).CheckoutMergeRequestBranch(
		anEnvironment(t), aFork(), forkHTTPS, "project-update-bot-only",
	); err == nil {
		t.Fatalf("it carried on:\n%s", runner.transcript())
	}
	if runner.didRun("checkout -b") {
		t.Errorf("it checked out after a failed fetch:\n%s", runner.transcript())
	}
}

// A remote that cannot be added stops the checkout, because the branch would
// then be fetched with nowhere to push it back to.
func TestARemoteThatCannotBeAddedStopsTheCheckout(t *testing.T) {
	runner := cleanOn("1.0.x")
	runner.fails("remote add")

	if err := engineWith(runner).CheckoutMergeRequestBranch(
		anEnvironment(t), aFork(), forkHTTPS, "project-update-bot-only",
	); err == nil {
		t.Fatalf("it carried on:\n%s", runner.transcript())
	}
	if runner.didRun("fetch") {
		t.Errorf("it fetched with no remote to push back to:\n%s", runner.transcript())
	}
}

// Either way of checking the branch out can fail, and neither may be reported
// as a successful checkout.
func TestAFailedBranchCheckoutStopsTheMergeRequestCheckout(t *testing.T) {
	for name, script := range map[string]func(*recordingRunner){
		"a new tracking branch": func(r *recordingRunner) {
			r.fails("rev-parse --verify refs/heads/project-update-bot-only")
			r.fails("checkout -b")
		},
		"an existing one": func(r *recordingRunner) {
			r.answer("rev-parse --verify refs/heads/project-update-bot-only", "abc123\n")
			r.fails("checkout project-update-bot-only")
		},
	} {
		runner := cleanOn("1.0.x")
		script(runner)

		if err := engineWith(runner).CheckoutMergeRequestBranch(
			anEnvironment(t), aFork(), forkHTTPS, "project-update-bot-only",
		); err == nil {
			t.Errorf("%s: a failed checkout was reported as done:\n%s", name, runner.transcript())
		}
	}
}

// Switching to a merge request's branch resolves what that branch declares it
// needs: a contribution can add a dependency, and without this the checks fail
// on a missing class rather than on the contribution.
func TestAMergeRequestCheckoutResolvesTheBranchsDependencies(t *testing.T) {
	runner := cleanOn("1.0.x")
	runner.fails("rev-parse --verify refs/heads/project-update-bot-only")
	environment := anEnvironment(t)
	requiring(t, environment, `"drupal/token":"^1.0"`)

	if err := engineWith(runner).CheckoutMergeRequestBranch(
		environment, aFork(), forkHTTPS, "project-update-bot-only",
	); err != nil {
		t.Fatalf("checkout: %v\n%s", err, runner.transcript())
	}

	checkout := runner.indexOfCommand("checkout -b project-update-bot-only")
	resolve := runner.indexOfCommand("composer require", "drupal/token")
	if checkout < 0 || resolve < 0 {
		t.Fatalf("missing steps:\n%s", runner.transcript())
	}
	if checkout > resolve {
		t.Errorf("dependencies were resolved before the branch moved:\n%s", runner.transcript())
	}
}
