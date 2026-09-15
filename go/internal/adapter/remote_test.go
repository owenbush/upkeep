package adapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Telling somebody with an authorization problem to check their SSH key sends
// them to debug something that already works. The two refusals read almost
// alike in git's output and the fix for one is useless for the other.
func TestTheTwoRefusalsGetOppositeAnswers(t *testing.T) {
	branch := IssueBranchFor(3603341, "Drupal 12 compatibility")
	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")

	authorization := []string{
		"remote: GitLab: You are not allowed to push code to this project.",
		"remote: error: pre-receive hook declined",
		"remote: insufficient permission for adding an object to repository database",
	}
	for _, output := range authorization {
		message := ExplainPushRefusal(branch, remote, output)
		if !strings.Contains(message, "Your SSH key worked") {
			t.Errorf("%q was diagnosed as an SSH problem:\n%s", output, message)
		}
		if !strings.Contains(message, "Get push access") {
			t.Errorf("%q does not name the button that fixes it", output)
		}
		if strings.Contains(message, "ssh-add -l") {
			t.Errorf("%q sends the operator to debug a key that works", output)
		}
	}

	authentication := []string{
		"git@git.drupal.org: Permission denied (publickey).",
		"Access denied. Authentication failed.",
	}
	for _, output := range authentication {
		message := ExplainPushRefusal(branch, remote, output)
		if !strings.Contains(message, "ssh-add -l") {
			t.Errorf("%q was not diagnosed as an SSH problem:\n%s", output, message)
		}
		if strings.Contains(message, "Get push access") {
			t.Errorf("%q sends the operator to a button that will not help", output)
		}
	}
}

// An authorization refusal can carry the word "denied" too, and it is the more
// specific diagnosis — so it is matched first.
func TestAnAuthorizationRefusalCarryingDeniedIsStillAuthorization(t *testing.T) {
	branch := IssueBranchFor(3603341, "t")
	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")

	output := "remote: Permission denied\nremote: GitLab: You are not allowed to push code to this project."

	message := ExplainPushRefusal(branch, remote, output)
	if !strings.Contains(message, "Your SSH key worked") {
		t.Errorf("the more specific diagnosis lost:\n%s", message)
	}
}

// git's own text is always kept above the guidance. It suggests a password,
// which GitLab will never accept, but hiding what actually happened is worse
// than including an unhelpful line.
func TestGitsOwnWordsAreAlwaysKept(t *testing.T) {
	branch := IssueBranchFor(3603341, "t")
	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")

	for _, output := range []string{
		"remote: GitLab: You are not allowed to push code to this project.",
		"git@git.drupal.org: Permission denied (publickey).",
		"something nobody has seen before",
	} {
		message := ExplainPushRefusal(branch, remote, output)
		if !strings.Contains(message, output) {
			t.Errorf("git's output was dropped from:\n%s", message)
		}
		if !strings.Contains(message, remote.URL) {
			t.Errorf("the message does not say where the push went:\n%s", message)
		}
		if !strings.Contains(message, branch.Name) {
			t.Errorf("the message does not say what was pushed:\n%s", message)
		}
	}
}

// An unrecognised refusal gets no guessed diagnosis.
func TestAnUnrecognisedRefusalIsNotGuessedAt(t *testing.T) {
	branch := IssueBranchFor(3603341, "t")
	remote := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")

	message := ExplainPushRefusal(branch, remote, "fatal: the remote end hung up unexpectedly")

	for _, guess := range []string{"Your SSH key worked", "ssh-add -l", "Get push access"} {
		if strings.Contains(message, guess) {
			t.Errorf("an unrecognised refusal was diagnosed as %q:\n%s", guess, message)
		}
	}
}

