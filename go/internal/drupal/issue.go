package drupal

import (
	"regexp"
	"sort"
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
