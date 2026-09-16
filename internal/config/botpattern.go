// Package config holds shared definitions no single layer owns.
package config

// BotPattern is the single shared definition of what a Project Update Bot
// merge request looks like on git.drupalcode.org.
//
// Defaults are the live-verified observations: author username
// "Project-Update-Bot" (user id 66574), source branch
// "project-update-bot-only", title "Automated Project Update Bot fixes"
// (optionally "Draft: "-prefixed).
//
// Consumers: the release-notes command uses MatchesAuthor only, to group bot
// compatibility merge requests under one heading; the fast-lane gate uses
// Matches (author AND source branch) as its classification hook. The pattern
// lives in exactly one place.
type BotPattern struct {
	AuthorUsername string
	AuthorID       int
	SourceBranch   string
	Title          string
}

// BotPatternForCore is the bot pattern to apply when gating merge requests for
// one target core version.
//
// The verified pattern is core-independent — same account, same
// "project-update-bot-only" branch for every core — so today every core gets
// the defaults. Consumers must obtain their pattern through here, keyed by
// target core, so a future transition where the bot adopts per-core branches
// or accounts is a change to this one function rather than to gate logic.
func BotPatternForCore(coreMajor string) BotPattern {
	return BotPattern{
		AuthorUsername: "Project-Update-Bot",
		AuthorID:       66574,
		SourceBranch:   "project-update-bot-only",
		Title:          "Automated Project Update Bot fixes",
	}
}

// MatchesAuthor is an author-only match: the merge request was opened by the
// bot account, identified by username or (rename-proof) by user id.
//
// An id of 0 is the payload not carrying one, which is not a match on its own.
func (p BotPattern) MatchesAuthor(username string, id int) bool {
	return username == p.AuthorUsername || (id != 0 && id == p.AuthorID)
}

// Matches is the full pattern match for gate-style classification: the bot
// author AND the bot's dedicated source branch. A push from any other branch —
// even under the bot account — does not qualify.
func (p BotPattern) Matches(authorUsername string, authorID int, sourceBranch string) bool {
	return p.MatchesAuthor(authorUsername, authorID) && sourceBranch == p.SourceBranch
}
