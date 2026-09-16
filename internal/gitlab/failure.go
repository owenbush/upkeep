// Package gitlab reads and writes git.drupalcode.org.
package gitlab

import "fmt"

// FailureKind is the condition behind a non-success API outcome.
//
// Client methods never return a bare error for HTTP-level outcomes; they
// return a *Failure carrying one of these, so callers can switch on the
// condition rather than parse a message. The set is closed and the triggers
// are exhaustive and mutually exclusive:
//
//	Unauthorized      HTTP 401 — the token is missing, invalid, expired or unscoped
//	EndpointClosed    HTTP 403 — the instance has not opened this endpoint to tokens
//	NotFound          HTTP 404 — the resource does not exist, or is hidden from us
//	RateLimited       HTTP 429
//	RequestRejected   any other status >= 400
//	MalformedResponse a success whose body is not usable JSON
//	TransportError    no usable HTTP response at all — network, TLS, timeout
//	ResourceMissing   the call succeeded and the asked-for item was not in it
type FailureKind int

const (
	Unauthorized FailureKind = iota
	EndpointClosed
	NotFound
	RateLimited
	RequestRejected
	MalformedResponse
	TransportError
	ResourceMissing
)

// Failure is a non-success outcome.
//
// No failure ever carries credential material: messages are built from the
// status, the server's own error text, and the browser URL only.
type Failure struct {
	Kind FailureKind
	// Message is operator-facing, and never contains a token.
	Message string
	// Status is the HTTP status, when one was actually received.
	Status int
	// BrowserURL is where the maintainer can do this by hand, when known.
	BrowserURL string
	// RetryAfter is the server's requested wait, in seconds, on a 429.
	RetryAfter int
	// Resource names what was asked for, when the call succeeded without it.
	Resource string
}

func (f *Failure) Error() string { return f.Message }

// ShortCode is the compact discriminator the dashboard and command layer
// print, so no consumer re-derives the taxonomy.
func (f *Failure) ShortCode() string {
	switch f.Kind {
	case Unauthorized:
		return "401"
	case EndpointClosed:
		return "403"
	case NotFound:
		return "404"
	case RateLimited:
		return "rate-limited"
	case MalformedResponse:
		return "malformed"
	case TransportError:
		return "transport"
	case ResourceMissing:
		return "missing"
	default:
		if f.Status > 0 {
			return fmt.Sprint(f.Status)
		}

		return "rejected"
	}
}

// Transient reports whether the same request could plausibly succeed if
// retried.
//
// The per-run GET memoization keeps only non-transient outcomes, so one blip
// cannot poison a resource for the rest of the invocation.
func (f *Failure) Transient() bool {
	switch f.Kind {
	case RateLimited:
		// Transient by definition.
		return true
	case TransportError:
		// A network condition may well have cleared by the next attempt.
		return true
	case MalformedResponse:
		// Usually an interstitial or a truncated read; worth another attempt.
		return true
	case RequestRejected:
		// Transient so a 5xx blip during a long run is retried rather than
		// memoized. A genuinely stable rejection (a 400) costs one extra
		// request per resource, which is cheaper than a wrong cached verdict.
		return true
	default:
		// Unauthorized: a bad token stays bad for the whole run.
		// EndpointClosed: instance policy, not a blip.
		// NotFound, ResourceMissing: stable within a run.
		return false
	}
}

// unauthorized is a rejected credential, deliberately distinct from
// endpointClosed.
//
// A 403 on this block-by-default instance means "that endpoint is not open to
// the API, use the browser" — advice that is actively misleading for a bad
// token, where the only useful action is to re-check the credential. So this
// names the sources a token is read from, and never any token material.
func unauthorized(browserURL string) *Failure {
	return &Failure{
		Kind: Unauthorized,
		Message: fmt.Sprintf(
			"GitLab rejected the credential (HTTP 401): the token is missing, invalid, expired, or "+
				"lacks the required scope. Re-check the token configured in: %s. "+
				"(The token is never printed or logged.)",
			DescribeDefaultSources(),
		),
		Status:     401,
		BrowserURL: browserURL,
	}
}

func endpointClosed(browserURL string) *Failure {
	return &Failure{
		Kind:       EndpointClosed,
		Message:    fmt.Sprintf("Endpoint closed to API access (HTTP 403). Use the browser instead: %s", browserURL),
		Status:     403,
		BrowserURL: browserURL,
	}
}

func notFound(browserURL string) *Failure {
	return &Failure{
		Kind:       NotFound,
		Message:    fmt.Sprintf("Resource not found (HTTP 404). Check in the browser: %s", browserURL),
		Status:     404,
		BrowserURL: browserURL,
	}
}

// rateLimited carries the server's Retry-After, in seconds, when it sent one.
// retryAfter is 0 when it did not.
func rateLimited(retryAfter int, browserURL string) *Failure {
	message := "Rate limited by the GitLab instance (HTTP 429). Please retry later."
	if retryAfter > 0 {
		message = fmt.Sprintf(
			"Rate limited by the GitLab instance (HTTP 429). Please retry in %d seconds.",
			retryAfter,
		)
	}

	return &Failure{Kind: RateLimited, Message: message, Status: 429, BrowserURL: browserURL, RetryAfter: retryAfter}
}

// requestRejected quotes only the body's own message/error field, never
// request headers, so no credential can reach it.
func requestRejected(status int, detail, browserURL string) *Failure {
	if detail != "" {
		detail = ": " + detail
	}

	return &Failure{
		Kind:       RequestRejected,
		Message:    fmt.Sprintf("GitLab rejected the request (HTTP %d)%s. Browser fallback: %s", status, detail, browserURL),
		Status:     status,
		BrowserURL: browserURL,
	}
}

// malformedResponse is a response that arrived with a non-error status but
// whose body is not the JSON the endpoint promises. Status is 0 when the fault
// is structural rather than tied to one response.
func malformedResponse(message string, status int, browserURL string) *Failure {
	return &Failure{Kind: MalformedResponse, Message: message, Status: status, BrowserURL: browserURL}
}

// resourceMissing is a domain-level miss: the call succeeded, and the
// collection it returned does not hold the asked-for item.
//
// Status stays 0 so no message ever claims an HTTP 404 that did not happen.
func resourceMissing(resource, searched, browserURL string) *Failure {
	return &Failure{
		Kind:       ResourceMissing,
		Message:    fmt.Sprintf("No %s in %s. Check in the browser: %s", resource, searched, browserURL),
		BrowserURL: browserURL,
		Resource:   resource,
	}
}

// transportError is the request never producing a usable HTTP response at all:
// DNS failure, TLS error, connection reset, timeout.
//
// Status is always 0 — there was no response to take one from.
func transportError(message, browserURL string) *Failure {
	return &Failure{Kind: TransportError, Message: message, BrowserURL: browserURL}
}

// failureFor builds the failure a status implies. detail is the server's own
// complaint, used only by the catch-all arm.
func failureFor(status int, detail, browserURL string, retryAfter int) *Failure {
	switch status {
	case 401:
		return unauthorized(browserURL)
	case 403:
		return endpointClosed(browserURL)
	case 404:
		return notFound(browserURL)
	case 429:
		return rateLimited(retryAfter, browserURL)
	default:
		return requestRejected(status, detail, browserURL)
	}
}
