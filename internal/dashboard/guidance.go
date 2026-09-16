package dashboard

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// Guidance is what a row *is*, in a phrase, and what to run about it.
//
// The dashboard used to render the fast-lane gate's own vocabulary —
// "REVIEW not-bot-author, ci-missing, local-missing" — which is precise,
// machine-readable, and answers a question nobody asked. A maintainer looking
// at a hundred rows needs to know which one to touch and what to type; the
// reason tokens tell them neither, and not-bot-author appears on every
// human-authored merge request, so the ordinary case reads as a defect.
//
// So the tokens move behind -v (anything scripted against them still has them)
// and every row carries two human things instead: a status phrase, and the
// literal command.
type Guidance struct {
	Status string
	// Command is never empty. Every row has something worth running,
	// including the ones that briefly did not: a red pipeline is when you most
	// want the branch locally, and a draft is often work somebody started and
	// could not finish, which is a thing to pick up rather than to wait on.
	Command string
}

// GuidanceFor decides what one row says and what to run about it.
//
// **The order the reasons are considered is the design.** A row usually has
// several, and only one can be shown, so they are ranked by what actually
// blocks progress: something upkeep cannot fix, then evidence the work is
// wrong, then evidence that is missing, then nothing at all to do. The phrase
// a maintainer sees is therefore the most actionable true thing about the row,
// not the first one the gate happened to record.
func GuidanceFor(row Row) Guidance {
	if row.ModuleFailure != nil {
		return Guidance{
			Status:  "unavailable",
			Command: fmt.Sprintf("upkeep dashboard --refresh=%s", row.Module),
		}
	}

	// An empty merge request is not work to check. Applying it changes
	// nothing, so the checks would run against the branch as it already stands
	// and the verdict would be reported as though it were about the
	// contribution. Reported from a real dashboard, where a row showed
	// "!1 empty" and "1" patch side by side and then said to check the merge
	// request.
	//
	// Explicitly No, never a truthiness test: emptiness is a tri-state, and
	// unknown means the list endpoint did not carry diff_refs — which no
	// caller may read as empty.
	if row.MergeRequest != nil && row.MergeRequest.CarriesChanges() == gitlab.No {
		return forEmptyMergeRequest(row)
	}

	if row.MergeRequest != nil {
		return forMergeRequest(row)
	}

	return forDormantIssue(row)
}

// forEmptyMergeRequest handles a merge request that carries nothing.
//
// With a patch beside it this is the shape the whole patch surface exists for
// — the row looks covered and is not — so the patch is the work and
// forDormantIssue already says all of that. With no patch there is simply
// nothing to check yet, and the honest move is to go and look at the issue
// rather than to name a command that would do nothing.
func forEmptyMergeRequest(row Row) Guidance {
	if row.PatchCount > 0 {
		return forDormantIssue(row)
	}

	return Guidance{
		Status:  "empty MR, nothing to check",
		Command: fmt.Sprintf("upkeep issue %s %d", row.Module, row.MergeRequest.IID),
	}
}

// forMergeRequest ranks a merge request by what stands between it and being
// merged.
func forMergeRequest(row Row) Guidance {
	reasons := []string{}
	if row.Verdict != nil {
		reasons = row.Verdict.Reasons
	}
	iid := row.MergeRequest.IID

	if row.Verdict != nil && row.Verdict.Status == gate.ReadyAuto {
		return Guidance{Status: "ready to merge", Command: "upkeep merge --fast-lane"}
	}

	// Evidence the work is wrong, and it is *your* evidence — worth telling
	// the contributor about, which is what needs-work does.
	if failed := failedChecks(reasons); len(failed) > 0 {
		status := fmt.Sprintf("%d checks failed", len(failed))
		if len(failed) == 1 {
			status = failed[0] + " failed"
		}

		return Guidance{
			Status:  status,
			Command: fmt.Sprintf("upkeep needs-work %s %d", row.Module, iid),
		}
	}

	// A landing outranks everything else this row could say. An open issue
	// whose work is already merged is the one thing a maintainer cannot read
	// off the row at all, and it is exactly what a promoted-and-merged patch
	// leaves behind: the bot's draft still sitting there, looking like the
	// only contribution on the issue.
	if landing, found := landingGuidance(row, checkCommand(row, iid)); found {
		return landing
	}

	// Red CI and draft are *modifiers*: they change what the row is, not what
	// to do about it. Both spent a while suggesting nothing, and both were
	// wrong to. A red pipeline is when you most want the branch on your own
	// machine to reproduce the failure; a draft is frequently something a
	// contributor started and could not finish, which is a thing to pick up
	// rather than to wait on.
	//
	// Blocked is the same condition as ci-red: the gate sets it from red CI
	// and from nothing else, never inspecting mergeability.
	ciRed := slices.Contains(reasons, "ci-red") ||
		(row.Verdict != nil && row.Verdict.Status == gate.Blocked)
	prefix := ""
	if slices.Contains(reasons, "draft") {
		prefix = "draft, "
	}

	// What to *do* is decided by the evidence you hold, independently of
	// either: nothing yet, or something about an older revision, means run the
	// checks; anything else means the change itself is what is left to look
	// at.
	if needsChecking(reasons) {
		status := "needs a check"
		switch {
		case ciRed:
			status = "CI failed"
		case slices.Contains(reasons, "local-stale"):
			status = "checks are stale"
		}

		return Guidance{Status: prefix + status, Command: checkCommand(row, iid)}
	}

	// Checked and green. Either the fast lane will never take it because it is
	// not a bot merge request, or your checks disagree with drupal.org's — and
	// a disagreement is exactly a thing to go and look at.
	status := "needs your review"
	if ciRed {
		status = "CI failed, local green"
	}

	return Guidance{
		Status:  prefix + status,
		Command: fmt.Sprintf("upkeep review %s %d", row.Module, iid),
	}
}

