package adapter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/owenbush/upkeep/internal/drupal"
)

// The git.drupalcode.org remote, in its two forms.
//
// Cloning happens over HTTPS because anonymous read needs no credentials and
// every public contrib project answers it — checking a merge request, applying
// a patch and running the suite all work with no key and no account, and that
// must stay true.
//
// Pushing is SSH, to a URL **GitLab itself supplies** (ssh_url_to_repo) and
// never one assembled here. That is not fussiness: git.drupalcode.org serves
// the web and the API, and the SSH remote it advertises is on git.drupal.org.
// A URL built by swapping the scheme on the host you fetched from points
// somewhere that does not answer.
//
// SSH rather than the PAT because **upkeep never handles a credential for
// git**. Every way of feeding git a token writes it to disk (.git/config, a
// credential store) or exposes it in process argv where ps can read it, and
// each would be a second exception to the rule that UPKEEP_GITLAB_TOKEN never
// reaches a child process. The operator's agent answers instead.
const (
	HTTPSBase = "https://git.drupalcode.org/"
	// SSHKeyURL is where drupal.org takes SSH keys, named in the failure that
	// needs it.
	SSHKeyURL = "https://git.drupalcode.org/-/user_settings/ssh_keys"
	// defaultSSHHost is only a fallback for messages; real URLs come from the
	// API.
	defaultSSHHost = "git@git.drupal.org"
)

// HTTPSURL is the anonymous clone URL for a namespaced project path.
func HTTPSURL(project string) string { return HTTPSBase + project + ".git" }

var sshHost = regexp.MustCompile(`^([^@\s]+@[^:\s]+):`)

// SSHHostOf is the host part of an SSH remote, for telling somebody what to
// try `ssh -T` against.
//
// Worth extracting rather than hardcoding: git.drupalcode.org serves the web
// and the API, but the SSH remote GitLab advertises is on git.drupal.org.
// Naming the wrong one in a recovery instruction sends people to test a host
// that was never the problem.
func SSHHostOf(sshURL string) string {
	if match := sshHost.FindStringSubmatch(strings.TrimSpace(sshURL)); match != nil {
		return match[1]
	}

	return defaultSSHHost
}

// GitRemote is a git remote to push to: a name and a URL.
//
// It exists because "push the work branch" stopped being a question with one
// answer. Contributing to Drupal does not put branches on the canonical
// project — drupal.org mints an issue fork at issue/<machine-name>-<nid>, the
// branch goes there, and the merge request is opened across projects into the
// canonical repository. Measured on pathauto: 100 of 100 open merge requests
// come from a fork, none from the project itself. So the destination is
// decided by the command that knows about issues and forks, and handed to the
// adapter, which only has to put a branch where it is told.
//
// Origin stays exactly as cloned: HTTPS, anonymous, read-only in practice.
type GitRemote struct {
	Name string
	URL  string
}

// IssueForkRemote is the remote for an issue fork, named after the issue
// rather than something generic.
//
// A working copy is reused across issues — the same (module x core)
// environment serves every one of them — so a single "fork" remote would be
// silently re-pointed each time and a push could land on the fork for whatever
// issue happened to be published last.
//
// sshURL is GitLab's own ssh_url_to_repo.
func IssueForkRemote(issueNid int, sshURL string) GitRemote {
	return GitRemote{Name: "issue-" + strconv.Itoa(issueNid), URL: sshURL}
}

// Why a push was refused, and what to do about it.
//
// There are two refusals and they need opposite answers, which is the whole
// reason this is not one message:
//
//   - **Authentication** — the server does not know who you are. Your SSH key
//     is missing, wrong, or not loaded in the agent.
//   - **Authorization** — the server knows exactly who you are and will not
//     let you write here. On drupal.org this is the ordinary state of a fresh
//     issue fork: creating it does not grant you push access, and there is a
//     separate button on the issue page that does.
//
// They read almost alike in git's output and the fix for one is useless for
// the other. Telling somebody with an authorization problem to check their SSH
// key sends them to debug something that already works.
//
// git's own text is always kept above the guidance. It suggests a password,
// which GitLab will never accept, but hiding what actually happened is worse
// than including an unhelpful line.
var (
	// authorizationRefusal is GitLab's wording when the key is fine and the
	// account is not allowed.
	authorizationRefusal = regexp.MustCompile(`(?i)not allowed to push|pre-receive hook declined|insufficient permission`)
	// authenticationRefusal is its wording when it does not know who is asking
	// at all.
	authenticationRefusal = regexp.MustCompile(`(?i)Access denied|Authentication failed|Permission denied|publickey`)
)

// ExplainPushRefusal builds the message for a refused push.
func ExplainPushRefusal(branch IssueBranch, remote GitRemote, output string) string {
	preamble := fmt.Sprintf(
		"Pushing %q to %s was refused:\n%s", branch.Name, remote.URL, strings.TrimSpace(output),
	)

	// Order matters: an authorization refusal can carry the word "denied" too,
	// and it is the more specific diagnosis.
	if authorizationRefusal.MatchString(output) {
		return preamble + "\n\n" + PushAuthorizationHelp(branch.IssueNid)
	}
	if authenticationRefusal.MatchString(output) {
		return preamble + "\n\n" + authenticationHelp(remote)
	}

	return preamble
}

// PushAuthorizationHelp is the guidance for "GitLab knows you and says no".
//
// Creating an issue fork and being allowed to push to it are two separate
// grants on drupal.org, and the second is a button most people meet only when
// a push has already failed.
func PushAuthorizationHelp(issueNid int) string {
	return fmt.Sprintf(
		"Your SSH key worked — GitLab knows who you are and will not let you write to this fork.\n"+
			"On drupal.org, creating an issue fork does not grant push access to it; that is a separate\n"+
			"button on the issue.\n\n"+
			"  1. Open %s\n"+
			"  2. In the merge-request section, click the button granting push access to the issue fork\n"+
			"     (\"Get push access\", beside the fork it names)\n"+
			"  3. Re-run the publish\n\n"+
			"If the fork is somebody else's and you are not a maintainer, the access is theirs to give.",
		drupal.IssueURL(issueNid),
	)
}

// authenticationHelp is the guidance for "GitLab does not know who you are".
func authenticationHelp(remote GitRemote) string {
	return fmt.Sprintf(
		"upkeep pushes over SSH and never hands git a password or a token, so this is your SSH key.\n"+
			"  - Add one at %s\n"+
			"  - Check it works:  ssh -T %s\n"+
			"  - Make sure the agent has it:  ssh-add -l",
		SSHKeyURL, SSHHostOf(remote.URL),
	)
}

// QuoteShellArgument is POSIX single-quoting for a value interpolated into a
// shell script body.
//
// A few engine invocations must be a `bash -c <script>` because the engine's
// own commands assume a layout this tool does not use. The argv stays
// list-form, so nothing can be injected into the *argument vector* — but the
// script text itself is shell, and any value spliced into it is shell too.
//
// Hand-written rather than taken from a library: the script always runs in a
// Linux container, so the quoting must be POSIX regardless of the host it is
// built on. PHP's escapeshellarg double-quotes on Windows, which is the same
// reason the PHP side does not use it either.
func QuoteShellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
