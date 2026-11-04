// Package drupalorgcli is the single way upkeep runs drupalorg-cli
// (github.com/mglaman/drupalorg-cli).
//
// It finds the binary (UPKEEP_DRUPALORG_BIN, else `drupalorg` on PATH),
// refuses anything older than MinVersion once per process, runs the command
// exactly as the caller built it, hands the child the GitLab token through
// DRUPALORG_GITLAB_TOKEN only, and reports every non-success as a
// *gitlab.Failure so callers keep switching on conditions, not strings.
//
// Nothing here decodes the CLI's JSON, and that is a finding rather than an
// omission: plan 1 compared every merge-request read against the native
// client and none reached parity (missing fields, invalid JSON for a title
// containing '<'), so upkeep only runs the CLI for actions it has no native
// path for, and relays their text.
//
// # Token handling
//
// The token comes from gitlab.TokenResolver and travels only in the child's
// environment, never in argv, and only when one resolved. Children start
// through proc.Exec, the one sanctioned credential hand-over: the child sees
// DRUPALORG_GITLAB_TOKEN and not UPKEEP_GITLAB_TOKEN, and the token is
// redacted from both streams before this package reads them, so no failure
// message can quote it.
//
// # Failure mapping
//
// The CLI has no structured error channel: it prints a message on stdout or
// stderr and exits non-zero. Guzzle's own errors name the HTTP status
// ("... resulted in a `404 Not Found` response"); the CLI's own checks print
// prose ("Project  not found."). Rules are tried in order, first match wins:
//
//	Condition                                         Kind               Status
//	------------------------------------------------  -----------------  ------
//	binary missing, exec fails, PHP missing, too old  TransportError     0
//	context cancelled or deadline exceeded            TransportError     0
//	killed by a signal (no exit status)               TransportError     0
//	non-zero exit, output names HTTP 401              Unauthorized       401
//	non-zero exit, output names HTTP 403              EndpointClosed     403
//	non-zero exit, output names HTTP 404              NotFound           404
//	non-zero exit, output names HTTP 429              RateLimited        429
//	non-zero exit, output names any other HTTP status RequestRejected    status
//	non-zero exit, cURL / DNS / connection error      TransportError     0
//	non-zero exit, "unauthorized", "invalid token"…   Unauthorized       0
//	non-zero exit, "not found"                        NotFound           0
//	any other non-zero exit                           RequestRejected    0
//
// Status is set only when the output actually named one.
package drupalorgcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/proc"
)

const (
	// BinEnvVar overrides where the binary is found.
	BinEnvVar = "UPKEEP_DRUPALORG_BIN"
	// TokenEnvVar is where drupalorg-cli reads its GitLab token from.
	TokenEnvVar = "DRUPALORG_GITLAB_TOKEN"
	// MinVersion is the oldest release upkeep has been verified against.
	MinVersion = "0.10.3"
	// DefaultMaxConcurrent matches DrupalOrgClient::MAX_CONCURRENT.
	DefaultMaxConcurrent = 8

	binaryName      = "drupalorg"
	installGuidance = "Install or upgrade drupalorg-cli " + MinVersion + " or newer (it needs PHP): " +
		"download drupalorg.phar from https://github.com/mglaman/drupalorg-cli/releases, make it " +
		"executable as `" + binaryName + "` on your PATH, or set " + BinEnvVar + " to its path."
	versionTimeout = 30 * time.Second
	// detailLimit bounds how much child output a failure message quotes.
	detailLimit = 500
)

// Runner runs drupalorg-cli commands with bounded concurrency.
type Runner struct {
	resolver *gitlab.TokenResolver
	sem      chan struct{}

	tokenOnce sync.Once
	token     string
}

// New builds a runner. resolver may be nil (no token is passed, and the CLI
// falls back to glab's); maxConcurrent <= 0 takes DefaultMaxConcurrent.
func New(resolver *gitlab.TokenResolver, maxConcurrent int) *Runner {
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrent
	}

	return &Runner{resolver: resolver, sem: make(chan struct{}, maxConcurrent)}
}

// RunRaw runs `drupalorg <args...>` and returns its stdout.
func (r *Runner) RunRaw(ctx context.Context, args []string) ([]byte, *gitlab.Failure) {
	return r.run(ctx, "", args)
}