// git.drupalcode.org serves the web and the API, but the SSH remote GitLab
// advertises is on git.drupal.org. Naming the wrong one sends people to test a
// host that was never the problem.
func TestTheSshHostComesFromTheRemoteNotTheWebHost(t *testing.T) {
	for url, want := range map[string]string{
		"git@git.drupal.org:issue/pathauto-3603341.git":   "git@git.drupal.org",
		"  git@git.drupal.org:project/pathauto.git  ":     "git@git.drupal.org",
		"someone@gitlab.example.test:group/project.git":   "someone@gitlab.example.test",
		"https://git.drupalcode.org/project/pathauto.git": "git@git.drupal.org",
		"":                 "git@git.drupal.org",
		"not a url at all": "git@git.drupal.org",
	} {
		if got := SSHHostOf(url); got != want {
			t.Errorf("SSHHostOf(%q) = %q, want %q", url, got, want)
		}
	}
}

// A working copy is reused across issues, so a single "fork" remote would be
// silently re-pointed and a push could land on the wrong fork.
func TestEachIssueGetsItsOwnRemoteName(t *testing.T) {
	first := IssueForkRemote(3603341, "git@git.drupal.org:issue/pathauto-3603341.git")
	second := IssueForkRemote(3597808, "git@git.drupal.org:issue/pathauto-3597808.git")

	if first.Name == second.Name {
		t.Errorf("both issues use the remote %q", first.Name)
	}
	if first.Name != "issue-3603341" {
		t.Errorf("got %q", first.Name)
	}
}

// The push URL is GitLab's own, never assembled: a URL built by swapping the
// scheme on the host you fetched from points somewhere that does not answer.
func TestThePushUrlIsWhateverGitlabSupplied(t *testing.T) {
	remote := IssueForkRemote(1, "git@somewhere.else:x/y.git")

	if remote.URL != "git@somewhere.else:x/y.git" {
		t.Errorf("the supplied URL was rewritten to %q", remote.URL)
	}
}

func TestTheCloneUrlIsAnonymousHttps(t *testing.T) {
	got := HTTPSURL("project/pathauto")

	if got != "https://git.drupalcode.org/project/pathauto.git" {
		t.Errorf("got %q", got)
	}
	if strings.Contains(got, "@") {
		t.Errorf("the clone URL carries a credential: %q", got)
	}
}

// The script text is shell, and any value spliced into it is shell too. This
// is what stops that.
func TestQuotingSurvivesARealShell(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}

	for _, value := range []string{
		"plain",
		"with space",
		"it's got an apostrophe",
		`; rm -rf /`,
		`$(echo pwned)`,
		"`echo pwned`",
		`$HOME`,
		`"double"`,
		`back\slash`,
		"new\nline",
		"tab\there",
		"*",
		"a'b'c",
		`'''`,
		"",
	} {
		// printf %s so the shell echoes exactly one word back, whatever it is.
		script := "printf %s " + QuoteShellArgument(value)
		out, err := exec.Command(bash, "-c", script).Output()
		if err != nil {
			t.Errorf("%q: bash refused the script %q: %v", value, script, err)

			continue
		}
		if string(out) != value {
			t.Errorf("%q came back as %q (script was %s)", value, out, script)
		}
	}
}

// And nothing interpolated can start a second command.
//
// Detected by whether the injected command *ran*, not by whether its text
// appears in the output — printf echoing the literal string back is the
// quoting working, which is what the first version of this test mistook for a
// breach.
func TestQuotingCannotStartASecondCommand(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}

	marker := filepath.Join(t.TempDir(), "injected")

	for _, payload := range []string{
		"x; touch " + marker,
		"x && touch " + marker,
		"x`touch " + marker + "`",
		"x$(touch " + marker + ")",
		"x\ntouch " + marker,
	} {
		script := "printf %s " + QuoteShellArgument(payload)
		if err := exec.Command(bash, "-c", script).Run(); err != nil {
			t.Errorf("%q: bash refused the script: %v", payload, err)

			continue
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%q ran a second command (script was %s)", payload, script)
		}
	}
}
