package patches

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/drupal"
)

// Selection is what naming a patch on the command line resolved to.
//
// Three outcomes rather than two, because "several patches and you did not say
// which" is not an error and not an answer — it is a question for the
// operator, and only the command knows whether there is anyone there to ask.
type Selection struct {
	// Chosen is the settled patch. Nil when nothing was settled.
	Chosen *drupal.IssueFile
	// Candidates is every patch on the issue, newest first.
	Candidates []drupal.IssueFile
	// Problem says why nothing could be settled. Empty when there is no
	// problem.
	Problem string
	// Ambiguous is several candidates and no instruction: a question, not a
	// failure.
	Ambiguous bool
}

func settled(chosen drupal.IssueFile, candidates []drupal.IssueFile) Selection {
	return Selection{Chosen: &chosen, Candidates: candidates}
}

func ambiguous(candidates []drupal.IssueFile) Selection {
	return Selection{Candidates: candidates, Ambiguous: true}
}

func problem(format string, args ...any) Selection {
	return Selection{Problem: fmt.Sprintf(format, args...)}
}

// Select decides which patch on an issue the operator meant.
//
// An issue routinely carries several: an original, two re-rolls, an interdiff,
// and a screenshot. Picking the wrong one wastes a full environment build and
// reports a verdict on code nobody submitted, so the rules are explicit rather
// than clever:
//
//	file    exactly that attachment, matched case-insensitively on the
//	        filename; an unmatched name is a problem, never a silent fallback
//	        to something else.
//	latest  the newest patch, no question asked.
//	neither one patch settles itself; several are ambiguous, and the command
//	        decides whether to ask or to take the newest.
//
// "Newest" is the same ordering `upkeep patches` prints — the drupal.org
// comment number when the filename carries one, the upload timestamp otherwise.
func Select(issue drupal.Issue, file string, latest bool) Selection {
	candidates := Candidates(issue)
	if len(candidates) == 0 {
		return problem(
			"Issue #%d has no patch files attached (%d attachment(s), none of them a .patch or .diff).",
			issue.Nid, len(issue.Files),
		)
	}

	if file != "" {
		for _, candidate := range candidates {
			if strings.EqualFold(candidate.Name, file) {
				// Newest first, so a name shared by several re-uploads settles
				// on the most recent of them. Picking an older one is what
				// --url is for: drupal.org keeps the filenames identical but
				// the URLs distinct.
				return settled(candidate, candidates)
			}
		}

		return problem(
			"Issue #%d has no patch named %q. It carries:\n  %s",
			issue.Nid, file, strings.Join(Labels(candidates), "\n  "),
		)
	}

	if latest || len(candidates) == 1 {
		return settled(candidates[0], candidates)
	}

	return ambiguous(candidates)
}

// Candidates is every patch on the issue, newest first.
func Candidates(issue drupal.Issue) []drupal.IssueFile {
	patches := []drupal.IssueFile{}
	for _, file := range issue.Files {
		if file.IsPatch() {
			patches = append(patches, file)
		}
	}

	// Stable, so two uploads the ordering cannot separate stay in the order
	// the API listed them rather than swapping between runs.
	sort.SliceStable(patches, func(a, b int) bool {
		leftComment, leftHas := patches[a].CommentNumber()
		rightComment, rightHas := patches[b].CommentNumber()
		if leftHas && rightHas {
			return rightComment < leftComment
		}

		return patches[b].Timestamp < patches[a].Timestamp
	})

	return patches
}

// Labels is descriptions for a list of patches, guaranteed distinct.
//
// Distinctness is not cosmetic. The Project Update Bot re-uploads its patch
// under the *same* filename on every run, so a real issue routinely carries
// four attachments called entity_type_access_conditions.1.0.1.rector.patch
// differing only by date and URL. A picker offering four identical lines
// cannot be answered, and a prompt whose answer is matched back by label would
// resolve every one of them to the first. The upload date usually separates
// them; when even that collides, an ordinal does.
func Labels(patches []drupal.IssueFile) []string {
	labels := make([]string, 0, len(patches))
	seen := map[string]int{}

	for _, patch := range patches {
		label := Describe(patch)
		if count, already := seen[label]; already {
			seen[label] = count + 1
			label = fmt.Sprintf("%s (#%d)", label, count+1)
		} else {
			seen[label] = 1
		}
		labels = append(labels, label)
	}

	return labels
}

// Describe is the one-line description of a patch a picker offers, e.g.
// "3597808-9-d11.patch (comment 9, 4.2 KB)".
func Describe(patch drupal.IssueFile) string {
	parts := []string{}

	if comment, has := patch.CommentNumber(); has {
		parts = append(parts, fmt.Sprintf("comment %d", comment))
	}
	if patch.Size > 0 {
		parts = append(parts, humanSize(patch.Size))
	}
	if patch.Timestamp > 0 {
		parts = append(parts, time.Unix(int64(patch.Timestamp), 0).UTC().Format("2006-01-02"))
	}

	if len(parts) == 0 {
		return patch.Name
	}

	return fmt.Sprintf("%s (%s)", patch.Name, strings.Join(parts, ", "))
}

func humanSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
}