func (r *Runner) run(ctx context.Context, dir string, args []string) ([]byte, *gitlab.Failure) {
	bin, failure := gate.check()
	if failure != nil {
		return nil, failure
	}

	// Asked before the slot, so an already-cancelled run never races a free
	// slot against ctx.Done() in the select below.
	if ctx.Err() != nil {
		return nil, cancelled(args, ctx.Err())
	}

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, cancelled(args, ctx.Err())
	}

	var credentials map[string]string
	if token := r.resolvedToken(); token != "" {
		credentials = map[string]string{TokenEnvVar: token}
	}

	outcome := proc.ExecIn(ctx, dir, append([]string{bin}, args...), credentials)
	if ctx.Err() != nil {
		return nil, cancelled(args, ctx.Err())
	}
	if outcome.Err == nil {
		return outcome.Stdout, nil
	}
	if outcome.ExitCode == nil {
		return nil, &gitlab.Failure{
			Kind:    gitlab.TransportError,
			Message: fmt.Sprintf("drupalorg %s did not complete: %v", commandName(args), outcome.Err),
		}
	}

	return nil, r.classify(args, *outcome.ExitCode, joinOutput(outcome.Stderr, outcome.Stdout))
}

func (r *Runner) resolvedToken() string {
	r.tokenOnce.Do(func() {
		if r.resolver != nil {
			r.token = r.resolver.Resolve()
		}
	})

	return r.token
}

// excerpt is child output short enough to quote. proc.Exec has already
// redacted it, before any truncation could leave part of a token behind; this
// collapses whitespace (the CLI's error box is mostly padding) and bounds it.
func excerpt(output []byte) string {
	text := strings.Join(strings.Fields(string(output)), " ")
	if runes := []rune(text); len(runes) > detailLimit {
		// Cut on runes, never bytes, so a multi-byte character is not split.
		text = string(runes[:detailLimit]) + "…"
	}

	return text
}

func (r *Runner) sources() string {
	if r.resolver != nil {
		return r.resolver.DescribeSources()
	}

	return gitlab.DescribeDefaultSources()
}

// rule is one row of the failure-mapping table. match reports whether it
// applies and any HTTP status the output named.
type rule struct {
	match func(output string) (status int, ok bool)
	build func(r *Runner, cmd string, status int, detail string) *gitlab.Failure
}

var (
	guzzleStatus = regexp.MustCompile("resulted in a `(\\d{3})")
	networkError = regexp.MustCompile(`(?i)curl error|could not resolve host|connection (refused|reset|timed out)|operation timed out`)
	authError    = regexp.MustCompile(`(?i)\b401\b|unauthori[sz]ed|authentication (failed|required)|invalid (token|credentials)`)
	notFoundText = regexp.MustCompile(`(?i)not found`)
)

func matches(re *regexp.Regexp) func(string) (int, bool) {
	return func(output string) (int, bool) { return 0, re.MatchString(output) }
}

// rules is the table documented in the package comment, in order.
var rules = []rule{
	{
		match: func(output string) (int, bool) {
			m := guzzleStatus.FindStringSubmatch(output)
			if m == nil {
				return 0, false
			}
			status, _ := strconv.Atoi(m[1])

			return status, true
		},
		build: httpFailure,
	},
	{
		match: matches(networkError),
		build: func(_ *Runner, cmd string, _ int, detail string) *gitlab.Failure {
			return &gitlab.Failure{
				Kind:    gitlab.TransportError,
				Message: fmt.Sprintf("drupalorg %s could not reach the server: %s", cmd, detail),
			}
		},
	},
	{match: matches(authError), build: unauthorized},
	{
		match: matches(notFoundText),
		build: func(_ *Runner, cmd string, _ int, detail string) *gitlab.Failure {
			return &gitlab.Failure{Kind: gitlab.NotFound, Message: fmt.Sprintf("drupalorg %s: %s", cmd, detail)}
		},
	},
}

// classify maps a non-zero exit onto a failure kind using rules.
func (r *Runner) classify(args []string, exitCode int, output []byte) *gitlab.Failure {
	detail := excerpt(output)
	cmd := commandName(args)
	for _, row := range rules {
		if status, ok := row.match(detail); ok {
			return row.build(r, cmd, status, detail)
		}
	}

	if detail == "" {
		detail = "no output"
	}

	return &gitlab.Failure{
		Kind:    gitlab.RequestRejected,
		Message: fmt.Sprintf("drupalorg %s failed (exit %d): %s", cmd, exitCode, detail),
	}
}

