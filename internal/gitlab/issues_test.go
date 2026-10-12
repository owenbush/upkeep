package gitlab

import (
	"fmt"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/drupal"
)

// helper is the project whose queue has moved, and every fixture below is a
// row from it rather than an invented shape.
func helperProject() Project {
	return Project{
		ID:     15204,
		Path:   "helper",
		WebURL: "https://web.test/project/helper",
	}
}

// A real migrated issue, with the label vocabulary as helper actually carries
// it: camelCase in the scoped values, a v-prefixed branch, an unscoped
// component.
const migratedIssueJSON = `{
	"iid": 3619171,
	"title": "Add URL and menu link operation helpers",
	"web_url": "https://web.test/project/helper/-/work_items/3619171",
	"state": "opened",
	"labels": ["category::feature", "code", "priority::normal", "state::needsReview", "v8.x-1.x-dev"]
}`

func TestAMigratedIssueMapsOntoTheModelEveryConsumerReads(t *testing.T) {
	client, _ := clientWith("", ok("["+migratedIssueJSON+"]"))

	issues, failure := client.OpenIssues(helperProject())
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues", len(issues))
	}

	issue := issues[0]
	for _, check := range []struct {
		what string
		got  any
		want any
	}{
		{"nid", issue.Nid, 3619171},
		{"title", issue.Title, "Add URL and menu link operation helpers"},
		{"url", issue.URL, "https://web.test/project/helper/-/work_items/3619171"},
		{"project", issue.Project, "helper"},
		{"status", issue.Status, drupal.StatusNeedsReview},
		{"category", issue.Category, "Feature request"},
		{"priority", issue.Priority, 200},
		{"version", issue.Version, "8.x-1.x-dev"},
		{"component", issue.Component, "code"},
	} {
		if check.got != check.want {
			t.Errorf("%s = %v, want %v", check.what, check.got, check.want)
		}
	}
}

// The URL is GitLab's own web_url, never a path assembled here.
//
// It already points at /-/work_items/<iid>, which is what the project's links
// use; building one would be a second copy of GitLab's routing to keep right.
func TestTheIssueURLIsTheOneGitlabGave(t *testing.T) {
	client, _ := clientWith("", ok(`[{"iid":1,"title":"t","web_url":"https://elsewhere.test/surprising/path"}]`))

	issues, _ := client.OpenIssues(helperProject())

	if issues[0].URL != "https://elsewhere.test/surprising/path" {
		t.Errorf("URL %q", issues[0].URL)
	}
}

// Every state label helper carries maps to the drupal.org status it stands
// for. These five are the complete open vocabulary, measured.
func TestTheOpenStateVocabularyMaps(t *testing.T) {
	for label, want := range map[string]drupal.IssueStatus{
		"state::accepted":    drupal.StatusActive,
		"state::needsReview": drupal.StatusNeedsReview,
		"state::needsWork":   drupal.StatusNeedsWork,
		"state::rtbc":        drupal.StatusRtbc,
		"state::toBePorted":  drupal.StatusPatchToBePorted,
	} {
		issue := issueFromRow(map[string]any{
			"iid": 1.0, "state": "opened", "labels": []any{label},
		}, helperProject())

		if issue.Status != want {
			t.Errorf("%s -> %v, want %v", label, issue.Status, want)
		}
	}
}

// drupal.org's several flavours of closed survive the migration as
// `state::closed` plus a reason, and upkeep keeps the distinction.
//
// Reporting a duplicate or a won't-fix as plainly fixed is a statement about
// somebody's issue that is simply false.
func TestAClosedIssueKeepsTheReasonItWasClosedFor(t *testing.T) {
	for _, tc := range []struct {
		labels []any
		want   drupal.IssueStatus
	}{
		{[]any{"state::closed"}, drupal.StatusFixed},
		{[]any{"state::closed", "why::duplicate"}, drupal.StatusClosedDuplicate},
		{[]any{"state::closed", "why::wontFix"}, drupal.StatusClosedWontFix},
		{[]any{"state::closed", "why::worksAsDesigned"}, drupal.StatusClosedWorksAsDesigned},
	} {
		issue := issueFromRow(map[string]any{
			"iid": 1.0, "state": "closed", "labels": tc.labels,
		}, helperProject())

		if issue.Status != tc.want {
			t.Errorf("%v -> %v, want %v", tc.labels, issue.Status, tc.want)
		}
	}
}

// The reason wins however the labels are ordered.
//
// GitLab returns them alphabetically, so `state::closed` arrives before
// `why::duplicate` today and nothing guarantees it will tomorrow.
func TestTheClosedReasonDoesNotDependOnLabelOrder(t *testing.T) {
	forwards := issueFromRow(map[string]any{
		"iid": 1.0, "state": "closed",
		"labels": []any{"state::closed", "why::duplicate"},
	}, helperProject())
	backwards := issueFromRow(map[string]any{
		"iid": 1.0, "state": "closed",
		"labels": []any{"why::duplicate", "state::closed"},
	}, helperProject())

	if forwards.Status != backwards.Status {
		t.Errorf("order changed the answer: %v vs %v", forwards.Status, backwards.Status)
	}
	if forwards.Status != drupal.StatusClosedDuplicate {
		t.Errorf("status %v", forwards.Status)
	}
}

