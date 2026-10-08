package command

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/naming"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewModulesTrack builds the modules:track command.
//
// Which cores a module is tracked for is the one registry field that changes
// in the ordinary course of maintenance — a new core major reaches alpha, an
// old one goes end of life — and until this existed the only way to change it
// was to open registry.yml in an editor. The refusal that sends people here
// said so in as many words ("Add it to core_versions in registry.yml"), which
// is a tool describing a file edit rather than offering to make it.
//
// Its stdout is empty on every path, and the report goes to stderr with
// `modules:add`'s. What this command answers with is the edit; the queryable
// fact is the registry itself, which `upkeep modules` already prints as a
// table on stdout. Two commands printing the same list to stdout is how a
// script comes to depend on the chattier one.
//
// It is deliberately not part of `modules:add`. That command skips a name
// already registered, on the stance that an existing definition is a
// maintainer's deliberate statement and a bulk registration must never
// overturn it; folding an in-place edit into it would make that stance
// conditional on a flag. It also reads GitLab for your memberships, and
// needing a credential to change a local YAML list would be absurd.
func NewModulesTrack() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "modules:track <module> [core...]",
		Short: "Change which Drupal core majors a registered module is tracked for",
		Long: `Changes the core_versions of a module already in the registry, so the cores a
module is checked against are a command rather than a file edit.

  upkeep modules:track jumplinks           what it tracks today
  upkeep modules:track jumplinks 12        track core 12 as well
  upkeep modules:track jumplinks --remove=10
  upkeep modules:track jumplinks --set=12,11

Cores named as arguments are appended, so the first entry — the core a command
targets when --version is omitted — does not move unless you say so. --set
replaces the whole list in the order you give it, which is how that default
changes.

Writes registry.yml, and nothing else. Use ` + "`upkeep modules:add`" + ` to register a
module that has no entry yet.`,
		Args: cobra.MinimumNArgs(1),
	}
	cli.AddCockpit(cmd)
	cmd.Flags().String(cli.FlagRemove, "",
		`Comma-separated core majors to stop tracking (e.g. "10")`)
	cmd.Flags().String(cli.FlagSet, "",
		`Comma-separated core majors to track, replacing the list; the first is the default (e.g. "12,11")`)
	cli.AddTrackCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runModulesTrack(cmd, args)
	})

	return cmd
}

func runModulesTrack(cmd *cobra.Command, args []string) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	registered, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}

	// The strict lookup, not ResolveModule: this edits a registry entry, and a
	// module derived from the base artifacts on this machine has none. Its
	// cores are a fact about a directory, so "tracking" one more would write a
	// statement nobody could honour.
	module, err := workflow.RequireModule(registered, args[0])
	if err != nil {
		return 0, err
	}

	added, removed := args[1:], splitCoreVersions(cli.Flag(cmd, cli.FlagRemove))
	replacement := splitCoreVersions(cli.Flag(cmd, cli.FlagSet))

	wanted, err := trackedAfter(module, added, removed, replacement)
	if err != nil {
		return 0, err
	}

	if slices.Equal(wanted, module.CoreVersions) {
		cli.Progressf(cmd, "%s", tracksLine(module.Name, module.CoreVersions))

		return workflow.OK, nil
	}

	updated, err := cockpit.NewEditor(where.RegistryPath()).SetCoreVersions(module.Name, wanted)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "%s", strings.Replace(
		tracksLine(updated.Name, updated.CoreVersions), " tracks ", " now tracks ", 1))
	reportUnbuilt(cmd, where, updated.CoreVersions, module.CoreVersions)

	return workflow.OK, nil
}

// trackedAfter is the list the module should end up with, or why it cannot.
//
// Every refusal here happens before the registry is opened for writing, so a
// contradictory invocation costs nothing and leaves the file exactly as it was.
func trackedAfter(
	module cockpit.Module, added, removed, replacement []string,
) ([]string, error) {
	if len(replacement) > 0 && (len(added) > 0 || len(removed) > 0) {
		return nil, fmt.Errorf(
			"--set replaces the whole list, so it cannot be combined with cores to add or --remove. "+
				"Name every core you want, in order: --set=%s",
			strings.Join(replacement, ","),
		)
	}

	for _, core := range slices.Concat(added, removed, replacement) {
		if !naming.IsCoreMajor(core) {
			return nil, fmt.Errorf(
				"%q is not a Drupal core major version. Name it as a whole number, e.g. 11", core)
		}
	}

	if len(replacement) > 0 {
		// Deduplicated but never sorted: the order given is the order written,
		// because the first entry is the core a command targets when --version
		// is omitted.
		return firstOccurrences(replacement), nil
	}

	wanted := []string{}
	for _, core := range module.CoreVersions {
		if !slices.Contains(removed, core) {
			wanted = append(wanted, core)
		}
	}
	// Appended, not inserted: this must not move the entry at the front.
	for _, core := range added {
		if !slices.Contains(wanted, core) {
			wanted = append(wanted, core)
		}
	}

	if len(wanted) == 0 {
		return nil, fmt.Errorf(
			"that would leave %q tracking no core at all, and the registry needs at least one. "+
				"To stop watching the module entirely, remove its entry from registry.yml",
			module.Name,
		)
	}

	return wanted, nil
}

// firstOccurrences keeps each value once, at the position it first appeared.
func firstOccurrences(values []string) []string {
	seen := map[string]bool{}
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		kept = append(kept, value)
	}

	return kept
}

// tracksLine is what a module tracks, and which of those a bare command gets.
//
// The default is named because it is the consequence of the list's *order*,
// which is the one thing about this field that is not obvious from reading it.
func tracksLine(name string, cores []string) string {
	return fmt.Sprintf("%s tracks core %s (default %s).",
		name, strings.Join(cores, ", "), cores[0])
}

// reportUnbuilt warns about a newly tracked core that has no base artifacts.
//
// Tracking a core is permission to check against it, and the check is what
// actually needs the artifact set — so without this the command succeeds and
// the very next run refuses, naming a build the operator could have started
// here. A warning rather than a refusal: building is a long job, and deciding
// to track a core before building for it is a perfectly ordinary order to do
// these in.
//
// Silence when the base-artifacts directory cannot be read at all, since
// CoresOnDisk answers nothing for both "none built" and "could not look", and
// warning about every core on an unreadable directory would be noise.
func reportUnbuilt(cmd *cobra.Command, where *cockpit.Cockpit, wanted, before []string) {
	built := cli.CoresOnDisk(where)
	if len(built) == 0 {
		return
	}

	for _, core := range wanted {
		if slices.Contains(before, core) || slices.Contains(built, core) {
			continue
		}
		cli.Warnf(cmd, "no base artifacts for core %s yet, so checks against it will refuse. "+
			"Build them with: upkeep base-artifacts:build --version=%s", core, core)
	}
}
