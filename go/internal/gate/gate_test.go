package gate

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/config"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
)

const head = "abc1234"

// botMr is the shape the fast lane exists for: the bot's branch, not a draft,
// green CI, and real changes.
func botMr() gitlab.MergeRequest {
	return gitlab.MergeRequest{
		IID:            42,
		State:          "opened",
		AuthorUsername: "Project-Update-Bot",
		AuthorID:       66574,
		SourceBranch:   "project-update-bot-only",
		DiffBaseSHA:    "base",
		DiffHeadSHA:    "head",
		HeadPipeline:   &gitlab.Pipeline{ID: 1, Status: gitlab.StatusSuccess, RawStatus: "success"},
	}
}

func greenOn(cores ...string) results.LocalEvidence {
	byCore := map[string]*results.CachedResult{}
	for _, core := range cores {
		byCore[core] = &results.CachedResult{
			SHA:        head,
			RecordedAt: time.Now(),
			Result:     check.RunResult{Results: []check.Result{{Type: check.PhpUnit, Status: check.Passed}}},
		}
	}

	return results.Evidence(byCore, head)
}

func classify(mr gitlab.MergeRequest, cores []string, local results.LocalEvidence) Verdict {
	return NewFastLane(nil).Classify(mr, cores, local)
}

func TestTheHappyPathIsReadyAuto(t *testing.T) {
	verdict := classify(botMr(), []string{"10", "11"}, greenOn("10", "11"))

	if verdict.Status != ReadyAuto {
		t.Fatalf("got %s %v", verdict.Status, verdict.Reasons)
	}
	if len(verdict.Reasons) != 0 {
		t.Errorf("a ready row carried reasons: %v", verdict.Reasons)
	}
	if verdict.Describe() != "READY-AUTO" {
		t.Errorf("describe %q", verdict.Describe())
	}
}

// Checking an empty merge request applies nothing, so the suite runs against
// the base branch and passes — green local plus green CI plus a bot author.
// The fast lane would have offered to merge a branch identical to its target.
func TestAnEmptyMergeRequestIsDeniedEvenWhenEverythingElseIsGreen(t *testing.T) {
	mr := botMr()
	mr.DiffBaseSHA, mr.DiffHeadSHA = "same", "same"

	verdict := classify(mr, []string{"11"}, greenOn("11"))

	if verdict.Status == ReadyAuto {
		t.Fatal("the fast lane offered to merge an empty merge request")
	}
	if !slices.Contains(verdict.Reasons, "no-changes") {
		t.Errorf("reasons %v do not say why", verdict.Reasons)
	}
}

// Unknown means the payload could not settle it, which is not permission to
// assume — and the list endpoint omits diff_refs entirely.
func TestUnsettledEmptinessIsNotTreatedAsEmpty(t *testing.T) {
	mr := botMr()
	mr.DiffBaseSHA, mr.DiffHeadSHA = "", ""

	verdict := classify(mr, []string{"11"}, greenOn("11"))

	if slices.Contains(verdict.Reasons, "no-changes") {
		t.Error("an unknown was read as empty")
	}
	if verdict.Status != ReadyAuto {
		t.Errorf("got %s %v", verdict.Status, verdict.Reasons)
	}
}

func TestEitherDraftSignalDenies(t *testing.T) {
	flagged := botMr()
	flagged.Draft = true

	status := botMr()
	status.DetailedMergeStatus = "draft_status"

	for name, mr := range map[string]gitlab.MergeRequest{"the flag": flagged, "detailed status": status} {
		verdict := classify(mr, []string{"11"}, greenOn("11"))
		if !slices.Contains(verdict.Reasons, "draft") {
			t.Errorf("%s: reasons %v", name, verdict.Reasons)
		}
	}
}

// Red CI is the one thing that sets BLOCKED, and nothing else does.
func TestRedCiBlocksAndEverythingElseReviews(t *testing.T) {
	red := botMr()
	red.HeadPipeline = &gitlab.Pipeline{Status: gitlab.StatusFailed, RawStatus: "failed"}
	if verdict := classify(red, []string{"11"}, greenOn("11")); verdict.Status != Blocked {
		t.Errorf("red CI gave %s", verdict.Status)
	}

	draft := botMr()
	draft.Draft = true
	if verdict := classify(draft, []string{"11"}, greenOn("11")); verdict.Status != Review {
		t.Errorf("a draft gave %s, want REVIEW — BLOCKED is set by red CI and nothing else", verdict.Status)
	}
}