// A closed issue carrying no reason is plainly fixed, which is 92 of helper's
// 100 most recently closed.
func TestTheStructuralStateIsTheFloor(t *testing.T) {
	closed := issueFromRow(map[string]any{"iid": 1.0, "state": "closed"}, helperProject())
	open := issueFromRow(map[string]any{"iid": 1.0, "state": "opened"}, helperProject())

	if closed.Status != drupal.StatusFixed {
		t.Errorf("closed -> %v", closed.Status)
	}
	if open.Status != drupal.StatusActive {
		t.Errorf("opened -> %v", open.Status)
	}
}

// A label upkeep does not recognise is ignored, and the issue still lists.
//
// The migration renames rather than transliterating — drupal.org's "Active" is
// `state::accepted` — so an unknown scoped value is one upkeep has not
// measured, not one it should guess at.
func TestAnUnrecognisedLabelIsIgnoredRatherThanGuessedAt(t *testing.T) {
	issue := issueFromRow(map[string]any{
		"iid": 1.0, "state": "opened", "title": "t",
		"labels": []any{"state::somethingNew", "category::invented", "priority::urgent", "why::mysterious"},
	}, helperProject())

	if issue.Status != drupal.StatusActive {
		t.Errorf("status %v, want the structural default", issue.Status)
	}
	if issue.Category != "" || issue.Priority != 0 {
		t.Errorf("it invented category %q / priority %d", issue.Category, issue.Priority)
	}
	if issue.Nid != 1 {
		t.Error("the issue did not survive its unknown labels")
	}
}

// An issue with no labels at all still lists. One of helper's 74 open issues
// is exactly this — and it is the GitLab-native one, so it is the case that
// matters most.
func TestAnIssueWithNoLabelsStillLists(t *testing.T) {
	client, _ := clientWith("", ok(`[{"iid":3619172,"title":"Drush command to run a module hook",
		"web_url":"https://web.test/project/helper/-/work_items/3619172","state":"opened"}]`))

	issues, failure := client.OpenIssues(helperProject())
	if failure != nil {
		t.Fatalf("failure: %v", failure)
	}
	if len(issues) != 1 || issues[0].Nid != 3619172 {
		t.Fatalf("got %+v", issues)
	}
	if issues[0].Status != drupal.StatusActive {
		t.Errorf("status %v", issues[0].Status)
	}
}

// A version label is a branch; a word that merely begins with v is a
// component.
func TestAVersionLabelIsToldFromAWordBeginningWithV(t *testing.T) {
	for label, wantVersion := range map[string]string{
		"v8.x-1.x-dev": "8.x-1.x-dev",
		"v2.x-dev":     "2.x-dev",
		"v7.x-1.9":     "7.x-1.9",
		"views":        "",
		"v":            "",
	} {
		issue := issueFromRow(map[string]any{
			"iid": 1.0, "labels": []any{label},
		}, helperProject())

		if issue.Version != wantVersion {
			t.Errorf("%q -> version %q, want %q", label, issue.Version, wantVersion)
		}
		if wantVersion == "" && issue.Component != label {
			t.Errorf("%q should have been the component, got %q", label, issue.Component)
		}
	}
}

// A second version label is ignored, not recorded as the component.
//
// No issue on helper carries two, so this is latent rather than observed — but
// recording `v7.x-1.x-dev` as a component would be wrong by construction.
func TestASecondVersionLabelDoesNotBecomeTheComponent(t *testing.T) {
	issue := issueFromRow(map[string]any{
		"iid": 1.0, "labels": []any{"v8.x-1.x-dev", "v7.x-1.x-dev"},
	}, helperProject())

	if issue.Version != "8.x-1.x-dev" {
		t.Errorf("version %q", issue.Version)
	}
	if issue.Component != "" {
		t.Errorf("the second version became the component: %q", issue.Component)
	}
}

// The first unscoped label is the component and the rest are ignored, rather
// than concatenated into something nobody wrote.
func TestTheFirstComponentWins(t *testing.T) {
	issue := issueFromRow(map[string]any{
		"iid": 1.0, "labels": []any{"code", "documentation", "miscellaneous"},
	}, helperProject())

	if issue.Component != "code" {
		t.Errorf("component %q", issue.Component)
	}
}

// Attachments are empty, because a project that has moved takes contributions
// as merge requests. The patch surface finding nothing is the truth.
func TestAGitlabIssueCarriesNoPatchFiles(t *testing.T) {
	client, _ := clientWith("", ok("["+migratedIssueJSON+"]"))

	issues, _ := client.OpenIssues(helperProject())

	if len(issues[0].Files) != 0 {
		t.Errorf("it invented %d attachments", len(issues[0].Files))
	}
	if issues[0].PatchCount() != 0 {
		t.Error("the patch surface was told there were patches")
	}
}

