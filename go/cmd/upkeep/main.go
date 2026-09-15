// Command upkeep is the maintenance orchestrator's entry point, and the
// composition root.
//
// This file and internal/adapter are the only places that name a concrete
// engine. Commands receive an adapter.Factory and never choose, name, or
// construct an engine themselves — which is what makes the boundary check
// meaningful rather than merely satisfied.
package main

import (
	"os"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cli/command"
	"github.com/owenbush/upkeep/internal/security"
)

func main() {
	redactor := security.RedactorFromEnvironment()
	engines := adapter.NewDdevContribFactory(redactor)

	os.Exit(cli.Execute(command.NewRoot(engines), os.Stderr))
}
