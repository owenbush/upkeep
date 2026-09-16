package drupal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const issueFixtureDir = "../../testdata/issues"

type expectedIssue struct {
	Parsed        bool   `json:"parsed"`
	Nid           int    `json:"nid"`
	Title         string `json:"title"`
	Status        int    `json:"status"`
	URL           string `json:"url"`
	Project       string `json:"project"`
	Priority      int    `json:"priority"`
	PriorityLabel string `json:"priority_label"`
	Version       string `json:"version"`
	Component     string `json:"component"`
	Category      string `json:"category"`
	PatchCount    int    `json:"patch_count"`
	LatestPatch   string `json:"latest_patch"`
	Files         []struct {
		Name          string `json:"name"`
		URL           string `json:"url"`
		Size          int64  `json:"size"`
		Timestamp     int64  `json:"timestamp"`
		OwnerUID      int    `json:"owner_uid"`
		IsPatch       bool   `json:"is_patch"`
		CommentNumber *int   `json:"comment_number"`
	} `json:"files"`
	RoundTripped map[string]any `json:"round_tripped"`
}

// Two reasons this matters more than it looks.
//
// The category is an integer id in the payload and a label on the issue page,
// so an implementation that passes the id through prints "1" where the other
// prints "Bug report" — which is exactly what this port did until this test
// was written.
//
// And the dashboard snapshot stores these payloads verbatim: an issue payload
// written by one implementation is read back by the other, so the attachment
// envelope, the resolved-file shape and the round trip all have to agree.
//
// The answers are committed; git history holds the PHP that produced them.
func TestIssueParsingMatchesPhp(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(issueFixtureDir, "expected.json"))
	if err != nil {
		t.Fatalf("answers: %v (a committed fixture — see git history for the PHP that produced it)", err)
	}

	var expected map[string]expectedIssue
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("answers: %v", err)
	}

	fixtures, err := filepath.Glob(filepath.Join(issueFixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	// The answers file lives alongside the fixtures.
	if len(fixtures)-1 != len(expected) {
		t.Fatalf("%d fixtures on disk, %d answers recorded — regenerate", len(fixtures)-1, len(expected))
	}

	for _, fixture := range fixtures {
		name := filepath.Base(fixture)
		if name == "expected.json" {
			continue
		}
		want, recorded := expected[name]
		if !recorded {
			t.Errorf("%s: no recorded answer", name)

			continue
		}

		data := readPayload(t, fixture)
		issue, parsed := IssueFrom(data)
		if parsed != want.Parsed {
			t.Errorf("%s: parsed=%v, PHP says %v", name, parsed, want.Parsed)

			continue
		}
		if !parsed {
			continue
		}

		checkField(t, name, "nid", issue.Nid, want.Nid)
		checkField(t, name, "title", issue.Title, want.Title)
		checkField(t, name, "status", int(issue.Status), want.Status)
		checkField(t, name, "url", issue.URL, want.URL)
		checkField(t, name, "project", issue.Project, want.Project)
		checkField(t, name, "priority", issue.Priority, want.Priority)
		checkField(t, name, "priority label", issue.PriorityLabel(), want.PriorityLabel)
		checkField(t, name, "version", issue.Version, want.Version)
		checkField(t, name, "component", issue.Component, want.Component)
		checkField(t, name, "category", issue.Category, want.Category)
		checkField(t, name, "patch count", issue.PatchCount(), want.PatchCount)

		latest, hasLatest := issue.LatestPatch()
		latestName := ""
		if hasLatest {
			latestName = latest.Name
		}
		checkField(t, name, "latest patch", latestName, want.LatestPatch)

		if len(issue.Files) != len(want.Files) {
			t.Errorf("%s: %d attachments, PHP read %d", name, len(issue.Files), len(want.Files))

			continue
		}
		for i, wantFile := range want.Files {
			file := issue.Files[i]
			checkField(t, name, "file name", file.Name, wantFile.Name)
			checkField(t, name, "file url", file.URL, wantFile.URL)
			checkField(t, name, "file size", file.Size, wantFile.Size)
			checkField(t, name, "file timestamp", file.Timestamp, wantFile.Timestamp)
			checkField(t, name, "file owner", file.OwnerUID, wantFile.OwnerUID)
			checkField(t, name, "file is a patch", file.IsPatch(), wantFile.IsPatch)

			comment, hasComment := file.CommentNumber()
			if (wantFile.CommentNumber != nil) != hasComment {
				t.Errorf("%s: comment number present=%v, PHP says %v", name, hasComment, wantFile.CommentNumber != nil)
			} else if hasComment && comment != *wantFile.CommentNumber {
				t.Errorf("%s: comment number %d, PHP read %d", name, comment, *wantFile.CommentNumber)
			}
		}
	}
}

// A snapshot written by one implementation is read back by the other, so what
// the round trip produces has to be the same payload on both sides.
func TestTheIssueRoundTripMatchesPhps(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(issueFixtureDir, "expected.json"))
	if err != nil {
		t.Fatalf("answers: %v", err)
	}
	var expected map[string]expectedIssue
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("answers: %v", err)
	}

	checked := 0
	for name, want := range expected {
		if !want.Parsed || want.RoundTripped == nil {
			continue
		}

		issue, parsed := IssueFrom(readPayload(t, filepath.Join(issueFixtureDir, name)))
		if !parsed {
			t.Errorf("%s: did not parse", name)

			continue
		}

		// Against PHP's payload directly, not against this implementation's
		// own output read back — that would let a bug in the writer cancel
		// itself out, which is exactly what the first version of this test
		// did.
		//
		// Through JSON both ways, so the comparison is of values rather than
		// of Go's int against PHP's float.
		got := normalise(t, issue.ToAPIMap())
		if !reflect.DeepEqual(got, normalise(t, want.RoundTripped)) {
			t.Errorf("%s: the written payload differs from PHP's:\nphp:  %v\nhere: %v",
				name, normalise(t, want.RoundTripped), got)

			continue
		}

		// And PHP's payload read back here must give the same issue, which is
		// what a snapshot written by the other implementation depends on.
		back, parsedBack := IssueFrom(want.RoundTripped)
		if !parsedBack {
			t.Errorf("%s: PHP's payload does not parse here:\n%v", name, want.RoundTripped)

			continue
		}
		if !reflect.DeepEqual(normalise(t, back.ToAPIMap()), got) {
			t.Errorf("%s: reading PHP's payload back gives something else:\n%v", name, normalise(t, back.ToAPIMap()))
		}
		checked++
	}

	if checked == 0 {
		t.Fatal("nothing was round-tripped")
	}
}

func readPayload(t *testing.T, path string) map[string]any {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	return data
}

// normalise puts a payload through JSON so an int and a float that mean the
// same number compare equal.
func normalise(t *testing.T, value map[string]any) map[string]any {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return out
}

func checkField[T comparable](t *testing.T, fixture, field string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s: %s is %v, PHP read %v", fixture, field, got, want)
	}
}
