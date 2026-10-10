package adapter

import (
	"slices"
	"strings"
	"testing"
)

func aPatch() PatchApplication {
	return PatchApplication{
		IssueNid:  3603341,
		Name:      "3603341-3-d12.patch",
		LocalPath: "/cockpit/cache/patches/3603341/3603341-3-d12.patch",
	}
}

// The escalation is straight, then three-way, then reduced context — and the
// third rung is load-bearing, because without it essentially every Project
// Update Bot patch reads as stale.
func TestTheApplyEscalationKeepsGettingLooserAboutContextOnly(t *testing.T) {
	path := aPatch().LocalPath

	straight := ApplyArgs(path)
	threeWay := ThreeWayApplyArgs(path)
	reduced := ReducedContextApplyArgs(path)

	for name, args := range map[string][]string{
		"straight": straight, "three-way": threeWay, "reduced context": reduced,
	} {
		// The checks must run against a clean tree, so every rung that leads
		// to a commit stages what it applies.
		if !slices.Contains(args, "--index") {
			t.Errorf("%s does not stage what it applies: %v", name, args)
		}
		// drupal.org patches are git diff output taken at the repository root.
		if !slices.Contains(args, "-p1") {
			t.Errorf("%s does not apply at -p1: %v", name, args)
		}
		if args[len(args)-1] != path {
			t.Errorf("%s does not end with the patch: %v", name, args)
		}
	}

	if slices.Contains(straight, "--3way") || slices.Contains(straight, "-C1") {
		t.Errorf("the first attempt is not exact: %v", straight)
	}
	if !slices.Contains(threeWay, "--3way") {
		t.Errorf("the second attempt is not a three-way merge: %v", threeWay)
	}
	if !slices.Contains(reduced, "-C1") {
		t.Errorf("the third attempt does not reduce the context: %v", reduced)
	}
	// Reduced context loosens what has to *surround* a hunk, never what has to
	// match inside it.
	if slices.Contains(reduced, "--ignore-whitespace") || slices.Contains(reduced, "--unidiff-zero") {
		t.Errorf("the third attempt loosens what has to match: %v", reduced)
	}
}

// The point of the reject apply is to hand back a working copy somebody is
// about to edit, so staging half of it would put them in a state where
// `git diff` hides the very changes they came to look at.
func TestTheRejectApplyStagesNothing(t *testing.T) {
	args := RejectApplyArgs(aPatch().LocalPath)

	if slices.Contains(args, "--index") {
		t.Errorf("the reject apply stages what it applies: %v", args)
	}
	if !slices.Contains(args, "--reject") {
		t.Errorf("not a reject apply: %v", args)
	}
}

// Per file rather than stopping at the first failure — the difference between
// "this patch is stale" and "one of its nine files is stale".
func TestTheCheckReportsPerFile(t *testing.T) {
	args := CheckArgs(aPatch().LocalPath)

	if !slices.Contains(args, "--check") || !slices.Contains(args, "-v") {
		t.Errorf("the check is not verbose and dry: %v", args)
	}
}

// An issue routinely carries several re-rolls, and the working copy should say
// which one it holds.
func TestTheCommitMessageNamesTheFileNotJustTheIssue(t *testing.T) {
	message := PatchCommitMessage(aPatch())

	if !strings.Contains(message, "3603341-3-d12.patch") {
		t.Errorf("message %q does not name the file", message)
	}
	if !strings.Contains(message, "#3603341") {
		t.Errorf("message %q does not name the issue", message)
	}
	if !strings.Contains(message, "[upkeep]") {
		t.Errorf("message %q does not say what wrote it", message)
	}
}

const checkOutputStale = `Checking patch pathauto.info.yml...
error: while searching for:
name: Pathauto
type: module
core_version_requirement: ^10 || ^11
package: Other
error: patch failed: pathauto.info.yml:1
error: pathauto.info.yml: patch does not apply
Checking patch src/PathautoGenerator.php...
error: patch failed: src/PathautoGenerator.php:412
error: src/PathautoGenerator.php: patch does not apply
`

