// Command upkeep is the maintenance orchestrator's entry point, and the
// composition root.
//
// This file and internal/adapter are the only places that name a concrete
// engine. Commands receive an adapter.Factory and never choose, name, or
// construct an engine themselves — which is what makes the boundary check
// meaningful rather than merely satisfied.
package main

import (
	"fmt"
	"os"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cli/command"
	"github.com/owenbush/upkeep/internal/maintenance"
	"github.com/owenbush/upkeep/internal/proc"
	"github.com/owenbush/upkeep/internal/security"
)

func main() {
	redactor := security.RedactorFromEnvironment()

	// One runner for the read-only probes the reporting commands make. Their
	// output is never shown — a `du` measurement is a number in a table — but
	// it still goes through the package that strips the credential, because
	// "every child" is the rule and an exception is how a rule stops holding.
	quiet := proc.New(nil, nil, redactor)

	os.Exit(cli.Execute(command.NewRoot(
		adapter.NewDdevContribFactory(redactor),
		cli.NewResolvedClients(func(note string) { fmt.Fprintln(os.Stderr, "Warning: "+note) }),
		adapter.NewVolumeProbe(quiet),
		maintenance.DiskSizer(quiet),
	), os.Stderr))
}
