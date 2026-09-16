package command

import "strings"

// Glossary is what every term upkeep prints actually means.
//
// The tool emits six overlapping vocabularies — gate statuses, gate reason
// tokens, CI states, local-check states, contribution kinds, drupal.org issue
// statuses — and some words appear in more than one with different meanings
// ("review" is both a gate verdict and an issue status). Until this existed, a
// maintainer who saw the patch arrow on a dashboard row could read the source
// or guess, and those were the options.
//
// So the definitions live in one place, as data, and `upkeep explain` prints
// them. Deliberately not next to the code that emits each term: the point is
// that a reader can find them all at once.
//
// The order is the order they are printed in, which groups them by where they
// appear rather than alphabetically — somebody looking at a dashboard row
// wants the dashboard words together.

// Definition is one term: what it means, and where upkeep prints it.
type Definition struct {
	Term    string
	Meaning string
	Where   string
}

// Glossary is every term, in print order.
var Glossary = []Definition{
	{
		Term: "ready to merge",
		Meaning: "A Project Update Bot compatibility MR with green CI and green local checks. " +
			"The only thing the fast lane will offer to merge.",
		Where: "dashboard STATUS",
	},
	{
		Term: "needs a check",
		Meaning: "Nobody has run the checks against this yet. Running them is what turns it into " +
			"evidence.",
		Where: "dashboard STATUS",
	},
	{
		Term: "checks are stale",
		Meaning: "The checks were run, but against an older revision — the branch has moved, or " +
			"the patch was re-rolled since. The verdict no longer describes what is there " +
			"now.",
		Where: "dashboard STATUS",
	},
	{
		Term: "needs your review",
		Meaning: "Checked, green, and not a bot MR — so the fast lane will never take it and a " +
			"human has to look at the change itself.",
		Where: "dashboard STATUS",
	},
	{
		Term: "CI failed",
		Meaning: "drupal.org's own pipeline is red. The row still points at `upkeep check`, " +
			"because a red pipeline is exactly when you want the branch on your own machine " +
			"to reproduce the failure.",
		Where: "dashboard STATUS",
	},
	{
		Term: "CI failed, local green",
		Meaning: "drupal.org's pipeline is red but your own checks pass against the current " +
			"head. The two disagree, which is itself the thing to go and look at — often a " +
			"difference in core version or toolchain rather than in the change.",
		Where: "dashboard STATUS",
	},
	{
		Term: "draft",
		Meaning: "Marked as a draft on GitLab. It prefixes the rest of the status rather than " +
			"replacing it, and the row is still checkable: unfinished is frequently " +
			"abandoned, and work somebody could not carry on is a thing to pick up rather " +
			"than to wait on.",
		Where: "dashboard STATUS",
	},
	{
		Term: "empty MR",
		Meaning: "A merge request whose branch holds no commits the target does not already " +
			"have. It exists, it can be linked from an issue, and it covers nothing — the " +
			"Project Update Bot leaves these on many projects. Any patch beside it is the " +
			"only work there is.",
		Where: "dashboard STATUS, patches MR column",
	},
	{
		Term: "promote",
		Meaning: "Turning a patch into a merge request: `upkeep patch:promote` applies the patch " +
			"onto the issue's work branch and commits it, crediting whoever posted it by " +
			"name, and `upkeep publish` opens the MR. Nothing is pushed by the first step — " +
			"putting somebody else's work on drupal.org under your account is a step a " +
			"human types.",
		Where: "patch:promote",
	},
	{
		Term: "Patch-author",
		Meaning: "A trailer on a promoted patch's commit naming the drupal.org account that " +
			"posted the file. There is deliberately no Co-authored-by: — that wants an " +
			"email address, drupal.org publishes none, and inventing one would be a guess " +
			"about somebody's identity written into permanent history.",
		Where: "patch:promote commit message",
	},
	{
		Term: "unclaimed",
		Meaning: "An open issue with no merge request and no patch. Nobody has started it — " +
			"which makes it the most actionable row on an issue list, not the least.",
		Where: "issues CONTRIBUTION",
	},
	{
		Term: "ISSUE",
		Meaning: "The drupal.org issue this row is about, and the status drupal.org has it in. " +
			"An en dash means a merge request that claims no issue — a fifth of them do.",
		Where: "dashboard column",
	},
	{
		Term: "VERSION",
		Meaning: "The module branch the row's work targets — 1.0.x, 8.x-1.x. Not a core version: " +
			"one branch supports several cores at once, which is why the cores live in " +
			"LOCAL instead.",
		Where: "dashboard column",
	},
	{
		Term: "PATCH",
		Meaning: "How many patch files the issue carries. An arrow beside the count means the " +
			"newest of them postdates the merge request.",
		Where: "dashboard column",
	},
	{
		Term: "↑",
		Meaning: "The issue carries a patch newer than the merge request's last update. Someone " +
			"posted a patch after the branch was last touched, so the branch may be behind " +
			"the issue.",
		Where: "dashboard PATCH",
	},
	{
		Term: "CI",
		Meaning: "drupal.org's pipeline for the branch: pass, fail, a raw pipeline state, or an " +
			"en dash when no pipeline has run. Patches never have one — drupal.org runs CI " +
			"on branches, not on attachments.",
		Where: "dashboard column",
	},
	{
		Term: "LOCAL",
		Meaning: "Your own cached check results, across every core the branch supports, with the " +
			"worst case winning and naming its core: \"pass 10,11\", \"fail 10\", \"pass 11 · ? " +
			"10\" for a core nobody has checked. An en dash means never checked at all. It " +
			"is what `upkeep check` and `upkeep patch:check` write, and -v lists every core " +
			"separately.",
		Where: "dashboard column",
	},
	{
		Term: "stale",
		Meaning: "Evidence recorded against a revision that is no longer current — an older MR " +
			"head, or a patch that has since been re-rolled. A stale pass is not a pass.",
		Where: "dashboard LOCAL",
	},
	{
		Term: "NEXT",
		Meaning: "The command to run for that row. Every row has one — red CI and draft describe " +
			"the row without changing what to do about it, since both are exactly when you " +
			"want the branch locally.",
		Where: "dashboard column",
	},
	{
		Term: "BRANCHES",
		Meaning: "The module branches that module's rows sit on. It named the tracked core " +
			"versions until those stopped being what a row is about.",
		Where: "dashboard overview column",
	},
	{
		Term: "PATCH ISSUES",
		Meaning: "How many *issues* on that module carry patch files. A row's own patch count is " +
			"a number of files, which is why this column does not say \"patches\".",
		Where: "dashboard overview column",
	},
	{
		Term: "CI FAILED",
		Meaning: "How many rows drupal.org's pipeline has failed. It is the gate's BLOCKED " +
			"verdict under its real name — that verdict is set by red CI and by nothing " +
			"else.",
		Where: "dashboard overview column",
	},
	{
		Term: "UNCHECKED",
		Meaning: "Rows with no local verdict, plus rows whose verdict is stale. The actionable " +
			"number on the overview.",
		Where: "dashboard overview column",
	},
	{
		Term:    "READY-AUTO",
		Meaning: "The gate verdict behind \"ready to merge\". Shown under -v.",
		Where:   "dashboard STATUS (-v)",
	},
	{
		Term: "REVIEW",
		Meaning: "The gate verdict meaning a human has to look. Shown under -v, with the reasons " +
			"that denied fast-lane eligibility.",
		Where: "dashboard STATUS (-v)",
	},
	{
		Term: "BLOCKED",
		Meaning: "The gate verdict set by red CI, and by nothing else — the gate does not " +
			"inspect mergeability, so this does not mean merge conflicts. Shown under -v, " +
			"and as \"CI failed\" without it.",
		Where: "dashboard STATUS (-v)",
	},
	{
		Term: "ci-red",
		Meaning: "A gate reason: drupal.org's pipeline for the head commit failed. It is also " +
			"the only thing that makes the gate verdict BLOCKED.",
		Where: "dashboard STATUS (-v)",
	},
	{
		Term: "not-bot-author",
		Meaning: "A gate reason: this is not a Project Update Bot compatibility MR, so it is not " +
			"fast-lane eligible. It appears on every human-authored MR and is not a " +
			"problem.",
		Where: "dashboard STATUS (-v)",
	},
	{
		Term: "local-missing",
		Meaning: "A gate reason: at least one core the branch supports has no cached check " +
			"result. Every applicable core must be green for the fast lane, so a merge " +
			"request checked on one core and not another is denied — it used to be admitted " +
			"on the half that was checked.",
		Where: "dashboard STATUS (-v)",
	},
	{
		Term: "local-stale",
		Meaning: "A gate reason: the cached result is for a different revision than the MR's " +
			"current head.",
		Where: "dashboard STATUS (-v)",
	},
	{
		Term: "no-changes",
		Meaning: "A gate reason: the merge request is empty — its branch is identical to the " +
			"branch it targets, so merging it would change nothing. Checking one applies " +
			"nothing either, which is why it has to be denied here: the checks pass against " +
			"the base branch and would otherwise read as a green contribution.",
		Where: "dashboard STATUS (-v)",
	},
	{
		Term:    "ci-missing",
		Meaning: "A gate reason: GitLab reports no pipeline for the head commit.",
		Where:   "dashboard STATUS (-v)",
	},
	{
		Term: ".rej",
		Meaning: "A file `patch:promote --partial` leaves beside one it could not change, " +
			"holding the hunks that did not fit. Resolving them by hand and deleting the " +
			".rej files is the re-roll. Nothing is committed until you do, because the " +
			"commit carries the patch author's name.",
		Where: "patch:promote --partial output",
	},
	{
		Term: "partial promotion",
		Meaning: "A patch that would not apply, promoted anyway: every hunk that still fits is " +
			"applied to the issue work branch and the rest is left as .rej files. On " +
			"`patch:promote` only — it is the one patch command whose branch upkeep never " +
			"resets, and rejects resolved on the disposable patch-<nid> branch would be " +
			"destroyed by the next apply. Exits 1 rather than 0 — the patch did not apply, " +
			"and treating it as success would publish half of somebody's work.",
		Where: "patch:promote --partial",
	},
	{
		Term: "active",
		Meaning: "A drupal.org issue status: open, and waiting on nobody in particular. Where " +
			"new work begins.",
		Where: "issues STATUS",
	},
	{
		Term: "review",
		Meaning: "A drupal.org issue status (Needs review): someone has submitted work and is " +
			"waiting on a maintainer. Not the same as the gate's REVIEW verdict.",
		Where: "issues STATUS",
	},
	{
		Term: "RTBC",
		Meaning: "A drupal.org issue status (Reviewed & tested by the community): someone else " +
			"has reviewed it and believes it is ready. Waiting on a maintainer.",
		Where: "issues STATUS",
	},
	{
		Term:    "needs work",
		Meaning: "A drupal.org issue status: reviewed and found wanting. Waiting on the contributor.",
		Where:   "issues STATUS",
	},
}

// SearchGlossary is the terms whose name or meaning matches a query, so a
// half-remembered word still finds its definition. An empty query is every
// term.
func SearchGlossary(query string) []Definition {
	needle := strings.ToLower(strings.TrimSpace(query))
	// Answered outright rather than left to the loop, where an empty needle
	// matches everything anyway. The two agree today; this one says which
	// answer is intended, and allocates nothing to give it.
	if needle == "" {
		return Glossary
	}

	var matched []Definition
	for _, definition := range Glossary {
		if strings.Contains(strings.ToLower(definition.Term), needle) ||
			strings.Contains(strings.ToLower(definition.Meaning), needle) {
			matched = append(matched, definition)
		}
	}

	return matched
}