const statOutput = ` pathauto.info.yml            |  2 +-
 src/PathautoGenerator.php    |  8 ++++----
 src/Form/SettingsForm.php    |  3 ++-
 3 files changed, 7 insertions(+), 6 deletions(-)
`

// "One of these nine files" is actionable; "the patch failed" is not.
func TestTheRefusalNamesWhichFilesAreStale(t *testing.T) {
	err := UnappliablePatchError(aPatch(), "2.0.x", statOutput, checkOutputStale, "pathauto")

	message := err.Error()
	for _, want := range []string{
		"3603341-3-d12.patch",
		"2.0.x",
		"It changes 3 file(s); 1 apply, 2 do not:",
		"  pathauto.info.yml",
		"  src/PathautoGenerator.php",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, message)
		}
	}
	// A file that applied is not listed as failing.
	if strings.Contains(message, "  src/Form/SettingsForm.php") {
		t.Errorf("a file that applies was listed as failing:\n%s", message)
	}
}

// What git could not find is what tells a maintainer whether the file moved on
// or the patch was cut against something else entirely.
func TestTheRefusalQuotesWhatGitLookedFor(t *testing.T) {
	err := UnappliablePatchError(aPatch(), "2.0.x", statOutput, checkOutputStale, "pathauto")

	message := err.Error()
	if !strings.Contains(message, "git looked for this and did not find it:") {
		t.Errorf("the context is not quoted:\n%s", message)
	}
	if !strings.Contains(message, "  core_version_requirement: ^10 || ^11") {
		t.Errorf("the context is not indented under the heading:\n%s", message)
	}
}

// Every refusal ends in something you can paste, and promote is the one patch
// command whose branch survives the next apply.
func TestTheRefusalEndsInTheRerollCommand(t *testing.T) {
	err := UnappliablePatchError(aPatch(), "2.0.x", statOutput, checkOutputStale, "pathauto")

	if !strings.Contains(err.Error(), "upkeep patch:promote pathauto 3603341 --partial") {
		t.Errorf("the refusal does not name the way forward:\n%s", err)
	}
}

const checkOutputTarball = `Checking patch static_setting_contexts.info.yml...
error: while searching for:
core_version_requirement: ^10 || ^11

; Information added by Drupal.org packaging script on 2025-08-14
version: '1.0.1'
project: 'static_setting_contexts'
datestamp: 1755185000
error: patch failed: static_setting_contexts.info.yml:1
error: static_setting_contexts.info.yml: patch does not apply
`

// The two failures give opposite advice. Telling somebody to re-roll against
// "1.0.x" sends them looking for changes that were never made.
//
// Found on static_setting_contexts #3603341, where the report blamed a file
// that had not moved.
func TestAPatchCutFromAReleaseTarballIsDiagnosedAsSuch(t *testing.T) {
	if !CutFromReleaseTarball(checkOutputTarball) {
		t.Fatal("not recognised as cut from a tarball")
	}
	if CutFromReleaseTarball(checkOutputStale) {
		t.Error("an ordinary stale patch was blamed on the packaging script")
	}

	err := UnappliablePatchError(aPatch(), "1.0.x", statOutput, checkOutputTarball, "static_setting_contexts")
	message := err.Error()

	if !strings.Contains(message, "cut against a release tarball") {
		t.Errorf("the diagnosis is missing:\n%s", message)
	}
	if !strings.Contains(message, "will not help until it is regenerated from a checkout") {
		t.Errorf("the refusal does not say what would help:\n%s", message)
	}
	if strings.Contains(message, "has moved on since the patch was cut") {
		t.Errorf("it also gave the opposite advice:\n%s", message)
	}
}

// Either marker on its own is enough: the packaging comment and the datestamp
// line arrive together in practice, but a truncated context can carry one.
func TestEitherPackagingMarkerIsEnough(t *testing.T) {
	for name, output := range map[string]string{
		"the comment":   "error: while searching for:\n; Information added by Drupal.org packaging script on 2025-08-14\n",
		"the datestamp": "error: while searching for:\ndatestamp: 1755185000\n",
		"indented":      "error: while searching for:\n  datestamp: 1755185000\n",
	} {
		if !CutFromReleaseTarball(output) {
			t.Errorf("%s alone was not recognised", name)
		}
	}

	// And a datestamp that is not a packaging line is not one.
	if CutFromReleaseTarball("datestamp: soon") {
		t.Error("a non-numeric datestamp was read as the packaging script's")
	}
}

