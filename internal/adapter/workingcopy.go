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
	mrBranch      = regexp.MustCompile(`^mr-\d+$`)
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
// not a base branch like "1.0.x" or "2.x", and not an upkeep-managed "mr-*"
// one.
func (s WorkingCopyStatus) IsOnCustomBranch() bool {
	if s.CurrentBranch == "" {
		return true
	}
	if mrBranch.MatchString(s.CurrentBranch) {
		return false
	}

	return !releaseBranch.MatchString(s.CurrentBranch) && !majorBranch.MatchString(s.CurrentBranch)
}

// HasLocalWork is any signal that suggests local work: dirty files, unpushed
// commits, or a developer branch.
func (s WorkingCopyStatus) HasLocalWork() bool {
	return s.IsDirty() || s.CommitsAhead > 0 || s.CommitsAhead == NoUpstream || s.IsOnCustomBranch()
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
	if s.CommitsAhead == NoUpstream {
		reasons = append(reasons, "No upstream tracking branch configured")
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
