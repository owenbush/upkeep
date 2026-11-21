package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/owenbush/upkeep/internal/cli/command"
	"github.com/owenbush/upkeep/internal/drupalorgcli"
)

// cliContractFlag selects the drupalorg-cli signature check instead of the
// domain-core run.
const cliContractFlag = "--cli-contract"

// cliContract holds the installed drupalorg-cli to the commands upkeep wraps:
// each exists, takes --no-interaction, and takes the positional arguments
// upkeep passes, in that order and shape. The expectations come from the
// same table that builds those commands, so they cannot drift from what
// upkeep runs.
//
// Read-only: it runs `drupalorg help <command>` and never the command, since
// most of them post on drupal.org under your name.
func cliContract() int {
	runner := drupalorgcli.New(nil, 0)
	ctx := context.Background()

	version, failure := runner.RunRaw(ctx, []string{"--version"})
	if failure != nil {
		fmt.Printf("FAILED: %s\n", failure.Message)

		return 1
	}
	fmt.Printf("=== live check: drupalorg-cli command signatures (%s) ===\n\n",
		strings.TrimSpace(string(version)))

	signatures := command.DrupalorgSignatures()
	failed := 0
	for _, want := range signatures {
		diffs := drupalorgcli.Check(ctx, runner, want)
		if diffs == nil {
			fmt.Printf("ok      %-20s %s\n", want.Command, usage(want))

			continue
		}
		failed++
		for _, diff := range diffs {
			fmt.Printf("FAILED  %-20s %s\n", want.Command, strings.TrimPrefix(diff, want.Command+": "))
		}
	}

	if failed > 0 {
		fmt.Printf("\nFAILED: %d of %d commands do not match what upkeep passes\n", failed, len(signatures))

		return 1
	}
	fmt.Println("\n=== ok ===")

	return 0
}

func usage(s drupalorgcli.Signature) string {
	parts := make([]string, 0, len(s.Args)+1)
	for _, option := range s.Options {
		parts = append(parts, "--"+option)
	}
	for _, arg := range s.Args {
		parts = append(parts, arg.String())
	}

	return strings.Join(parts, " ")
}
