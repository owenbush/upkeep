package gitlab

import "fmt"

// Report receives an operator-facing note. It never carries token material.
type Report func(string)

// ReadOnly is a client for commands that only read.
//
// git.drupalcode.org serves every public project's merge requests, refs, forks
// and raw files without a credential, so requiring one to *look* was a
// restriction upkeep imposed rather than one GitLab does. Reading anonymously
// is the documented degraded mode: this always returns a client, and notes
// what is lost when there is no token.
//
// What is lost is real and worth saying. A private project answers an
// anonymous read with 404 rather than 401 — GitLab hides existence — so a
// module you can see while signed in reads as missing; and rate limits are
// tighter. Writing is refused by the client itself, so no command can reach a
// merge or a comment down this path by forgetting to check.
func ReadOnly(resolver *TokenResolver, report Report) *Client {
	if token := resolver.Resolve(); token != "" {
		return NewClient(nil, token, "", "")
	}

	report(AnonymousReadMessage(resolver))

	return NewClient(nil, "", "", "")
}

// ReadOnlyOr is the injected client, or a read-only one built here.
//
// The "injected in tests, resolved otherwise" pattern lives in the factory
// rather than being written out at each call site, because written inline the
// fallback arm sits on the line immediately before a live request — which
// means no offline test can reach it, and a coverage floor cannot tell a
// deliberate gap from an accident. Here it is reachable on its own.
func ReadOnlyOr(injected *Client, resolver *TokenResolver, report Report) *Client {
	if injected != nil {
		return injected
	}

	return ReadOnly(resolver, report)
}

// Authenticated is the one place a resolved token becomes a live client, so
// "how do we talk to GitLab" is decided once.
//
// report receives the shared missing-token guidance and decides how loud it
// is: the patch surface reports it as a warning because running without a
// credential is a documented degraded mode, every other command as an error.
// It returns nil when no token is configured.
func Authenticated(resolver *TokenResolver, report Report) *Client {
	token := resolver.Resolve()
	if token == "" {
		report(MissingTokenMessage(resolver))

		return nil
	}

	return NewClient(nil, token, "", "")
}

// AnonymousReadMessage is said once, in one wording, like the missing-token
// guidance it replaces for read-only commands. It never contains token
// material.
func AnonymousReadMessage(resolver *TokenResolver) string {
	return fmt.Sprintf(
		"No GitLab token configured, so this is reading git.drupalcode.org anonymously. Public projects "+
			"answer fine; a private one will read as \"not found\" rather than \"not allowed\", and rate limits "+
			"are tighter. Configure a token in %s to read as yourself — and to merge, comment or publish, "+
			"which anonymous access cannot do at all.",
		// The resolver's own sources, not the defaults: a run pointed at a
		// different env var or config file must be told about that one, or the
		// guidance sends somebody to edit a file nothing reads.
		resolver.DescribeSources(),
	)
}

// MissingTokenMessage is the one wording for "no GitLab token configured", for
// callers that report it their own way — an error, a warning, a degraded mode.
func MissingTokenMessage(resolver *TokenResolver) string {
	return fmt.Sprintf(
		"No GitLab token found. Configure one of: %s. (The token is never printed or logged.)",
		resolver.DescribeSources(),
	)
}
