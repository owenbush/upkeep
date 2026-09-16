package gitlab

import (
	"encoding/json"
	"testing"
)

// The obvious fields lie, so emptiness is keyed on the diff refs — and the
// unknown case must never be read as "not empty".
func TestCarriesChangesDistinguishesEmptyFromUnsettled(t *testing.T) {
	for _, tc := range []struct {
		name, base, head string
		want             Tristate
	}{
		{"real work", "aaa", "bbb", Yes},
		{"empty: branch identical to target", "same", "same", No},
		{"list endpoint omits diff_refs", "", "", Unknown},
		{"half a payload is still unsettled", "aaa", "", Unknown},
	} {
		mr := MergeRequest{DiffBaseSHA: tc.base, DiffHeadSHA: tc.head}
		if got := mr.CarriesChanges(); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// A draft carrying real commits is not empty, and detailed_merge_status reports
// draft_status for it — which is why emptiness is not read from there.
func TestADraftIsNotAnEmptyMergeRequest(t *testing.T) {
	draft := MergeRequest{Draft: true, DetailedMergeStatus: "draft_status", DiffBaseSHA: "aaa", DiffHeadSHA: "bbb"}

	if !draft.IsDraft() {
		t.Error("not recognised as a draft")
	}
	if draft.CarriesChanges() != Yes {
		t.Error("a draft with commits was read as empty")
	}
}

func TestEitherDraftSignalAloneCounts(t *testing.T) {
	if !(MergeRequest{Draft: true}).IsDraft() {
		t.Error("the flag alone should count")
	}
	if !(MergeRequest{DetailedMergeStatus: "draft_status"}).IsDraft() {
		t.Error("detailed_merge_status alone should count")
	}
}

// With no draft field at all, GitLab's own convention is the title prefix.
func TestDraftIsInferredFromTheTitleWhenTheFieldIsAbsent(t *testing.T) {
	mr := decodeMergeRequest(t, `{"iid":1,"title":"Draft: Issue #123 by nobody"}`)
	if !mr.Draft {
		t.Error("a Draft:-prefixed title with no draft field did not read as a draft")
	}

	plain := decodeMergeRequest(t, `{"iid":1,"title":"Issue #123 by nobody"}`)
	if plain.Draft {
		t.Error("an ordinary title read as a draft")
	}

	// An explicit false outranks the title, since the field is what GitLab
	// actually thinks.
	explicit := decodeMergeRequest(t, `{"iid":1,"title":"Draft: something","draft":false}`)
	if explicit.Draft {
		t.Error("an explicit draft:false was overridden by the title")
	}
}

// CI analyses the merge, not the branch, so evidence is keyed on the merge ref
// when there is one.
func TestTheMergeRefIsTheRevisionEvidenceIsAbout(t *testing.T) {
	if got := MergeRevision("merge-sha", "head-sha"); got != "merge-sha" {
		t.Errorf("got %q, want the merge ref", got)
	}
	if got := MergeRevision("", "head-sha"); got != "head-sha" {
		t.Errorf("got %q, want the head when there is no merge ref", got)
	}
	if got := MergeRevision("", ""); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

// Only success is green: skipped and manual have demonstrated nothing.
func TestOnlySuccessIsGreen(t *testing.T) {
	if !StatusSuccess.IsGreen() {
		t.Error("success is not green")
	}
	for _, status := range []PipelineStatus{StatusFailed, StatusRunning, StatusSkipped, StatusManual, StatusUnknown} {
		if status.IsGreen() {
			t.Errorf("%v reported as green", status)
		}
	}
}

// A status GitLab adds later must not crash the caller, and must still be
// reportable as what the server said.
func TestAnUnrecognisedStatusIsUnknownButStillQuotable(t *testing.T) {
	if got := PipelineStatusFrom("something_new"); got != StatusUnknown {
		t.Errorf("got %v, want unknown", got)
	}

	mr := decodeMergeRequest(t, `{"iid":1,"head_pipeline":{"id":7,"status":"something_new","web_url":"u"}}`)
	if mr.HeadPipeline == nil {
		t.Fatal("no pipeline")
	}
	if mr.HeadPipeline.Status != StatusUnknown {
		t.Errorf("status %v, want unknown", mr.HeadPipeline.Status)
	}
	if mr.HeadPipeline.RawStatus != "something_new" {
		t.Errorf("raw status %q, want what the server said", mr.HeadPipeline.RawStatus)
	}
}

// Push access is the question a fresh issue fork answers no to — and an
// unauthenticated read cannot answer it at all.
func TestCanPushIsUnknownWithoutPermissions(t *testing.T) {
	anonymous := decodeProject(t, `{"id":1,"path":"pathauto"}`)
	if got := anonymous.CanPush(); got != Unknown {
		t.Errorf("got %v, want unknown when the payload carries no permissions", got)
	}

	reporter := decodeProject(t, `{"id":1,"permissions":{"project_access":{"access_level":20}}}`)
	if got := reporter.CanPush(); got != No {
		t.Errorf("got %v, want no below developer", got)
	}

	developer := decodeProject(t, `{"id":1,"permissions":{"project_access":{"access_level":30}}}`)
	if got := developer.CanPush(); got != Yes {
		t.Errorf("got %v, want yes at developer", got)
	}
}

// Either grant is enough to push, and GitLab reports them separately.
func TestTheHigherOfTheTwoGrantsWins(t *testing.T) {
	project := decodeProject(t, `{"id":1,"permissions":{
		"project_access":{"access_level":10},
		"group_access":{"access_level":40}
	}}`)

	if got := project.CanPush(); got != Yes {
		t.Errorf("got %v: an inherited group grant was ignored", got)
	}
}

// The push URL is GitLab's own, never assembled: git.drupalcode.org serves the
// web while the SSH remote it advertises is git.drupal.org.
func TestTheSSHURLIsTakenFromThePayload(t *testing.T) {
	project := decodeProject(t, `{"id":1,"web_url":"https://git.drupalcode.org/project/pathauto",
		"ssh_url_to_repo":"git@git.drupal.org:project/pathauto.git"}`)

	if project.SSHURL != "git@git.drupal.org:project/pathauto.git" {
		t.Errorf("got %q", project.SSHURL)
	}
}

// A landing is the fact an open bot issue cannot tell you about itself.
func TestLatestMergedTakesTheNewestMergeAndIgnoresOpenOnes(t *testing.T) {
	latest := LatestMerged([]MergeRequest{
		{IID: 1, State: "merged", MergedAt: "2026-01-01T00:00:00Z"},
		{IID: 2, State: "opened", MergedAt: "2026-09-01T00:00:00Z"},
		{IID: 3, State: "merged", MergedAt: "2026-06-12T00:00:00Z"},
	})

	if latest == nil {
		t.Fatal("nothing found")
	}
	if latest.IID != 3 {
		t.Errorf("picked !%d, want !3 — the newest *merged* one", latest.IID)
	}

	if LatestMerged([]MergeRequest{{IID: 1, State: "opened"}}) != nil {
		t.Error("an open merge request was reported as a landing")
	}
}

// A cached snapshot must answer CarriesChanges without refetching, and an
// unknown must read back as unknown rather than as "not empty".
func TestTheRoundTripPreservesUnknownEmptiness(t *testing.T) {
	unknown := MergeRequest{IID: 1, Title: "t"}
	if unknown.ToAPIMap()["diff_refs"] != nil {
		t.Error("unknown diff refs were written as a present value")
	}

	back := remarshal(t, unknown.ToAPIMap())
	if back.CarriesChanges() != Unknown {
		t.Error("an unknown came back as something else")
	}

	empty := MergeRequest{IID: 1, DiffBaseSHA: "same", DiffHeadSHA: "same"}
	if remarshal(t, empty.ToAPIMap()).CarriesChanges() != No {
		t.Error("a known-empty merge request did not survive the round trip")
	}
}

// Tag dates come in more than one shape, and a tag with none is not a crash.
func TestTagDatesAreReadFromEitherFieldAndMayBeAbsent(t *testing.T) {
	created := decodeTag(t, `{"name":"1.0.0","commit":{"id":"abc","created_at":"2026-06-12T10:00:00.000+02:00"}}`)
	if created.CreatedAt.IsZero() {
		t.Error("created_at was not read")
	}
	if created.CommitSHA != "abc" {
		t.Errorf("commit sha %q", created.CommitSHA)
	}

	committed := decodeTag(t, `{"name":"1.0.1","commit":{"id":"def","committed_date":"2026-06-13T10:00:00Z"}}`)
	if committed.CreatedAt.IsZero() {
		t.Error("committed_date was not read as the fallback")
	}

	undated := decodeTag(t, `{"name":"1.0.2","target":"ghi"}`)
	if !undated.CreatedAt.IsZero() {
		t.Error("a tag with no commit block invented a date")
	}
	if undated.CommitSHA != "ghi" {
		t.Errorf("a lightweight tag's target was not used: %q", undated.CommitSHA)
	}
}

// The boundary narrows, it does not abort: a wrong-typed field degrades one
// row rather than taking the run down.
func TestWrongTypedFieldsFallBackRatherThanFail(t *testing.T) {
	mr := decodeMergeRequest(t, `{"iid":"4212","title":{"nested":"object"},"state":null,"sha":12345}`)

	if mr.IID != 4212 {
		t.Errorf("a quoted id did not narrow: %d", mr.IID)
	}
	if mr.Title != "" {
		t.Errorf("a structured title was not defaulted: %q", mr.Title)
	}
	if mr.State != "" {
		t.Errorf("a null state was not defaulted: %q", mr.State)
	}
	if mr.HeadSHA != "12345" {
		t.Errorf("a numeric sha did not narrow: %q", mr.HeadSHA)
	}
}

func decodeMergeRequest(t *testing.T, body string) MergeRequest {
	t.Helper()

	return mergeRequestFrom(decodePayload(t, body))
}

func decodeProject(t *testing.T, body string) Project {
	t.Helper()

	return projectFrom(decodePayload(t, body))
}

func decodeTag(t *testing.T, body string) Tag {
	t.Helper()

	return tagFrom(decodePayload(t, body))
}

func decodePayload(t *testing.T, body string) payload {
	t.Helper()

	var fields map[string]any
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("bad test fixture: %v", err)
	}

	return payload{data: fields}
}

// remarshal sends a model through JSON and back, which is what a cached
// snapshot does.
func remarshal(t *testing.T, fields map[string]any) MergeRequest {
	t.Helper()

	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	return decodeMergeRequest(t, string(encoded))
}