func httpFailure(r *Runner, cmd string, status int, detail string) *gitlab.Failure {
	var failure *gitlab.Failure
	switch status {
	case 401:
		failure = unauthorized(r, cmd, status, detail)
	case 403:
		failure = &gitlab.Failure{
			Kind:    gitlab.EndpointClosed,
			Message: fmt.Sprintf("drupalorg %s: endpoint closed to API access (HTTP 403): %s", cmd, detail),
		}
	case 404:
		failure = &gitlab.Failure{
			Kind:    gitlab.NotFound,
			Message: fmt.Sprintf("drupalorg %s: resource not found (HTTP 404): %s", cmd, detail),
		}
	case 429:
		failure = &gitlab.Failure{
			Kind:    gitlab.RateLimited,
			Message: fmt.Sprintf("drupalorg %s: rate limited (HTTP 429). Please retry later.", cmd),
		}
	default:
		failure = &gitlab.Failure{
			Kind:    gitlab.RequestRejected,
			Message: fmt.Sprintf("drupalorg %s: request rejected (HTTP %d): %s", cmd, status, detail),
		}
	}
	failure.Status = status

	return failure
}

// unauthorized names where the token is read from, never the token.
func unauthorized(r *Runner, cmd string, status int, _ string) *gitlab.Failure {
	return &gitlab.Failure{
		Kind: gitlab.Unauthorized,
		Message: fmt.Sprintf(
			"drupalorg %s: GitLab rejected the credential: the token is missing, invalid, expired, or "+
				"lacks the required scope. Re-check the token configured in: %s. "+
				"(The token is never printed or logged.)",
			cmd, r.sources(),
		),
		Status: status,
	}
}

func cancelled(args []string, err error) *gitlab.Failure {
	return &gitlab.Failure{
		Kind:    gitlab.TransportError,
		Message: fmt.Sprintf("drupalorg %s was stopped before it finished: %v", commandName(args), err),
	}
}

func commandName(args []string) string {
	if len(args) == 0 {
		return "(no command)"
	}

	return args[0]
}

func joinOutput(stderr, stdout []byte) []byte {
	return append(append(slices.Clone(stderr), '\n'), stdout...)
}

// versionGate locates the binary and checks its version, once per process.
type versionGate struct {
	once    sync.Once
	bin     string
	failure *gitlab.Failure
}

var gate = &versionGate{}

var versionLine = regexp.MustCompile(`Drupal\.org CLI v?(\d+)\.(\d+)\.(\d+)`)

func (g *versionGate) check() (string, *gitlab.Failure) {
	g.once.Do(func() { g.bin, g.failure = locateAndVerify() })

	return g.bin, g.failure
}

func gateFailure(format string, args ...any) *gitlab.Failure {
	return &gitlab.Failure{Kind: gitlab.TransportError, Message: fmt.Sprintf(format, args...) + " " + installGuidance}
}

func locateAndVerify() (string, *gitlab.Failure) {
	bin, source := os.Getenv(BinEnvVar), BinEnvVar
	if bin == "" {
		found, err := exec.LookPath(binaryName)
		if err != nil {
			return "", gateFailure("drupalorg-cli not found: %s is unset and `%s` is not on PATH.", BinEnvVar, binaryName)
		}
		bin, source = found, "PATH"
	}

	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	// The version probe needs no credential.
	outcome := proc.Exec(ctx, []string{bin, "--version"}, nil)
	output := append(slices.Clone(outcome.Stdout), outcome.Stderr...)
	detail := excerpt(output)

	switch {
	case outcome.ExitCode != nil && *outcome.ExitCode != 0:
		return "", gateFailure(
			"drupalorg-cli at %s (from %s) failed to report its version (exit %d): %s. "+
				"If PHP is missing, install PHP and make sure `php` is on PATH.",
			bin, source, *outcome.ExitCode, detail,
		)
	case outcome.Err != nil:
		return "", gateFailure("could not run drupalorg-cli at %s (from %s): %v.", bin, source, outcome.Err)
	}

	m := versionLine.FindStringSubmatch(string(output))
	if m == nil {
		return "", gateFailure(
			"drupalorg-cli at %s (from %s) did not print a `Drupal.org CLI X.Y.Z` version line: %s.",
			bin, source, detail,
		)
	}
	have := m[1] + "." + m[2] + "." + m[3]
	if !atLeast(m[1:], strings.Split(MinVersion, ".")) {
		return "", gateFailure("drupalorg-cli at %s (from %s) is version %s; upkeep needs %s or newer.",
			bin, source, have, MinVersion)
	}

	return bin, nil
}

// atLeast compares dotted numeric versions part by part.
func atLeast(have, want []string) bool {
	for i := range want {
		h, _ := strconv.Atoi(have[i])
		w, _ := strconv.Atoi(want[i])
		if h != w {
			return h > w
		}
	}

	return true
}