// Pagination walks until a short page, and asks for the open queue only.
func TestOpenIssuesWalksEveryPage(t *testing.T) {
	first := make([]string, 100)
	for index := range first {
		first[index] = fmt.Sprintf(`{"iid":%d,"title":"t","state":"opened"}`, index+1)
	}

	client, transport := clientWith("",
		ok("["+strings.Join(first, ",")+"]"),
		ok(`[{"iid":101,"title":"t","state":"opened"}]`),
	)

	issues, failure := client.OpenIssues(helperProject())
	if failure != nil {
		t.Fatalf("failure: %v", failure)
	}
	if len(issues) != 101 {
		t.Fatalf("got %d issues", len(issues))
	}

	urls := transport.urls()
	if len(urls) != 2 {
		t.Fatalf("%d requests: %v", len(urls), urls)
	}
	for _, requested := range urls {
		if !strings.Contains(requested, "state=opened") {
			t.Errorf("request did not ask for the open queue: %s", requested)
		}
	}
	if !strings.Contains(urls[1], "page=2") {
		t.Errorf("second request was not page 2: %s", urls[1])
	}
}

// A full page every time is bounded, because an unbounded loop against a
// paginating API is how a bug becomes a denial of service against somebody
// else's server.
func TestOpenIssuesIsBounded(t *testing.T) {
	rows := make([]string, 100)
	for index := range rows {
		rows[index] = fmt.Sprintf(`{"iid":%d,"title":"t"}`, index+1)
	}
	full := ok("[" + strings.Join(rows, ",") + "]")

	transport := &recorder{fallback: &full}
	client := NewClient(transport, "", "https://api.test/api/v4", "https://web.test")

	if _, failure := client.OpenIssues(helperProject()); failure != nil {
		t.Fatalf("failure: %v", failure)
	}

	if len(transport.requests) != maxIssuePages {
		t.Errorf("%d requests, want the %d-page bound", len(transport.requests), maxIssuePages)
	}
}

// An empty queue is no issues and no failure, which is what a migrated project
// with nothing open looks like.
func TestAnEmptyQueueIsNotAFailure(t *testing.T) {
	client, _ := clientWith("", ok("[]"))

	issues, failure := client.OpenIssues(helperProject())

	if failure != nil {
		t.Fatalf("failure: %v", failure)
	}
	if len(issues) != 0 {
		t.Errorf("got %d issues", len(issues))
	}
}

// A failure is reported rather than returned as an empty list: returning less
// data is indistinguishable at the call site from there being less data.
func TestAFailedReadIsReported(t *testing.T) {
	client, _ := clientWith("", reply{status: 429, body: `{"message":"slow down"}`})

	issues, failure := client.OpenIssues(helperProject())

	if failure == nil {
		t.Fatal("a rate limit read as an empty queue")
	}
	if issues != nil {
		t.Errorf("it returned %d issues alongside the failure", len(issues))
	}
}

// One issue by its number.
func TestIssueReadsOneByIid(t *testing.T) {
	client, transport := clientWith("", ok(migratedIssueJSON))

	issue, found := client.Issue(helperProject(), 3619171)
	if !found {
		t.Fatal("not found")
	}
	if issue.Nid != 3619171 || issue.Status != drupal.StatusNeedsReview {
		t.Errorf("got %+v", issue)
	}
	if want := "/projects/15204/issues/3619171"; !strings.Contains(transport.urls()[0], want) {
		t.Errorf("requested %q, want it to contain %q", transport.urls()[0], want)
	}
}

// A number that is not an issue reports false rather than a blank issue.
func TestIssueReportsAMissingOneAsAbsent(t *testing.T) {
	client, _ := clientWith("", reply{status: 404, body: `{"message":"404 Not found"}`})

	if issue, found := client.Issue(helperProject(), 999); found {
		t.Errorf("it found %+v", issue)
	}
}

// A list where an object was promised is a structural fault, not an issue with
// every field defaulted.
func TestIssueRefusesAListBody(t *testing.T) {
	client, _ := clientWith("", ok(`[{"iid":1}]`))

	if issue, found := client.Issue(helperProject(), 1); found {
		t.Errorf("a list body became an issue: %+v", issue)
	}
}

// Labels that are not a list of strings degrade the row rather than aborting
// the scan — the tolerant narrowing the payload boundary exists for.
func TestMalformedLabelsDegradeTheRowOnly(t *testing.T) {
	for _, labels := range []any{nil, "code", []any{1.0, true}, map[string]any{}} {
		issue := issueFromRow(map[string]any{
			"iid": 1.0, "title": "t", "labels": labels,
		}, helperProject())

		if issue.Nid != 1 || issue.Title != "t" {
			t.Errorf("labels %v took the row down: %+v", labels, issue)
		}
	}
}