const rejectOutput = `Checking patch pathauto.info.yml...
Applied patch pathauto.info.yml cleanly.
Checking patch src/PathautoGenerator.php...
Applying patch src/PathautoGenerator.php with 2 rejects...
Hunk #1 applied cleanly.
Rejected hunk #2.
Applying patch src/Form/SettingsForm.php with 1 reject...
`

// A partial apply leaves the work of re-rolling rather than a description of
// it, so both halves have to be reportable.
func TestTheRejectApplyOutputSeparatesWhatFitFromWhatDidNot(t *testing.T) {
	clean := CleanlyApplied(rejectOutput)
	rejected := RejectedFiles(rejectOutput)

	if !slices.Equal(clean, []string{"pathauto.info.yml"}) {
		t.Errorf("cleanly applied: %v", clean)
	}
	if !slices.Equal(rejected, []string{"src/PathautoGenerator.php", "src/Form/SettingsForm.php"}) {
		t.Errorf("rejected: %v", rejected)
	}
}

// git repeats itself; the report should not.
func TestRepeatedFilesAreListedOnce(t *testing.T) {
	repeated := strings.Repeat("error: patch failed: pathauto.info.yml:1\n", 3) +
		"error: patch failed: pathauto.info.yml:40\n"

	if got := FailedFiles(repeated); !slices.Equal(got, []string{"pathauto.info.yml"}) {
		t.Errorf("got %v", got)
	}
}

func TestOutputThatSaysNothingYieldsNothing(t *testing.T) {
	for _, output := range []string{"", "Checking patch x...\n", "fatal: unrecognised input"} {
		if got := FailedFiles(output); len(got) != 0 {
			t.Errorf("FailedFiles(%q) = %v", output, got)
		}
		if got := RejectedFiles(output); len(got) != 0 {
			t.Errorf("RejectedFiles(%q) = %v", output, got)
		}
		if got := CleanlyApplied(output); len(got) != 0 {
			t.Errorf("CleanlyApplied(%q) = %v", output, got)
		}
		if got := TouchedFiles(output); got != 0 {
			t.Errorf("TouchedFiles(%q) = %d", output, got)
		}
		if got := SearchedContext(output); got != "" {
			t.Errorf("SearchedContext(%q) = %q", output, got)
		}
	}
}

func TestASingleFileStatIsCountedToo(t *testing.T) {
	if got := TouchedFiles(" pathauto.info.yml | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n"); got != 1 {
		t.Errorf("got %d", got)
	}
}

// The branch is keyed by issue, so a newer re-roll replaces it rather than
// stacking on the last one.
func TestThePatchBranchIsTheIssuesOwn(t *testing.T) {
	if got := aPatch().BranchName(); got != ManagedBranchForPatch(3603341) {
		t.Errorf("got %q", got)
	}
}

// git can name more failing files than --stat counted — a patch touching a
// file twice, or a rename reported under both names — and "3 file(s); -1
// apply" is worse than saying nothing.
func TestTheAppliedCountNeverGoesNegative(t *testing.T) {
	// One file in the stat, three named as failing.
	oneFileStat := " x.php | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n"
	manyFailed := "error: patch failed: a.php:1\nerror: patch failed: b.php:1\nerror: patch failed: c.php:1\n"

	message := UnappliablePatchError(aPatch(), "2.0.x", oneFileStat, manyFailed, "pathauto").Error()

	if strings.Contains(message, "-") && strings.Contains(message, "apply,") {
		for _, line := range strings.Split(message, "\n") {
			if strings.Contains(line, "apply,") && strings.Contains(line, "-") {
				t.Errorf("the count went negative: %q", line)
			}
		}
	}
	if !strings.Contains(message, "0 apply") {
		t.Errorf("the count was not clamped:\n%s", message)
	}
}