// forDormantIssue handles an issue with nothing of its own open: patches
// waiting, work already landed, or both.
//
// There is no gate here — nothing about a patch is mergeable — so the ranking
// is what has already happened, then whether the patch has been checked.
func forDormantIssue(row Row) Guidance {
	nid := row.IssueNid
	next := patchCheckCommand(row, nid)

	if landing, found := landingGuidance(row, next); found {
		return landing
	}

	status := fmt.Sprintf("%d patches", row.PatchCount)
	if row.PatchCount == 1 {
		status = "1 patch"
	}

	// An empty merge request beside a patch is the thing worth flagging: the
	// row looks covered and is not.
	if strings.Contains(row.MergeRequestCell(), "empty") {
		status += ", empty MR"
	}

	// Green on every applicable core, and stricter than it reads: a patch
	// checked on 11 and never checked on 10 is not "checked".
	if row.Local.AllGreen() {
		return Guidance{
			Status:  status + ", checked",
			Command: fmt.Sprintf("upkeep patch:apply %s %d", row.Module, nid),
		}
	}

	// A failing patch is work to pick up rather than a verdict to deliver:
	// start opens a branch on the issue, which is what a maintainer does next
	// with a patch that does not hold up.
	if row.Local.AnyFailed() {
		return Guidance{
			Status:  status + ", failed",
			Command: fmt.Sprintf("upkeep start %s %d", row.Module, nid),
		}
	}

	if row.Local.AnyStale() {
		return Guidance{Status: status + ", stale check", Command: next}
	}

	return Guidance{Status: status, Command: next}
}

// landingGuidance is the landing phrase, shared by both row shapes.
//
// ifNewerWork is what to run when the landing is not the last word — a bot
// that posts again after its work merged has raised new work to check.
func landingGuidance(row Row, ifNewerWork string) (Guidance, bool) {
	if row.Landed == nil {
		return Guidance{}, false
	}

	merged := row.Landed.MergedAt
	if len(merged) > 10 {
		merged = merged[:10]
	}

	if row.NewerWorkSinceLanding {
		return Guidance{
			Status:  fmt.Sprintf("merged %s, newer work since", merged),
			Command: ifNewerWork,
		}, true
	}

	return Guidance{
		Status:  "merged " + merged,
		Command: fmt.Sprintf("upkeep issue %s %d", row.Module, row.Landed.IID),
	}, true
}

// checkCommand and patchCheckCommand carry --version only when a core needs
// attention: a row whose evidence is green everywhere has nothing to re-run,
// and the core named is the one the LOCAL cell named, so the table and the
// command cannot disagree about which core is the problem.
func checkCommand(row Row, iid int) string {
	return "upkeep check " + row.Module + " " + strconv.Itoa(iid) + coreSuffix(row)
}

func patchCheckCommand(row Row, nid int) string {
	return "upkeep patch:check " + row.Module + " " + strconv.Itoa(nid) + coreSuffix(row)
}

func coreSuffix(row Row) string {
	if core := row.Local.AttentionCore(); core != "" {
		return " --version=" + core
	}

	return ""
}

// failedChecks is the named checks that failed, from local-failed:<check>
// reasons.
func failedChecks(reasons []string) []string {
	failed := []string{}
	for _, reason := range reasons {
		if name, isFailure := strings.CutPrefix(reason, "local-failed:"); isFailure {
			failed = append(failed, name)
		}
	}

	return failed
}

func needsChecking(reasons []string) bool {
	return slices.Contains(reasons, "local-missing") || slices.Contains(reasons, "local-stale")
}
