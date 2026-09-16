// Command docgen writes docs/commands.md from the CLI itself.
//
// A hand-written command reference is a second copy of the truth, and the copy
// loses: the project site's table sat at 11 of 27 commands long enough for the
// issue loop, the patch surface and a whole browser UI to be missing from it.
// cobra already knows every command, argument, flag, default and help string,
// so the reference is derived and gated in CI rather than remembered.
//
// The port of the PHP's tools/generate-command-reference.php, and deliberately
// the same shape — the file it writes is the file that was there before, so a
// reader who knew the old reference knows this one.
//
// Not under internal/: this is build tooling, outside the coverage floor and
// the adapter boundary, like the corpus generators it now sits beside.
//
//	go run ./cmd/docgen            # write docs/commands.md
//	go run ./cmd/docgen --check    # fail if it is out of date
package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cli/command"
)

// internalCommands are cobra's own, which describe the framework rather than
// upkeep. `completion` stays: it is how somebody turns on the module-name
// completion, which is most of the CLI's value at a prompt.
var internalCommands = map[string]bool{"help": true}

func main() {
	check := len(os.Args) > 1 && os.Args[1] == "--check"

	target := filepath.Join("docs", "commands.md")
	rendered := render(tree())

	if !check {
		if err := os.WriteFile(target, []byte(rendered), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "writing %s: %v\n", target, err)
			os.Exit(1)
		}
		fmt.Printf("%s written (%d commands).\n", target, len(commands(tree())))

		return
	}

	current, err := os.ReadFile(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading %s: %v\n", target, err)
		os.Exit(1)
	}
	if string(current) != rendered {
		fmt.Fprintf(os.Stderr,
			"%s is out of date. Regenerate it: go run ./cmd/docgen\n", target)
		os.Exit(1)
	}
	fmt.Printf("%s is up to date.\n", target)
}

// tree is the command tree as the binary composes it.
//
// Built from the same NewRoot the binary calls, with seams that do nothing:
// the reference describes the surface, and nothing here runs a command.
func tree() *cobra.Command {
	return command.NewRoot(command.Surface{
		Engines: adapter.NewDdevContribFactory(nil),
		Clients: cli.NewResolvedClients(func(string) {}),
		Issues:  command.NewDrupalClients(),
		Prompts: func(cmd *cobra.Command) cli.Prompt { return cli.NewTerminalPrompt(cmd) },
		Volumes: nil,
		Sizer:   nil,
		// A client rather than nil: the patch commands hold one, and a
		// reference that panicked while describing them would be a poor guard
		// against drift.
		Downloader: http.DefaultClient,
		Browser:    cli.NoBrowser{},
	})
}

// commands are the documented commands, in name order.
func commands(root *cobra.Command) []*cobra.Command {
	var documented []*cobra.Command
	for _, child := range root.Commands() {
		if internalCommands[child.Name()] || child.Hidden {
			continue
		}
		documented = append(documented, child)
	}
	sort.Slice(documented, func(a, b int) bool {
		return documented[a].Name() < documented[b].Name()
	})

	return documented
}

func render(root *cobra.Command) string {
	var out bytes.Buffer
	documented := commands(root)

	out.WriteString("# Command reference\n\n")
	out.WriteString("<!-- GENERATED FILE. Run `go run ./cmd/docgen` to regenerate; " +
		"do not edit by hand. -->\n\n")
	out.WriteString("Every command upkeep ships, generated from the CLI itself so it cannot " +
		"drift from what\nthe binary actually does. `go run ./cmd/docgen --check` fails the " +
		"build when this file\nand the commands disagree.\n\n")
	out.WriteString("Conventions worth knowing before the list:\n\n")
	out.WriteString("- **`--version` is always the target Drupal core major**, never an " +
		"application version.\n  The application-level `-V` is deliberately removed; " +
		"`upkeep version` prints it.\n")
	out.WriteString("- **Exit codes are a contract**: `0` the command did what was asked, " +
		"`1` the work it\n  supervised failed, `2` upkeep could not do the job.\n")
	out.WriteString("- **Reading needs no credential.** A token is required only to merge, " +
		"comment or publish.\n\n")

	out.WriteString("| Command | What it does |\n| --- | --- |\n")
	for _, cmd := range documented {
		fmt.Fprintf(&out, "| [`%s`](#%s) | %s |\n", cmd.Name(), anchor(cmd.Name()), cmd.Short)
	}

	if persistent := sortedFlags(root.PersistentFlags()); len(persistent) > 0 {
		out.WriteString("\n## Global options\n\n")
		out.WriteString("Accepted by every command, so they are listed once rather than " +
			"repeated below.\n\n")
		out.WriteString("| Option | What it does |\n| --- | --- |\n")
		for _, flag := range persistent {
			fmt.Fprintf(&out, "| %s | %s |\n", flagName(flag), flag.Usage)
		}
	}

	for _, cmd := range documented {
		renderCommand(&out, cmd)
	}

	return out.String()
}

func renderCommand(out *bytes.Buffer, cmd *cobra.Command) {
	fmt.Fprintf(out, "\n## `upkeep %s`\n\n", cmd.Name())

	if long := strings.TrimSpace(cmd.Long); long != "" {
		fmt.Fprintf(out, "%s\n\n", long)
	} else if cmd.Short != "" {
		fmt.Fprintf(out, "%s\n\n", cmd.Short)
	}

	// UseLine already carries the full path, "upkeep" included.
	fmt.Fprintf(out, "```\n%s\n```\n", strings.TrimSpace(cmd.UseLine()))

	if flags := sortedFlags(cmd.NonInheritedFlags()); len(flags) > 0 {
		out.WriteString("\n**Options**\n\n")
		out.WriteString("| Option | What it does |\n| --- | --- |\n")
		for _, flag := range flags {
			usage := flag.Usage
			if flag.DefValue != "" && flag.DefValue != "false" {
				usage += fmt.Sprintf(" Default: `%s`.", tilde(flag.DefValue))
			}
			fmt.Fprintf(out, "| %s | %s |\n", flagName(flag), usage)
		}
	}
}

// flagName is the flag as somebody types it, shorthand included.
func flagName(flag *pflag.Flag) string {
	name := "`--" + flag.Name
	if flag.Value.Type() != "bool" {
		name += "=" + strings.ToUpper(flag.Name)
	}
	name += "`"
	if flag.Shorthand != "" {
		name += " / `-" + flag.Shorthand + "`"
	}

	return name
}

func sortedFlags(set *pflag.FlagSet) []*pflag.Flag {
	var flags []*pflag.Flag
	set.VisitAll(func(flag *pflag.Flag) {
		if !flag.Hidden {
			flags = append(flags, flag)
		}
	})
	sort.Slice(flags, func(a, b int) bool { return flags[a].Name < flags[b].Name })

	return flags
}

// tilde puts $HOME back as `~`.
//
// Some defaults are computed from the home directory, and writing the
// generating machine's path into a committed file makes it differ per
// developer — which would fail --check for everyone but whoever ran it last.
// The same reproducibility trap the corpus generators had.
func tilde(value string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}

	return strings.ReplaceAll(value, home, "~")
}

// anchor is the heading's GitHub anchor: lowercased, punctuation dropped,
// spaces hyphenated.
func anchor(name string) string {
	slug := strings.ReplaceAll("upkeep "+name, " ", "-")
	var kept strings.Builder
	for _, r := range strings.ToLower(slug) {
		if r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			kept.WriteRune(r)
		}
	}

	return kept.String()
}
