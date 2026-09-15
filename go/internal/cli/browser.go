package cli

import (
	"runtime"

	"github.com/owenbush/upkeep/internal/proc"
)

// Browser opens a URL for the operator.
//
// A seam rather than a direct call, so a test does not launch a browser and so
// "did it open?" is answerable — the commands that use this say something
// different when it did not.
type Browser interface {
	// Open reports whether a browser was actually launched.
	Open(url string) bool
}

// SystemBrowser hands the URL to the platform's opener.
//
// One copy of the platform choice, and one place where the guarantee holds
// that the opener — a child process like any other — never inherits the
// credential.
type SystemBrowser struct{}

// Open launches the platform's URL handler, best effort.
func (SystemBrowser) Open(url string) bool {
	return proc.TryPassthrough([]string{openerFor(runtime.GOOS), url})
}

// openerFor is the platform's URL handler.
//
// Taking the OS as an argument rather than reading it: the branch not taken on
// the machine a test runs on is still the one an operator on the other
// platform gets, and a constant nothing can exercise is a constant nobody
// checked.
func openerFor(goos string) string {
	if goos == "darwin" {
		return "open"
	}

	return "xdg-open"
}

// NoBrowser opens nothing, for a run that asked not to.
type NoBrowser struct{}

// Open reports that nothing was launched.
func (NoBrowser) Open(string) bool { return false }
