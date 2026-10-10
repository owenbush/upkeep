// Package notes drafts release notes from a module's merged history.
package notes

import (
	"fmt"
	"strings"

	"github.com/owenbush/upkeep/internal/config"
	"github.com/owenbush/upkeep/internal/gitlab"
)

// Generator turns a module's merged merge-request history into a paste-ready
// Markdown draft for a drupal.org release node.
//
// Grouping and formatting only; fetching is the command's job. Bot
// compatibility merge requests — the homogeneous noise — are compressed under
// one heading and everything else is listed individually, because a release
// note that lists forty identical "Automated Project Update Bot fixes" lines
// buries the handful of changes a reader is looking for.
type Generator struct {
	bot config.BotPattern
}

// NewGenerator builds one over the shared bot pattern.
func NewGenerator(bot config.BotPattern) *Generator { return &Generator{bot: bot} }

// LatestTag is the newest tag by commit date, reporting false when there is
// none to be had.
//
// A tag with no resolvable date is skipped rather than ordered last: an undated
// tag cannot serve as a "since" boundary, and picking it would produce a draft
// covering a range nobody asked for.
func LatestTag(tags []gitlab.Tag) (gitlab.Tag, bool) {
	var latest gitlab.Tag
	found := false

	for _, tag := range tags {
		if tag.CreatedAt.IsZero() {
			continue
		}
		if !found || tag.CreatedAt.After(latest.CreatedAt) {
			latest, found = tag, true
		}
	}

	return latest, found
}

// Generate renders the draft.
//
// sinceTag is the boundary the merged set was gathered against; pass a zero
// Tag with tagged=false when the project has none, in which case merged is the
// whole history.
func (g *Generator) Generate(
	module string, sinceTag gitlab.Tag, tagged bool, merged []gitlab.MergeRequest,
) string {
	lines := []string{heading(module, sinceTag, tagged)}

	if !tagged {
		lines = append(lines, "",
			"No previous tag exists; the list below covers every merged merge request.")
	}

	if len(merged) == 0 {
		nothing := "No merge requests have been merged in this project."
		if tagged {
			nothing = fmt.Sprintf("No merge requests have been merged since tag %s.", sinceTag.Name)
		}

		return strings.Join(append(lines, "", nothing), "\n")
	}

	var bot, other []gitlab.MergeRequest
	for _, mergeRequest := range merged {
		if g.bot.MatchesAuthor(mergeRequest.AuthorUsername, mergeRequest.AuthorID) {
			bot = append(bot, mergeRequest)
		} else {
			other = append(other, mergeRequest)
		}
	}

	for _, group := range []struct {
		title   string
		entries []gitlab.MergeRequest
	}{
		{"Compatibility updates", bot},
		{"Changes", other},
	} {
		if len(group.entries) == 0 {
			continue
		}
		lines = append(lines, "", "### "+group.title, "")
		for _, mergeRequest := range group.entries {
			lines = append(lines, entry(mergeRequest))
		}
	}

	return strings.Join(lines, "\n")
}

// heading names the module and the range the draft covers.
func heading(module string, sinceTag gitlab.Tag, tagged bool) string {
	if !tagged {
		return fmt.Sprintf("## %s — full merged history (no previous tag)", module)
	}

	// The date is always present here — LatestTag only ever returns a dated
	// tag — but a caller may hand over a tag from somewhere else, and a
	// heading claiming a date nobody has is worse than one saying so.
	date := "date unknown"
	if !sinceTag.CreatedAt.IsZero() {
		date = sinceTag.CreatedAt.Format("2006-01-02")
	}

	return fmt.Sprintf("## %s — since %s (%s)", module, sinceTag.Name, date)
}

// entry is one merged merge request, linked to where it can be read.
func entry(mergeRequest gitlab.MergeRequest) string {
	return fmt.Sprintf(
		"- %s ([!%d](%s) by %s)",
		mergeRequest.Title, mergeRequest.IID, mergeRequest.WebURL, mergeRequest.AuthorUsername,
	)
}
