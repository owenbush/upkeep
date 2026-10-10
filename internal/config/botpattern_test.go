package config

import "testing"

func TestTheBotIsRecognisedByIdWhenTheAccountIsRenamed(t *testing.T) {
	pattern := BotPatternForCore("11")

	if !pattern.MatchesAuthor("Project-Update-Bot-2", 66574) {
		t.Error("a renamed bot account was not recognised by its id")
	}
	if !pattern.MatchesAuthor("Project-Update-Bot", 999) {
		t.Error("the bot username was not recognised on its own")
	}
	if pattern.MatchesAuthor("somebody", 999) {
		t.Error("an unrelated account matched")
	}
}

// A zero id is the payload not carrying one, which is not evidence of
// anything.
func TestAnAbsentAuthorIdIsNotAMatch(t *testing.T) {
	if BotPatternForCore("11").MatchesAuthor("somebody", 0) {
		t.Error("an absent id matched the bot")
	}
}

// A push from any other branch — even under the bot account — does not
// qualify for the fast lane.
func TestTheFullMatchNeedsTheDedicatedBranch(t *testing.T) {
	pattern := BotPatternForCore("11")

	if !pattern.Matches("Project-Update-Bot", 66574, "project-update-bot-only") {
		t.Error("the verified pattern did not match")
	}
	if pattern.Matches("Project-Update-Bot", 66574, "3603341-fix-something") {
		t.Error("the bot account from another branch qualified")
	}
}

// The pattern is core-independent today, and consumers must still ask per
// core so a future per-core account is a change here and nowhere else.
func TestEveryCoreGetsTheVerifiedPatternToday(t *testing.T) {
	for _, core := range []string{"10", "11", "12", "13"} {
		if got := BotPatternForCore(core); got != BotPatternForCore("11") {
			t.Errorf("core %s got a different pattern: %+v", core, got)
		}
	}
}

// Zero is "the payload carried no id" on one side and "no id configured" on
// the other, and two absences are not a match.
//
// PHP spells the first of those null and gets this for free from the type. Go
// has one zero value doing both jobs, so the guard is explicit — and it is
// reachable: a pattern for a core whose bot has not been onboarded carries no
// id, and an author GitLab described thinly carries none either.
func TestTwoAbsentIdsAreNotAMatch(t *testing.T) {
	unconfigured := BotPattern{AuthorUsername: "nobody", SourceBranch: "nowhere"}

	if unconfigured.MatchesAuthor("somebody-else", 0) {
		t.Error("an author with no id matched a pattern with no id")
	}
	if unconfigured.Matches("somebody-else", 0, "nowhere") {
		t.Error("the full match let two absences through")
	}
}
