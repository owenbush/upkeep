package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// GitlabClients hands out the GitLab client a command should use.
//
// An interface so a test can inject one, and so "how do we talk to GitLab" is
// a thing a command is given rather than a thing it decides.
type GitlabClients interface {
	// ReadOnly is a client for a command that only looks. It always returns
	// one — reading is anonymous when there is no token — and reports what
	// that costs.
	ReadOnly(report gitlab.Report) *gitlab.Client

	// Authenticated is a client for a command that writes, or an error naming
	// how to configure a token. No credential means no verdict can be
	// produced, which is an infrastructure failure rather than a degraded
	// mode.
	Authenticated(report gitlab.Report) (*gitlab.Client, error)
}

// ResolvedClients builds clients from whatever token the environment yields.
type ResolvedClients struct {
	resolver *gitlab.TokenResolver
}

// NewResolvedClients builds the production client source. warn receives
// diagnostics about the token itself — a world-readable file, an unusable
// value — and never carries token material.
func NewResolvedClients(warn func(string)) *ResolvedClients {
	return &ResolvedClients{resolver: gitlab.NewTokenResolver("", "", warn)}
}

// ReadOnly is a client for a command that only reads.
func (c *ResolvedClients) ReadOnly(report gitlab.Report) *gitlab.Client {
	return gitlab.ReadOnly(c.resolver, report)
}

// Authenticated is a client for a command that writes.
func (c *ResolvedClients) Authenticated(report gitlab.Report) (*gitlab.Client, error) {
	client := gitlab.Authenticated(c.resolver, func(string) {
		// Deliberately silent: the guidance travels in the error below, so it
		// is printed once by the failure path rather than twice.
	})
	if client == nil {
		return nil, fmt.Errorf("%s", gitlab.MissingTokenMessage(c.resolver))
	}

	return client, nil
}

// ReadingClient is the client for a command that only looks, with the cost of
// reading anonymously noted once, where diagnostics go.
func ReadingClient(cmd *cobra.Command, clients GitlabClients) *gitlab.Client {
	return clients.ReadOnly(func(note string) { Warnf(cmd, "%s", note) })
}

// WritingClient is the client for a command that changes something on GitLab.
//
// Declared by the command rather than inferred, and the strict path is the
// default: a command that writes and forgets to say so is refused early with
// the token guidance, where the other way round would let a write reach GitLab
// with no credential and fail four frames down.
func WritingClient(cmd *cobra.Command, clients GitlabClients) (*gitlab.Client, error) {
	return clients.Authenticated(func(note string) { Warnf(cmd, "%s", note) })
}
