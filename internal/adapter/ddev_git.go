package adapter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// ApplyMr checks out the merge request's code in the module working copy.
func (d *DdevContrib) ApplyMr(environment Environment, mergeRequest gitlab.MergeRequest) error {
	dir := moduleWorkingCopy(environment.ProjectPath)

	status := InspectWorkingCopy(dir, d.runner)
	if status.IsDirty() {
		return fmt.Errorf(
			"cannot apply MR !%d: the module working copy has uncommitted changes:\n  %s\n"+
				"Commit or stash your changes first, then re-run",
			mergeRequest.IID, strings.Join(status.Describe(), "\n  "),
		)
	}

	currentBranch, _ := d.gitProbe(dir, "symbolic-ref", "--short", "HEAD")
	recordedBase, _ := d.gitProbe(dir, "config", "--get", "upkeep.base-branch")

	baseBranch, err := ResolveBaseBranch(currentBranch, recordedBase)
	if err != nil {
		return err
	}
	if err := AssertNativeBase(mergeRequest, baseBranch, environment.ModuleName); err != nil {
		return err
	}

	d.log(fmt.Sprintf(
		"Applying MR !%d (%s -> %s) into the module working copy ...",
		mergeRequest.IID, mergeRequest.SourceBranch, mergeRequest.TargetBranch,
	))

	// Step back onto the base first: git refuses to fetch into the currently
	// checked-out branch, which mr-<iid> is on a re-apply.
	if _, err := d.git(dir, "checkout", baseBranch); err != nil {
		return err
	}

	ref, err := d.resolveMergeRequestRef(dir, mergeRequest.IID)
	if err != nil {
		return err
	}

	branch := ManagedBranchForMergeRequest(mergeRequest.IID)
	if _, err := d.git(dir, "fetch", "origin", FetchRefspec(ref, mergeRequest.IID)); err != nil {
		return err
	}
	if _, err := d.git(dir, "checkout", branch); err != nil {
		return err
	}
	// Recorded so the next apply can validate the native base even though the
	// working copy now sits on an mr-* branch.
	if _, err := d.git(dir, "config", "upkeep.base-branch", baseBranch); err != nil {
		return err
	}

	head, err := d.git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	if head != branch {
		return fmt.Errorf(
			"MR checkout did not stick: working copy is on %q, expected %q", head, branch,
		)
	}

	sha, err := d.git(dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	sha = strings.TrimSpace(sha)

	// Only meaningful against the head ref. On the merge ref the checked-out
	// commit is one GitLab made by merging the branch into the target, so it is
	// *never* the merge request's head SHA and comparing them would warn on
	// every healthy run.
	if ref == HeadRef(mergeRequest.IID) && mergeRequest.HeadSHA != "" && sha != mergeRequest.HeadSHA {
		d.log(fmt.Sprintf(
			"Note: checked-out head %s differs from the MR model's head %s — the MR may have moved "+
				"since it was fetched.",
			sha, mergeRequest.HeadSHA,
		))
	}

	d.log("Syncing the composer pin to the MR branch (and resolving any dependencies the MR adds) ...")
	if err := d.requireWorkingCopyBranch(environment.ProjectPath, environment.ModuleName, head); err != nil {
		return err
	}

	d.log(fmt.Sprintf(
		"MR !%d applied: working copy on %s at %s (base %s).", mergeRequest.IID, head, sha, baseBranch,
	))

	return nil
}

// ApplyPatch applies a downloaded patch onto a branch off the base, and
// commits it so the checks that follow run against a clean tree.
func (d *DdevContrib) ApplyPatch(environment Environment, patch PatchApplication, refresh BaseRefresh) error {
	dir := moduleWorkingCopy(environment.ProjectPath)

	status := InspectWorkingCopy(dir, d.runner)
	if status.IsDirty() {
		return fmt.Errorf(
			"cannot apply patch %q: the module working copy has uncommitted changes:\n  %s\n"+
				"Commit or stash your changes first, then re-run",
			patch.Name, strings.Join(status.Describe(), "\n  "),
		)
	}

	// The branch the patch was cut from wins over whatever the working copy
	// happens to sit on: an issue filed against 2.0.x carries patches for
	// 2.0.x, and applying them to the default branch is how a good patch comes
	// to read as stale.
	baseBranch := patch.BaseBranch
	if baseBranch == "" {
		resolved, err := d.resolveBase(dir)
		if err != nil {
			return err
		}
		baseBranch = resolved
	}

	branch := patch.BranchName()
	d.log(fmt.Sprintf("Applying patch %q onto %s as %s ...", patch.Name, baseBranch, branch))

	// Reset the branch from the base on every apply: a re-roll must be tested
	// on its own, not stacked on whatever was applied last time.
	if _, err := d.git(dir, "checkout", baseBranch); err != nil {
		return err
	}
	cutPoint, err := d.refreshBase(dir, baseBranch, refresh)
	if err != nil {
		return err
	}
	if _, err := d.git(dir, "checkout", "-B", branch, cutPoint); err != nil {
		return err
	}
	if _, err := d.git(dir, "config", "upkeep.base-branch", baseBranch); err != nil {
		return err
	}

	if _, err := d.applyPatchFile(dir, patch, baseBranch, environment.ModuleName, false); err != nil {
		return err
	}

	if _, err := d.git(dir,
		"-c", "user.name=upkeep",
		"-c", "user.email=upkeep@localhost",
		"commit", "--no-verify", "-m", PatchCommitMessage(patch),
	); err != nil {
		return err
	}

	sha, err := d.git(dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	d.log("Syncing the composer pin to the patch branch ...")
	if err := d.requireWorkingCopyBranch(environment.ProjectPath, environment.ModuleName, branch); err != nil {
		return err
	}

	d.log(fmt.Sprintf(
		"Patch %q applied: working copy on %s at %s (base %s).",
		patch.Name, branch, strings.TrimSpace(sha), baseBranch,
	))

	return nil
}

// applyAttempt is one rung of the escalation.
type applyAttempt struct {
	label string
	args  []string
}

// applyPatchFile is the apply itself: an escalation, not a single attempt.
//
// Each rung loosens something different, and none loosens what has to *match*
// — the changed lines are compared exactly throughout:
//
//  1. straight — the patch as cut.
//  2. three-way — resolves hunks plain context matching rejects, whenever the
//     blobs the patch was generated against are in the repository, which for a
//     drupal.org patch on its own project is common.
//  3. reduced context — requires one line of surrounding context instead of
//     three. The usual cause of needing this is not a stale patch but
//     trailing-whitespace drift: drupal.org patches are generated against an
//     export whose files may carry a trailing blank line the repository does
//     not, so a hunk header promises seven context lines for a six-line file
//     and git refuses all nine files over one of them.
//
// Only when all three fail is the patch genuinely stale, and then the report
// names which files are stale rather than only that something was.
//
// The returned promotion is set only for a partial apply.
func (d *DdevContrib) applyPatchFile(
	dir string,
	patch PatchApplication,
	baseBranch, moduleName string,
	allowPartial bool,
) (PatchPromotion, error) {
	attempts := []applyAttempt{
		{"straight", ApplyArgs(patch.LocalPath)},
		{"three-way", ThreeWayApplyArgs(patch.LocalPath)},
		{"reduced context", ReducedContextApplyArgs(patch.LocalPath)},
	}

	for i, attempt := range attempts {
		if i > 0 {
			d.log(fmt.Sprintf("Retrying with %s ...", attempt.label))
		}

		result := d.gitCapture(dir, attempt.args...)
		if result.ExitCode == nil || *result.ExitCode != 0 {
			continue
		}

		if i > 0 {
			// A hunk placed on one line of context is a weaker guarantee than
			// one placed on three. The operator is told which they got,
			// because they are the one reviewing the result.
			d.log(fmt.Sprintf(
				"Applied via %s — the patch did not match the working copy exactly; review the result with "+
					"that in mind.",
				attempt.label,
			))
		}

		return PatchPromotion{}, nil
	}

	// Nothing applied. Ask git what it wanted and where it failed, so the
	// report can name the stale file rather than just the stale patch.
	stat := d.gitCapture(dir, StatArgs(patch.LocalPath)...)
	checked := d.gitCapture(dir, CheckArgs(patch.LocalPath)...)

	if allowPartial {
		partial, fitted := d.applyWhatFits(dir, patch)
		if fitted {
			return partial, nil
		}
		// Nothing fitted either, so there is no re-roll to start from. Falls
		// through to the ordinary refusal rather than reporting an empty
		// partial success.
	}

	// Leave the working copy on the base rather than half-patched: the next
	// command must not inherit a tree nobody chose.
	d.gitProbe(dir, "reset", "--hard")
	d.gitProbe(dir, "checkout", baseBranch)

	return PatchPromotion{}, UnappliablePatchError(patch, baseBranch, stat.Output, checked.Output, moduleName)
}

// applyWhatFits applies every hunk that fits and leaves the rest as .rej
// files.
//
// It reports false when nothing fitted: a working copy with no change in it
// and a pile of rejects is not a head start on anything, and reporting it as a
// partial success would hide an ordinary unappliable patch behind a cheerier
// message.
//
// The tree is left dirty on purpose. This is the beginning of a re-roll, and
// the person doing it is about to edit these files.
func (d *DdevContrib) applyWhatFits(dir string, patch PatchApplication) (PatchPromotion, bool) {
	d.log("Applying what still fits, and leaving the rest as .rej files ...")

	result := d.gitCapture(dir, RejectApplyArgs(patch.LocalPath)...)
	applied := CleanlyApplied(result.Output)
	rejected := RejectedFiles(result.Output)

	if len(applied) == 0 {
		d.gitProbe(dir, "reset", "--hard")
		d.gitProbe(dir, "clean", "-fd")

		return PatchPromotion{}, false
	}

	return PartialPromotion(applied, rejected), true
}

// StartWork cuts the issue's work branch from a freshly fetched base, or
// resumes it when it already exists. The returned bool is whether it resumed.
func (d *DdevContrib) StartWork(
	environment Environment,
	branch IssueBranch,
	baseBranch string,
	refresh BaseRefresh,
) (bool, error) {
	dir := moduleWorkingCopy(environment.ProjectPath)

	status := InspectWorkingCopy(dir, d.runner)
	if status.IsDirty() {
		return false, fmt.Errorf(
			"cannot start work on %q: the module working copy has uncommitted changes:\n  %s\n"+
				"Commit or stash them first — starting here would mix them into the new branch",
			branch.Name, strings.Join(status.Describe(), "\n  "),
		)
	}

	// Existing work is resumed exactly as it stands. `checkout -B`, which the
	// disposable-branch paths use, would silently discard commits that may
	// exist nowhere else.
	if _, exists := d.gitProbe(dir, "rev-parse", "--verify", branch.Name); exists {
		if _, err := d.git(dir, "checkout", branch.Name); err != nil {
			return false, err
		}
		d.log(fmt.Sprintf("Resumed existing work branch %q.", branch.Name))

		return true, d.requireWorkingCopyBranch(environment.ProjectPath, environment.ModuleName, branch.Name)
	}

	// A branch already pushed but not yet local — the maintainer started this
	// on another machine, or in another environment for another core.
	if remote, found := d.gitProbe(
		dir, "ls-remote", "--exit-code", "--heads", "origin", branch.Name,
	); found && remote != "" {
		if _, err := d.git(dir, "fetch", "origin", branch.Name); err != nil {
			return false, err
		}
		if _, err := d.git(dir, "checkout", "-b", branch.Name, "FETCH_HEAD"); err != nil {
			return false, err
		}
		d.log(fmt.Sprintf("Resumed work branch %q from origin.", branch.Name))

		return true, d.requireWorkingCopyBranch(environment.ProjectPath, environment.ModuleName, branch.Name)
	}

	// The base defaults to whatever the working copy already sits on — the
	// same rule the apply paths resolve against, so a branch started here and
	// a contribution checked out here share an origin.
	base := baseBranch
	if base == "" {
		resolved, err := d.resolveBase(dir)
		if err != nil {
			return false, err
		}
		base = resolved
	}

	d.log(fmt.Sprintf("Starting work branch %q off %s ...", branch.Name, base))
	if _, err := d.git(dir, "checkout", base); err != nil {
		return false, err
	}
	cutPoint, err := d.refreshBase(dir, base, refresh)
	if err != nil {
		return false, err
	}
	if _, err := d.git(dir, "checkout", "-b", branch.Name, cutPoint); err != nil {
		return false, err
	}
	// Recorded so a later apply from this working copy knows what the base
	// was, exactly as those paths record it for each other.
	if _, err := d.git(dir, "config", "upkeep.base-branch", base); err != nil {
		return false, err
	}

	return false, d.requireWorkingCopyBranch(environment.ProjectPath, environment.ModuleName, branch.Name)
}

// PromotePatch applies a patch onto the issue's work branch and commits it
// under the patch author's attribution.
func (d *DdevContrib) PromotePatch(
	environment Environment,
	patch PatchApplication,
	branch IssueBranch,
	commitMessage string,
	refresh BaseRefresh,
	allowPartial bool,
) (PatchPromotion, error) {
	dir := moduleWorkingCopy(environment.ProjectPath)

	// StartWork owns the dirty-tree refusal and the resume-never-reset rule.
	// Promoting must not hold a second, subtly different copy of either: the
	// branch this lands on can be the only place the work exists.
	if _, err := d.StartWork(environment, branch, "", refresh); err != nil {
		return PatchPromotion{}, err
	}

	d.log(fmt.Sprintf("Applying patch %q onto %s ...", patch.Name, branch.Name))

	// The work branch is what the patch is applied onto, so it is also what a
	// failure names and returns to — an unappliable patch leaves the branch
	// exactly as it was found.
	partial, err := d.applyPatchFile(dir, patch, branch.Name, environment.ModuleName, allowPartial)
	if err != nil {
		return PatchPromotion{}, err
	}

	// Deliberately uncommitted. The commit carries the patch author's
	// attribution, and half their patch plus a pile of rejects is not what
	// they wrote — committing it would put their name on it.
	if len(partial.Applied) > 0 || len(partial.Rejected) > 0 {
		d.log(fmt.Sprintf(
			"Applied %d file(s) onto %s; %d left rejected.",
			len(partial.Applied), branch.Name, len(partial.Rejected),
		))

		return partial, nil
	}

	if _, err := d.git(dir,
		"-c", "user.name=upkeep",
		"-c", "user.email=upkeep@localhost",
		"commit", "--no-verify", "-m", commitMessage,
	); err != nil {
		return PatchPromotion{}, err
	}

	sha, err := d.git(dir, "rev-parse", "HEAD")
	if err != nil {
		return PatchPromotion{}, err
	}
	sha = strings.TrimSpace(sha)

	d.log(fmt.Sprintf("Committed onto %s at %s.", branch.Name, shortSHA(sha)))

	return CommittedPromotion(sha, nil), nil
}

// PushWork pushes the work branch to the given remote and returns the commit
// it pushed.
func (d *DdevContrib) PushWork(
	environment Environment,
	branch IssueBranch,
	remote GitRemote,
) (string, error) {
	dir := moduleWorkingCopy(environment.ProjectPath)

	status := InspectWorkingCopy(dir, d.runner)
	if status.IsDirty() {
		return "", fmt.Errorf(
			"cannot publish %q: the module working copy has uncommitted changes:\n  %s\n"+
				"Commit them first — what is not committed cannot be pushed",
			branch.Name, strings.Join(status.Describe(), "\n  "),
		)
	}

	head, onABranch := d.gitProbe(dir, "symbolic-ref", "--short", "HEAD")
	if !onABranch || head != branch.Name {
		where := head
		if !onABranch || where == "" {
			where = "a detached HEAD"
		}

		return "", fmt.Errorf(
			"the module working copy is on %q, not %q. Publishing would push a branch you are not looking "+
				"at; switch to it first",
			where, branch.Name,
		)
	}

	if err := d.ensureRemote(dir, remote); err != nil {
		return "", err
	}

	// No --force, and no lease: a rejected push means the remote moved, which
	// is a thing to look at rather than to overwrite.
	d.log(fmt.Sprintf("Pushing %s to %s ...", branch.Name, remote.URL))
	push := d.gitCapture(dir, "push", "--set-upstream", remote.Name, branch.Name)
	if push.ExitCode == nil || *push.ExitCode != 0 {
		return "", fmt.Errorf("%s", ExplainPushRefusal(branch, remote, push.Output))
	}

	sha, err := d.git(dir, "rev-parse", "HEAD")

	return strings.TrimSpace(sha), err
}

// RecordedBaseBranch is the base a previous apply recorded, or "" when none
// was.
func (d *DdevContrib) RecordedBaseBranch(environment Environment) string {
	recorded, _ := d.gitProbe(
		moduleWorkingCopy(environment.ProjectPath), "config", "--get", "upkeep.base-branch",
	)

	return recorded
}

// CheckoutBranch puts the module working copy on a branch.
func (d *DdevContrib) CheckoutBranch(environment Environment, branch string) error {
	dir := moduleWorkingCopy(environment.ProjectPath)

	status := InspectWorkingCopy(dir, d.runner)
	if status.IsDirty() {
		return fmt.Errorf(
			"cannot switch to %q: the module working copy has uncommitted changes:\n  %s\n"+
				"Commit or stash them first",
			branch, strings.Join(status.Describe(), "\n  "),
		)
	}

	if _, err := d.git(dir, "checkout", branch); err != nil {
		return err
	}

	return d.requireWorkingCopyBranch(environment.ProjectPath, environment.ModuleName, branch)
}

// resolveBase reads the base the working copy implies.
func (d *DdevContrib) resolveBase(dir string) (string, error) {
	currentBranch, _ := d.gitProbe(dir, "symbolic-ref", "--short", "HEAD")
	recordedBase, _ := d.gitProbe(dir, "config", "--get", "upkeep.base-branch")

	return ResolveBaseBranch(currentBranch, recordedBase)
}

// resolveMergeRequestRef decides which of the merge request's refs to check
// out.
//
// Asked rather than assumed, because the two answers mean different things.
// /merge is the branch merged into the current target tip and is what CI
// analyses; /head is the branch alone. GitLab publishes no merge ref for a
// merge request that conflicts with its target, and falling back silently
// would report a branch-only verdict as though it were CI's.
//
// One extra ls-remote per apply, which is a single lightweight round trip
// against a command that is about to provision an environment.
func (d *DdevContrib) resolveMergeRequestRef(dir string, iid int) (string, error) {
	advertised, err := d.git(dir, "ls-remote", "origin", MergeRef(iid), HeadRef(iid))
	if err != nil {
		return "", err
	}

	refs := []string{}
	for _, line := range strings.Split(advertised, "\n") {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) == 2 {
			refs = append(refs, parts[1])
		}
	}

	ref, found := PreferredRef(refs, iid)
	if !found {
		return "", ErrNoRefsAtAll(iid)
	}

	if ref == HeadRef(iid) {
		d.log(NoMergeRefWarning(iid))
	} else {
		d.log(fmt.Sprintf(
			"Checking MR !%d as CI does: the branch merged into the current tip of its target.", iid,
		))
	}

	return ref, nil
}

// refreshBase brings the base up to date and returns what to cut the new
// branch from.
//
// The working copy is cloned once and, before this, was never fetched again on
// any path that cuts a branch — so its 2.0.x stayed frozen at the day of the
// clone while drupal.org's moved on. A patch applied to that is checked
// against months-old code, and CI, which checks your work merged into the
// *current* tip, is checking something else entirely.
//
// The local base is fast-forwarded when it can be, so the working copy a
// maintainer looks at afterwards is not still behind. It is never reset: a
// base carrying local commits is left exactly as it is and said so, because
// discarding somebody's unpushed work to make a check tidy is not a trade
// upkeep gets to make.
func (d *DdevContrib) refreshBase(dir, baseBranch string, refresh BaseRefresh) (string, error) {
	if refresh == RefreshSkip {
		d.log(SkippedBaseUpdate(baseBranch))

		return CutPoint(baseBranch, refresh), nil
	}

	fetch := d.runner.Capture([]string{"git", "-C", dir, "fetch", "origin", baseBranch}, "", 0)
	if fetch.ExitCode == nil || *fetch.ExitCode != 0 {
		return "", UnreachableBaseError(baseBranch, fetch.Output)
	}

	counted, err := d.git(dir, "rev-list", "--count", "HEAD..FETCH_HEAD")
	if err != nil {
		return "", err
	}
	behind, _ := strconv.Atoi(strings.TrimSpace(counted))

	if behind > 0 {
		d.log(DescribeBaseUpdate(baseBranch, behind))
		// Fast-forward only. A base that will not fast-forward has local
		// commits on it, and those are somebody's.
		if _, fastForwarded := d.gitProbe(dir, "merge", "--ff-only", "FETCH_HEAD"); !fastForwarded {
			d.log(DivergedBase(baseBranch))
		}
	}

	return CutPoint(baseBranch, refresh), nil
}

// ensureRemote makes the remote exist and point where it should, adding or
// re-pointing it as needed.
//
// The URL is GitLab's own ssh_url_to_repo, never assembled here:
// git.drupalcode.org serves HTTPS but advertises SSH on git.drupal.org, and a
// URL built from the host you cloned from does not answer. Origin is untouched
// — fetch stays anonymous over HTTPS, so checking a merge request or a patch
// still needs no key at all.
func (d *DdevContrib) ensureRemote(dir string, remote GitRemote) error {
	current, exists := d.gitProbe(dir, "remote", "get-url", "--push", remote.Name)

	if !exists {
		d.log(fmt.Sprintf("Adding remote %s -> %s", remote.Name, remote.URL))
		_, err := d.git(dir, "remote", "add", remote.Name, remote.URL)

		return err
	}

	if current != remote.URL {
		d.log(fmt.Sprintf("Re-pointing remote %s -> %s", remote.Name, remote.URL))
		_, err := d.git(dir, "remote", "set-url", remote.Name, remote.URL)

		return err
	}

	return nil
}

func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}

	return sha[:8]
}
