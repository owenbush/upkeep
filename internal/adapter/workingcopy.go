package adapter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/proc"
)

// NoUpstream is the commits-ahead value when no upstream is configured.
//
// Deliberately distinct from zero: "nothing to push" and "nothing knows where
// this would push to" are different states, and the second is a reason to
// treat the working copy as carrying local work.
const NoUpstream = -1

// gitStatusTimeout bounds the probes. They are three local git commands; a
// minute is generous and still bounds a wedged filesystem.
const gitStatusTimeout = time.Minute

var (
	releaseBranch = regexp.MustCompile(`^\d+\.\d+\.x$`)
	majorBranch   = regexp.MustCompile(`^\d+\.x$`)
	// legacyContribBranch is drupal.org's pre-semver convention — "8.x-1.x",
	// "7.x-2.x" — which a great many contributed modules are still on,
	// pathauto and token among them.
	legacyContribBranch = regexp.MustCompile(`^\d+\.x-\d+\.x$`)
)

// WorkingCopyStatus is a snapshot of the git state of a module working copy:
// uncommitted changes, untracked files, unpushed commits, and current branch.
//
// Used by guards on destructive operations — stale teardown, prune, applying a
// merge request — to refuse when local work would be lost.
type WorkingCopyStatus struct {
	HasStagedChanges   bool
	HasUnstagedChanges bool
	HasUntrackedFiles  bool
	// CommitsAhead is NoUpstream when no upstream is configured.
	CommitsAhead int
	// CurrentBranch is "" when HEAD is detached.
	CurrentBranch string
}

// IsDirty is the hard signals: uncommitted changes or untracked files that
// would be destroyed by a checkout or a teardown.
func (s WorkingCopyStatus) IsDirty() bool {
	return s.HasStagedChanges || s.HasUnstagedChanges || s.HasUntrackedFiles
}

// IsOnCustomBranch reports whether the working copy is on a developer branch —
// not a base branch like "1.0.x", "2.x" or "8.x-1.x", and not an
// upkeep-managed one.
//
// **It asks adapter.IsManagedBranch rather than matching "mr-" itself**, which
// it did, and which left "patch-<nid>" reading as somebody's own branch even
// though upkeep creates it, resets it with `checkout -B` on every apply and
// refuses it as a base. Two definitions of "a branch upkeep made" in one
// package is how the second one gets forgotten; there is now one.
//
// **The legacy contrib shape counts, and that is a change from the port.**
// Only "2.0.x" and "2.x" were recognised, so "8.x-1.x" — the convention
// pathauto, token and a great many other modules are still on — read as
// somebody's own branch. HasLocalWork was then permanently true for those
// modules and every guard keyed on it refused: the stale-environment
// teardown, prune, and the dirty-copy check before an apply. The tool
// declined to work on exactly the modules it is most often pointed at.
//
// The port knew and left it alone on purpose — the test recorded it as
// "faithful to the PHP… a decision to take deliberately", because
// recognising the shape loosens a guard on destructive operations. That
// decision has since been taken on the PHP side, which fixed it with the
// reasoning that the other three local-work signals still catch anything
// genuinely unsaved. So this is adopting a decision, not discovering a bug,
// and it is adopted here as the PHP implementation is removed — the last
// moment the two could disagree.
func (s WorkingCopyStatus) IsOnCustomBranch() bool {
	if s.CurrentBranch == "" {
		return true
	}
	if IsManagedBranch(s.CurrentBranch) {
		return false
	}

	return !releaseBranch.MatchString(s.CurrentBranch) &&
		!majorBranch.MatchString(s.CurrentBranch) &&
		!legacyContribBranch.MatchString(s.CurrentBranch)
}

// HasLocalWork is any signal that suggests local work: dirty files, unpushed
// commits, or a developer branch.
//
// **A missing upstream does not count on a branch upkeep manages.** It cannot
// have one: `mr-<iid>` and `patch-<nid>` are cut locally with `checkout -B`
// and nothing ever pushes them. So the no-upstream signal was true for every
// working copy that had been through a `check`, `review`, `patch:apply` or
// `patch:check` — which is every working copy, after the first one — and every
// guard keyed on this refused from then on. Reported from the wild twice: a
// stale environment that could not be re-provisioned, which is the recovery
// seed skew exists to trigger, so a base-artifact rebuild left the tool
// wedged until somebody switched branch by hand.
//
// It is sound as well as necessary. The managed branches are already destroyed
// on the ordinary path, by the `checkout -B` at the start of the next apply,
// with no guard at all — so refusing a teardown over one protected nothing
// while blocking the only way forward. IsDirty still catches uncommitted
// changes there, which is the signal that means somebody is mid-edit.
//
// On any other branch it still counts, and deliberately: without an upstream
// there is no way to ask how many commits are unpushed, so "no upstream" means
// "cannot tell" rather than "nothing to lose".
func (s WorkingCopyStatus) HasLocalWork() bool {
	return s.IsDirty() || s.CommitsAhead > 0 || s.missingUpstreamCounts() || s.IsOnCustomBranch()
}