// A pipeline that is neither green nor red must say which, because "running"
// and "manual" want different things from a maintainer.
func TestANonGreenPipelineNamesItsActualStatus(t *testing.T) {
	for _, raw := range []string{"running", "pending", "canceled", "skipped", "manual"} {
		mr := botMr()
		mr.HeadPipeline = &gitlab.Pipeline{Status: gitlab.PipelineStatusFrom(raw), RawStatus: raw}

		verdict := classify(mr, []string{"11"}, greenOn("11"))
		if verdict.Status != Review {
			t.Errorf("%s gave %s", raw, verdict.Status)
		}
		if !slices.Contains(verdict.Reasons, "ci-not-green:"+raw) {
			t.Errorf("%s: reasons %v", raw, verdict.Reasons)
		}
	}
}

// A status GitLab adds later is still reportable rather than silently unknown.
func TestAnUnrecognisedPipelineStatusIsQuotedAsTheServerSaidIt(t *testing.T) {
	mr := botMr()
	mr.HeadPipeline = &gitlab.Pipeline{Status: gitlab.StatusUnknown, RawStatus: "quantum_pending"}

	verdict := classify(mr, []string{"11"}, greenOn("11"))
	if !slices.Contains(verdict.Reasons, "ci-not-green:quantum_pending") {
		t.Errorf("reasons %v", verdict.Reasons)
	}
}

func TestAMissingPipelineDenies(t *testing.T) {
	mr := botMr()
	mr.HeadPipeline = nil

	verdict := classify(mr, []string{"11"}, greenOn("11"))
	if !slices.Contains(verdict.Reasons, "ci-missing") {
		t.Errorf("reasons %v", verdict.Reasons)
	}
	if verdict.Status == ReadyAuto {
		t.Error("a merge request with no CI at all was ready")
	}
}

// The row model changed this answer: a merge request green on 11 and unchecked
// on 10 used to be two rows, one of them ready, and the fast lane took it.
func TestACoreThatAppliesAndWasNeverCheckedDenies(t *testing.T) {
	verdict := classify(botMr(), []string{"10", "11"}, greenOn("11"))

	if verdict.Status == ReadyAuto {
		t.Fatal("merged on evidence covering half the cores")
	}
	if !slices.Contains(verdict.Reasons, "local-missing") {
		t.Errorf("reasons %v", verdict.Reasons)
	}
}

func TestStaleLocalEvidenceDenies(t *testing.T) {
	stale := results.Evidence(map[string]*results.CachedResult{
		"11": {SHA: "older11", RecordedAt: time.Now(), Result: check.RunResult{}},
	}, head)

	verdict := classify(botMr(), []string{"11"}, stale)
	if !slices.Contains(verdict.Reasons, "local-stale") {
		t.Errorf("reasons %v", verdict.Reasons)
	}
}

// Named rather than counted: "phpcs failed" is actionable in a way "1 check
// failed" is not.
func TestAFailedCheckIsNamedInTheReason(t *testing.T) {
	failing := results.Evidence(map[string]*results.CachedResult{
		"11": {SHA: head, RecordedAt: time.Now(), Result: check.RunResult{Results: []check.Result{
			{Type: check.PhpCs, Status: check.Failed},
			{Type: check.PhpStan, Status: check.Failed},
		}}},
	}, head)

	verdict := classify(botMr(), []string{"11"}, failing)
	for _, want := range []string{"local-failed:phpcs", "local-failed:phpstan"} {
		if !slices.Contains(verdict.Reasons, want) {
			t.Errorf("reasons %v missing %q", verdict.Reasons, want)
		}
	}
}

