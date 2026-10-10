package notes

import (
	"strings"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/config"
	"github.com/owenbush/upkeep/internal/gitlab"
)

func at(day string) time.Time {
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		panic(err)
	}

	return parsed
}

func aGenerator() *Generator { return NewGenerator(config.BotPatternForCore("11")) }

// byBot is a merge request the Project Update Bot opened.
func byBot(iid int, title string) gitlab.MergeRequest {
	pattern := config.BotPatternForCore("11")

	return gitlab.MergeRequest{
		IID: iid, Title: title, AuthorUsername: pattern.AuthorUsername, AuthorID: pattern.AuthorID,
		WebURL:       "https://git.drupalcode.org/project/pathauto/-/merge_requests/" + itoa(iid),
		SourceBranch: pattern.SourceBranch,
	}
}

func byPerson(iid int, title, who string) gitlab.MergeRequest {
	return gitlab.MergeRequest{
		IID: iid, Title: title, AuthorUsername: who,
		WebURL: "https://git.drupalcode.org/project/pathauto/-/merge_requests/" + itoa(iid),
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	return string(digits)
}

// The newest dated tag is the boundary.
func TestTheLatestTagIsTheNewestDatedOne(t *testing.T) {
	latest, found := LatestTag([]gitlab.Tag{
		{Name: "1.0.0", CreatedAt: at("2025-01-04")},
		{Name: "2.0.0", CreatedAt: at("2026-03-11")},
		{Name: "1.5.0", CreatedAt: at("2025-09-30")},
	})

	if !found || latest.Name != "2.0.0" {
		t.Errorf("got %q (%v)", latest.Name, found)
	}
}

// A tag with no resolvable date cannot serve as a "since" boundary, so it is
// skipped rather than ordered last — picking it would draft a range nobody
// asked for.
func TestAnUndatedTagIsNotABoundary(t *testing.T) {
	latest, found := LatestTag([]gitlab.Tag{
		{Name: "8.x-1.0"},
		{Name: "1.0.0", CreatedAt: at("2025-01-04")},
	})
	if !found || latest.Name != "1.0.0" {
		t.Errorf("got %q (%v)", latest.Name, found)
	}

	// And nothing but undated tags is no boundary at all.
	if _, found := LatestTag([]gitlab.Tag{{Name: "8.x-1.0"}, {Name: "8.x-1.1"}}); found {
		t.Error("an undated tag was used as a boundary")
	}
	if _, found := LatestTag(nil); found {
		t.Error("an empty tag list produced a boundary")
	}
}

// Bot merge requests are the homogeneous noise, so they go under one heading;
// everything else is listed individually.
func TestBotWorkIsCompressedAndEverythingElseIsListed(t *testing.T) {
	draft := aGenerator().Generate("pathauto",
		gitlab.Tag{Name: "1.5.0", CreatedAt: at("2026-03-11")}, true,
		[]gitlab.MergeRequest{
			byBot(101, "Automated Project Update Bot fixes"),
			byPerson(102, "Fix the token cache invalidation", "someone"),
			byBot(103, "Automated Project Update Bot fixes"),
		})

	if !strings.HasPrefix(draft, "## pathauto — since 1.5.0 (2026-03-11)") {
		t.Errorf("heading:\n%s", draft)
	}

	compatibility := strings.Index(draft, "### Compatibility updates")
	changes := strings.Index(draft, "### Changes")
	if compatibility < 0 || changes < 0 {
		t.Fatalf("missing headings:\n%s", draft)
	}
	// Noise first, so what a reader is looking for is at the bottom where the
	// eye lands after scrolling past it.
	if compatibility > changes {
		t.Errorf("the noise came after the changes:\n%s", draft)
	}

	// Each bot MR is still listed under its heading — compressed means
	// grouped, not summarised away.
	for _, iid := range []string{"!101", "!103"} {
		if !strings.Contains(draft[compatibility:changes], iid) {
			t.Errorf("%s is not under the compatibility heading:\n%s", iid, draft)
		}
	}
	if !strings.Contains(draft[changes:], "!102") {
		t.Errorf("the human's work is not under Changes:\n%s", draft)
	}
}

// An entry links to where it can be read and names who wrote it: a release
// note is pasted into a public node, and an unattributed line is somebody's
// work with their name taken off it.
func TestAnEntryLinksAndAttributes(t *testing.T) {
	draft := aGenerator().Generate("pathauto",
		gitlab.Tag{Name: "1.5.0", CreatedAt: at("2026-03-11")}, true,
		[]gitlab.MergeRequest{byPerson(102, "Fix the token cache invalidation", "someone")})

	want := "- Fix the token cache invalidation " +
		"([!102](https://git.drupalcode.org/project/pathauto/-/merge_requests/102) by someone)"
	if !strings.Contains(draft, want) {
		t.Errorf("got:\n%s\nwant a line:\n%s", draft, want)
	}
}

// A heading with only one kind of work does not print an empty section.
func TestAGroupWithNothingInItIsNotPrinted(t *testing.T) {
	onlyBot := aGenerator().Generate("pathauto",
		gitlab.Tag{Name: "1.5.0", CreatedAt: at("2026-03-11")}, true,
		[]gitlab.MergeRequest{byBot(101, "Automated Project Update Bot fixes")})
	if strings.Contains(onlyBot, "### Changes") {
		t.Errorf("an empty Changes heading was printed:\n%s", onlyBot)
	}

	onlyPeople := aGenerator().Generate("pathauto",
		gitlab.Tag{Name: "1.5.0", CreatedAt: at("2026-03-11")}, true,
		[]gitlab.MergeRequest{byPerson(102, "Fix it", "someone")})
	if strings.Contains(onlyPeople, "### Compatibility updates") {
		t.Errorf("an empty Compatibility heading was printed:\n%s", onlyPeople)
	}
}

// A project with no tag drafts its whole history, and says so — otherwise the
// draft reads as "everything since some release" and there was no release.
func TestAProjectWithNoTagDraftsItsWholeHistoryAndSaysSo(t *testing.T) {
	draft := aGenerator().Generate("pathauto", gitlab.Tag{}, false,
		[]gitlab.MergeRequest{byPerson(1, "The first thing", "someone")})

	if !strings.Contains(draft, "full merged history (no previous tag)") {
		t.Errorf("heading:\n%s", draft)
	}
	if !strings.Contains(draft, "covers every merged merge request") {
		t.Errorf("it did not say what the list covers:\n%s", draft)
	}
	if !strings.Contains(draft, "!1") {
		t.Errorf("the history is missing:\n%s", draft)
	}
}

// Nothing merged is a statement, not an empty draft: "nothing since 1.5.0" and
// "nothing ever" are different facts and a blank page is neither.
func TestNothingMergedSaysWhichKindOfNothing(t *testing.T) {
	sinceTag := aGenerator().Generate("pathauto",
		gitlab.Tag{Name: "1.5.0", CreatedAt: at("2026-03-11")}, true, nil)
	if !strings.Contains(sinceTag, "merged since tag 1.5.0") {
		t.Errorf("got:\n%s", sinceTag)
	}
	if strings.Contains(sinceTag, "###") {
		t.Errorf("an empty draft printed headings:\n%s", sinceTag)
	}

	untagged := aGenerator().Generate("pathauto", gitlab.Tag{}, false, nil)
	if !strings.Contains(untagged, "merged in this project") {
		t.Errorf("got:\n%s", untagged)
	}
	if strings.Contains(untagged, "since tag") {
		t.Errorf("an untagged project was described as having a tag:\n%s", untagged)
	}
}

// A tag from somewhere other than LatestTag may carry no date, and a heading
// claiming one nobody has is worse than one saying so.
func TestAHeadingNeverInventsADate(t *testing.T) {
	draft := aGenerator().Generate("pathauto", gitlab.Tag{Name: "8.x-1.0"}, true, nil)

	if !strings.Contains(draft, "since 8.x-1.0 (date unknown)") {
		t.Errorf("got:\n%s", draft)
	}
}

// Classification is author-only here. The gate's is stricter — it also
// requires the bot's own source branch — because a wrong answer there merges
// something, and a wrong answer here files a line under the wrong heading.
func TestClassificationHereIsAuthorOnly(t *testing.T) {
	pattern := config.BotPatternForCore("11")
	// The bot's account, on a branch it does not normally use.
	onAnotherBranch := gitlab.MergeRequest{
		IID: 200, Title: "Automated Project Update Bot fixes",
		AuthorUsername: pattern.AuthorUsername, AuthorID: pattern.AuthorID,
		SourceBranch: "some-other-branch",
	}

	draft := aGenerator().Generate("pathauto",
		gitlab.Tag{Name: "1.5.0", CreatedAt: at("2026-03-11")}, true,
		[]gitlab.MergeRequest{onAnotherBranch})

	if !strings.Contains(draft, "### Compatibility updates") {
		t.Errorf("the bot's own work was not grouped as such:\n%s", draft)
	}
	if pattern.Matches(pattern.AuthorUsername, pattern.AuthorID, "some-other-branch") {
		t.Error("the gate's stricter match has stopped being stricter")
	}
}
