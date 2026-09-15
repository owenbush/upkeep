package drupal

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// IssueFile is an attachment on an issue.
type IssueFile struct {
	Name      string
	URL       string
	Size      int64
	Timestamp int64
	// OwnerUID is the account that posted it, when the API said. It is what
	// `patch:promote` credits the work to.
	OwnerUID int
}

var patchComment = regexp.MustCompile(`^\d+-(\d+)`)

// IsPatch reports whether this attachment is something upkeep can apply.
func (f IssueFile) IsPatch() bool {
	lower := strings.ToLower(f.Name)

	return strings.HasSuffix(lower, ".patch") || strings.HasSuffix(lower, ".diff")
}

// CommentNumber is the drupal.org convention — {nid}-{comment}.patch, or
// {nid}-{comment}-{description}.patch. Higher is newer, and it is a better
// ordering than the timestamp because a re-roll posted out of order still
// sorts correctly.
func (f IssueFile) CommentNumber() (int, bool) {
	if m := patchComment.FindStringSubmatch(f.Name); m != nil {
		return atoi(m[1]), true
	}

	return 0, false
}

// Issue is a drupal.org issue.
type Issue struct {
	Nid       int
	Title     string
	Status    IssueStatus
	URL       string
	Project   string
	Priority  int
	Version   string
	Component string
	Category  string
	Files     []IssueFile
}

// Patches are the attachments upkeep could apply.
func (i Issue) Patches() []IssueFile {
	var patches []IssueFile
	for _, file := range i.Files {
		if file.IsPatch() {
			patches = append(patches, file)
		}
	}

	return patches
}

// PatchCount is how many patch files the issue carries.
func (i Issue) PatchCount() int { return len(i.Patches()) }

// LatestPatch is the newest patch on the issue, or false when there is none.
//
// Ordered by comment number where both have one, because that is the
// revision drupal.org assigns; by timestamp otherwise.
func (i Issue) LatestPatch() (IssueFile, bool) {
	patches := i.Patches()
	if len(patches) == 0 {
		return IssueFile{}, false
	}

	sort.SliceStable(patches, func(a, b int) bool {
		ca, aOK := patches[a].CommentNumber()
		cb, bOK := patches[b].CommentNumber()
		if aOK && bOK {
			return ca > cb
		}

		return patches[a].Timestamp > patches[b].Timestamp
	})

	return patches[0], true
}

var priorityLabels = map[int]string{400: "Critical", 300: "Major", 200: "Normal", 100: "Minor"}

// PriorityLabel is drupal.org's word for the priority, or "" when unknown.
func (i Issue) PriorityLabel() string { return priorityLabels[i.Priority] }

// nilIfEmpty and nilIfZero write an absent field as absent.
//
// PHP's models declare these nullable and this side does not, so "" and 0 are
// the only spelling of "not supplied" available here. Writing them as such
// keeps a payload written by either implementation identical, which is what
// the dashboard snapshot depends on.
func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func nilIfZero(value int) any {
	if value == 0 {
		return nil
	}

	return value
}

// ToAPIMap renders the attachment back into the shape it was read from.
//
// The "file" envelope is the file resource's own, and what a snapshot written
// by either implementation carries.
func (f IssueFile) ToAPIMap() map[string]any {
	var uid any
	if f.OwnerUID != 0 {
		uid = f.OwnerUID
	}

	return map[string]any{
		"file": map[string]any{
			"filename":  f.Name,
			"url":       f.URL,
			"filesize":  strconv.FormatInt(f.Size, 10),
			"timestamp": strconv.FormatInt(f.Timestamp, 10),
			"uid":       uid,
		},
	}
}

// ToAPIMap renders the issue back into the shape it was read from, so a
// snapshot round-trips through the same narrowing a live fetch uses.
func (i Issue) ToAPIMap() map[string]any {
	var project any
	if i.Project != "" {
		project = map[string]any{"machine_name": i.Project}
	}

	files := make([]any, 0, len(i.Files))
	for _, file := range i.Files {
		files = append(files, file.ToAPIMap())
	}

	return map[string]any{
		"nid":                i.Nid,
		"title":              i.Title,
		"field_issue_status": strconv.Itoa(int(i.Status)),
		"url":                i.URL,
		"field_project":      project,
		// Absent rather than empty, which is what PHP's nullable fields write
		// and what this side cannot tell apart anyway: a zero priority and an
		// empty version are both "the payload did not say".
		"field_issue_priority":  nilIfZero(i.Priority),
		"field_issue_version":   nilIfEmpty(i.Version),
		"field_issue_component": nilIfEmpty(i.Component),
		"field_issue_category":  categoryID(i.Category),
		"field_issue_files":     files,
	}
}