// A push from any other branch — even under the bot account — does not
// qualify.
func TestAnyoneElsesMergeRequestIsNotFastLaneWork(t *testing.T) {
	human := botMr()
	human.AuthorUsername = "owenbush"
	human.AuthorID = 12345
	human.SourceBranch = "3603341-fix-something"

	verdict := classify(human, []string{"11"}, greenOn("11"))
	if !slices.Contains(verdict.Reasons, "not-bot-author") {
		t.Errorf("reasons %v", verdict.Reasons)
	}

	wrongBranch := botMr()
	wrongBranch.SourceBranch = "3603341-fix-something"
	if !slices.Contains(classify(wrongBranch, []string{"11"}, greenOn("11")).Reasons, "not-bot-author") {
		t.Error("the bot account from another branch qualified")
	}
}

// A row with nothing to test on is not a row to merge.
func TestNoApplicableCoresIsNotAgreement(t *testing.T) {
	verdict := classify(botMr(), []string{}, results.NoEvidence())

	if verdict.Status == ReadyAuto {
		t.Fatal("a row applying to no core was ready")
	}
	if !slices.Contains(verdict.Reasons, "not-bot-author") {
		t.Errorf("reasons %v", verdict.Reasons)
	}
}

// Every applicable core must agree, so the one config point for a future
// per-core bot account is exercised per core.
func TestThePatternIsAskedForEveryApplicableCore(t *testing.T) {
	asked := []string{}
	gate := NewFastLane(func(core string) config.BotPattern {
		asked = append(asked, core)
		if core == "13" {
			// A core whose bot has not been onboarded yet.
			return config.BotPattern{AuthorUsername: "nobody", SourceBranch: "nowhere"}
		}

		return config.BotPatternForCore(core)
	})

	verdict := gate.Classify(botMr(), []string{"11", "13"}, greenOn("11", "13"))
	if !slices.Contains(verdict.Reasons, "not-bot-author") {
		t.Errorf("one core disagreeing did not deny: %v", verdict.Reasons)
	}
	if len(asked) == 0 || asked[0] != "11" {
		t.Errorf("asked %v", asked)
	}
}

// A draft bot merge request with no pipeline is denied by both facts
// independently, and consumers should see the complete picture.
func TestEveryDenyReasonIsCollectedNotJustTheFirst(t *testing.T) {
	mr := botMr()
	mr.Draft = true
	mr.HeadPipeline = nil
	mr.AuthorUsername = "somebody"
	mr.AuthorID = 1
	mr.DiffBaseSHA, mr.DiffHeadSHA = "same", "same"

	verdict := classify(mr, []string{"11"}, results.NoEvidence())

	for _, want := range []string{"not-bot-author", "no-changes", "draft", "ci-missing", "local-missing"} {
		if !slices.Contains(verdict.Reasons, want) {
			t.Errorf("reasons %v missing %q", verdict.Reasons, want)
		}
	}
	if !strings.HasPrefix(verdict.Describe(), "REVIEW ") {
		t.Errorf("describe %q", verdict.Describe())
	}
	if !strings.Contains(verdict.Describe(), ", ") {
		t.Errorf("describe %q does not separate the reasons", verdict.Describe())
	}
}

// The gate is handed the applicable cores and the evidence as two arguments,
// and nothing in either says they describe the same set.
//
// The PHP side builds both from one list in the row factory, so they agree by
// construction and the gap is latent rather than live. Latent is still worth
// closing: what it would produce is precisely the failure the row model exists
// to prevent — a merge request green on 11, never asked about 10, reading as
// fully green.
func TestEvidenceThatDoesNotCoverTheApplicableCoresDenies(t *testing.T) {
	verdict := classify(botMr(), []string{"10", "11"}, greenOn("11"))

	if verdict.Status == ReadyAuto {
		t.Fatal("evidence silent about core 10 was accepted as covering it")
	}
	if !slices.Contains(verdict.Reasons, "local-missing") {
		t.Errorf("reasons %v", verdict.Reasons)
	}

	// And evidence that does cover them is still ready.
	if got := classify(botMr(), []string{"10", "11"}, greenOn("10", "11")); got.Status != ReadyAuto {
		t.Errorf("got %s %v", got.Status, got.Reasons)
	}
}