// missingUpstreamCounts reports whether this working copy's lack of an
// upstream is a reason to treat it as carrying work.
func (s WorkingCopyStatus) missingUpstreamCounts() bool {
	return s.CommitsAhead == NoUpstream && !IsManagedBranch(s.CurrentBranch)
}

// Describe is the human-readable list of reasons the working copy has local
// work.
func (s WorkingCopyStatus) Describe() []string {
	reasons := []string{}

	if s.HasStagedChanges {
		reasons = append(reasons, "Staged changes not yet committed")
	}
	if s.HasUnstagedChanges {
		reasons = append(reasons, "Unstaged changes to tracked files")
	}
	if s.HasUntrackedFiles {
		reasons = append(reasons, "Untracked files not in .gitignore")
	}
	if s.CommitsAhead > 0 {
		reasons = append(reasons, fmt.Sprintf("%d commit(s) ahead of origin (unpushed)", s.CommitsAhead))
	}
	if s.missingUpstreamCounts() {
		reasons = append(reasons, fmt.Sprintf(
			"Branch %q has no upstream, so unpushed commits cannot be counted",
			s.CurrentBranch,
		))
	}
	if s.IsOnCustomBranch() {
		if s.CurrentBranch == "" {
			reasons = append(reasons, "HEAD is detached")
		} else {
			reasons = append(reasons, fmt.Sprintf("On branch %q (not a base or MR branch)", s.CurrentBranch))
		}
	}

	return reasons
}

// Recovery is the commands that clear each reason Describe reported, so a
// refusal can say what to do rather than only what is wrong.
//
// Every other refusal in upkeep names the command that resolves it; these two
// said "Push or stash your work, then re-run", which names neither the
// directory the work is in nor what would move it. The working copy is nested
// inside an engine project directory nobody navigates to by hand, so without
// the path there is nothing to act on.
//
// **One `cd`, then bare git commands.** Repeating `git -C <path>` on every
// line was the first shape and it put four 120-column lines in a terminal,
// which is a wall rather than instructions. The path appears once, where it is
// read once.
//
// Tailored to the signals that actually fired, because a list of every
// possible remedy is a list the reader has to filter. The base branch is named
// as a placeholder rather than resolved: this type knows the branch it is on
// and not the one the module releases from, and asking git for it here would
// turn a formatting helper into another process call on a path that is already
// failing.
func (s WorkingCopyStatus) Recovery(moduleDir string) []string {
	steps := []string{"cd " + moduleDir}

	if s.IsDirty() {
		steps = append(steps,
			"git stash --include-untracked   # keep the changes for later",
			"git status                      # or look at them first",
		)
	}
	if s.CommitsAhead > 0 {
		steps = append(steps, "git push                        # the commits are only here")
	}
	if s.missingUpstreamCounts() {
		steps = append(steps,
			"git log --oneline origin/HEAD.. # see what is only here",
			"git push -u origin "+s.CurrentBranch+"  # if it is worth keeping",
		)
	}
	if s.IsOnCustomBranch() {
		steps = append(steps, "git switch <base-branch>        # e.g. 1.0.x, or 8.x-1.x")
	}

	return steps
}

// InspectWorkingCopy reads the git state of a module working copy directory.
func InspectWorkingCopy(moduleDir string, runner proc.Runner) WorkingCopyStatus {
	status := WorkingCopyStatus{CommitsAhead: NoUpstream}

	if porcelain, ok := runner.TryRun(
		[]string{"git", "-C", moduleDir, "status", "--porcelain"}, "", gitStatusTimeout,
	); ok {
		status.HasStagedChanges, status.HasUnstagedChanges, status.HasUntrackedFiles = readPorcelain(porcelain)
	}

	if branch, ok := runner.TryRun(
		[]string{"git", "-C", moduleDir, "symbolic-ref", "--short", "HEAD"}, "", gitStatusTimeout,
	); ok {
		status.CurrentBranch = strings.TrimSpace(branch)
	}

	if ahead, ok := runner.TryRun(
		[]string{"git", "-C", moduleDir, "rev-list", "--count", "@{upstream}..HEAD"}, "", gitStatusTimeout,
	); ok {
		// A non-numeric answer is not zero: it is git having said something
		// this does not understand, which is the same "cannot tell" as having
		// no upstream at all.
		if count, err := strconv.Atoi(strings.TrimSpace(ahead)); err == nil {
			status.CommitsAhead = count
		}
	}

	return status
}

// readPorcelain reads `git status --porcelain`'s two status columns.
//
// Column one is the index, column two the worktree. A "?" in the first is an
// untracked file and says nothing about either.
func readPorcelain(porcelain string) (staged, unstaged, untracked bool) {
	for _, line := range strings.Split(strings.TrimRight(porcelain, "\n"), "\n") {
		if line == "" {
			continue
		}

		index, worktree := byte(' '), byte(' ')
		if len(line) > 0 {
			index = line[0]
		}
		if len(line) > 1 {
			worktree = line[1]
		}

		if index == '?' {
			untracked = true

			continue
		}
		if index != ' ' {
			staged = true
		}
		if worktree != ' ' && worktree != '?' {
			unstaged = true
		}
	}

	return staged, unstaged, untracked
}
