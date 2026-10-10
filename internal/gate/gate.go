// Package gate decides whether one dashboard row is eligible for the
// one-keypress human-approved merge.
package gate

import (
	"strings"

	"github.com/owenbush/upkeep/internal/config"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
)

// Status is one of the three derived row statuses on the dashboard. Only
// ReadyAuto rows are eligible for the fast-lane merge; everything else needs a
// human.
type Status string

const (
	ReadyAuto Status = "READY-AUTO"
	Review    Status = "REVIEW"
	Blocked   Status = "BLOCKED"
)

// Verdict is the outcome of one gate classification: a status plus every
// machine-readable reason that denied READY-AUTO (empty exactly when the row
// is READY-AUTO).
//
// All deny reasons are collected, not just the first: a draft bot merge
// request with no pipeline is denied by both facts independently, and
// consumers — the dashboard, the merge review — should see the complete
// picture.
type Verdict struct {
	Status Status
	// Reasons are machine-readable tokens, e.g. "ci-red",
	// "local-failed:phpunit".
	Reasons []string
}

// Describe is the rendered form for the dashboard's STATUS column:
// "REVIEW draft, ci-missing".
func (v Verdict) Describe() string {
	if len(v.Reasons) == 0 {
		return string(v.Status)
	}

	return string(v.Status) + " " + strings.Join(v.Reasons, ", ")
}

// FastLane is the gate classifier.
//
// Deliberately conservative pure logic with no I/O: READY-AUTO requires EVERY
// condition to hold, on EVERY core the row applies to; any unknown — a missing
// pipeline, missing or stale local results — denies. A misclassification here
// merges the wrong thing, so this stays standalone and exhaustively tested.
type FastLane struct {
	// patternForCore yields the bot pattern to gate against for one target
	// core version. Nil takes config.BotPatternForCore, the one config point
	// for per-core pattern differences.
	patternForCore func(string) config.BotPattern
}

// NewFastLane builds the gate. A nil patternForCore takes the default.
func NewFastLane(patternForCore func(string) config.BotPattern) *FastLane {
	if patternForCore == nil {
		patternForCore = config.BotPatternForCore
	}

	return &FastLane{patternForCore: patternForCore}
}

// Classify decides one row.
//
// cores are the cores that apply to this row — every one of them must be green
// for READY-AUTO. local is what is known across those cores.
func (g *FastLane) Classify(mr gitlab.MergeRequest, cores []string, local results.LocalEvidence) Verdict {
	reasons := []string{}
	blocked := false

	// The bot pattern is asked per core because BotPatternForCore is the one
	// place a future per-core bot account or branch would land. Every
	// applicable core must agree, and no cores at all is not agreement — a row
	// with nothing to test on is not a row to merge.
	if len(cores) == 0 || !g.matchesEveryCore(cores, mr) {
		reasons = append(reasons, "not-bot-author")
	}

	// A merge request that carries no changes is not mergeable work, whatever
	// else is green about it. This is reachable, and was: checking an empty
	// merge request applies nothing, so the suite runs against the base branch
	// and passes, and green local plus green CI plus a bot author is
	// READY-AUTO — the fast lane offering to merge a branch identical to its
	// target. Denied here rather than left to the prompt, because the gate is
	// what the fast lane reads.
	//
	// Explicitly No only: unknown means the payload could not settle it, which
	// is not permission to assume.
	if mr.CarriesChanges() == gitlab.No {
		reasons = append(reasons, "no-changes")
	}

	// Draft is signalled two ways by the API (the draft flag and
	// detailed_merge_status "draft_status"); either alone denies.
	if mr.IsDraft() {
		reasons = append(reasons, "draft")
	}

	switch pipeline := mr.HeadPipeline; {
	case pipeline == nil:
		reasons = append(reasons, "ci-missing")
	case pipeline.Status == gitlab.StatusFailed:
		reasons = append(reasons, "ci-red")
		blocked = true
	case !pipeline.Status.IsGreen():
		raw := pipeline.RawStatus
		if raw == "" {
			raw = string(pipeline.Status)
		}
		reasons = append(reasons, "ci-not-green:"+raw)
	}

	// Local evidence must exist for *every* applicable core AND be for the
	// merge request's current revision. This is where the row model changed
	// the answer: a merge request green on 11 and unchecked on 10 used to be
	// two rows, one of them READY-AUTO, and the fast lane took the ready one —
	// merging on evidence that covered half the cores, with nothing saying so.
	// One row cannot hide it.
	//
	// Covers is the part the PHP side leaves to convention: it asks whether
	// the evidence describes the same cores the row applies to, rather than
	// trusting the caller to have built both from one list.
	if len(local.Cores()) == 0 || !local.Covers(cores) || local.AnyUnchecked() {
		reasons = append(reasons, "local-missing")
	}
	if local.AnyStale() {
		reasons = append(reasons, "local-stale")
	}
	for _, failed := range local.FailedChecks() {
		reasons = append(reasons, "local-failed:"+failed)
	}

	if len(reasons) == 0 {
		return Verdict{Status: ReadyAuto}
	}
	if blocked {
		return Verdict{Status: Blocked, Reasons: reasons}
	}

	return Verdict{Status: Review, Reasons: reasons}
}

func (g *FastLane) matchesEveryCore(cores []string, mr gitlab.MergeRequest) bool {
	for _, core := range cores {
		if !g.patternForCore(core).Matches(mr.AuthorUsername, mr.AuthorID, mr.SourceBranch) {
			return false
		}
	}

	return true
}
