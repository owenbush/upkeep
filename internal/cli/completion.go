package cli

import (
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cockpit"
)

// Shell completion for the values a command takes.
//
// Command *names* complete for free. Argument values do not, and they are the
// tedious half: a machine name is long, easy to mistype, and the thing every
// invocation starts with. The suggestions come from the operator's own
// cockpit, so they are exactly the modules and cores they can act on.
//
// **Nothing here may fail loudly.** This runs on every press of TAB, and a
// panic or an error message would spill across the prompt of somebody who only
// wanted a module name. So a cockpit that cannot be resolved, a registry that
// will not parse, an unreadable directory — all of them suggest nothing, and
// the ordinary command run a moment later reports the problem properly.
//
// Values nothing local can enumerate are deliberately not completed. A merge
// request IID or an issue node id would mean a network round trip per
// keystroke, against an API with rate limits, for a number the operator is
// copying from a page they already have open.

// noSuggestions is the answer to everything that went wrong, and to every
// value this cannot know. NoFileComp matters: without it the shell falls back
// to listing the current directory, so a failed completion looks like upkeep
// suggesting file names.
func noSuggestions() ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// suggest answers with values, filtered to what has been typed so far.
func suggest(values []string, typed string) ([]string, cobra.ShellCompDirective) {
	matched := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, typed) {
			matched = append(matched, value)
		}
	}
	sort.Strings(matched)

	return matched, cobra.ShellCompDirectiveNoFileComp
}

// CompleteModule suggests module machine names.
//
// Every module the cockpit watches, plus every one an environment has been
// provisioned for — the registry is a watchlist rather than a gate, so a
// module somebody has already worked on is worth offering even if nobody
// added it.
func CompleteModule(cmd *cobra.Command, _ []string, typed string) ([]string, cobra.ShellCompDirective) {
	defer recoverCompletion()

	where, err := Cockpit(cmd)
	if err != nil {
		return noSuggestions()
	}

	names := map[string]bool{}
	for name := range registryModules(where) {
		names[name] = true
	}
	for _, name := range provisionedModules(where, cmd) {
		names[name] = true
	}

	return suggest(keysOf(names), typed)
}

// CompleteTargetCore suggests core major versions for --version.
//
// The cores the module already on the command line tracks, so it offers 10 and
// 11 rather than every version any module uses. With no module named yet,
// everything built here is fair game — that being the set a run could use at
// all.
func CompleteTargetCore(
	cmd *cobra.Command, args []string, typed string,
) ([]string, cobra.ShellCompDirective) {
	defer recoverCompletion()

	where, err := Cockpit(cmd)
	if err != nil {
		return noSuggestions()
	}

	modules := registryModules(where)
	if len(args) > 0 {
		if module, watched := modules[args[0]]; watched {
			return suggest(module.CoreVersions, typed)
		}
	}

	cores := map[string]bool{}
	for _, core := range CoresOnDisk(where) {
		cores[core] = true
	}
	for _, module := range modules {
		for _, core := range module.CoreVersions {
			cores[core] = true
		}
	}

	return suggest(keysOf(cores), typed)
}

// CompleteFixture suggests the fixtures a run could load.
//
// The shared library only. A module's own committed fixtures live inside an
// environment that may not exist yet, and offering a name that cannot be
// loaded is worse than offering nothing.
func CompleteFixture(cmd *cobra.Command, _ []string, typed string) ([]string, cobra.ShellCompDirective) {
	defer recoverCompletion()

	where, err := Cockpit(cmd)
	if err != nil {
		return noSuggestions()
	}

	entries, err := os.ReadDir(where.FixturesPath())
	if err != nil {
		return noSuggestions()
	}

	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), fixtureSuffix) {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), fixtureSuffix))
	}

	return suggest(names, typed)
}

// fixtureSuffix is what a fixture dump is named.
const fixtureSuffix = ".sql.gz"

// AddModuleCompletion makes a command's first argument complete as a module
// name, and its --version flag as that module's cores.
//
// Both together, because they are the pair every subject command takes and
// wiring one without the other is how half a surface ends up completing.
func AddModuleCompletion(cmd *cobra.Command) {
	cmd.ValidArgsFunction = func(
		cmd *cobra.Command, args []string, typed string,
	) ([]string, cobra.ShellCompDirective) {
		// Only the first argument is a module. The second, where there is one,
		// is a merge request IID — a number from a page the operator already
		// has open, and not something to spend a request per keystroke on.
		if len(args) > 0 {
			return noSuggestions()
		}

		return CompleteModule(cmd, args, typed)
	}

	registerFlagCompletion(cmd, FlagVersion, CompleteTargetCore)
}

// AddFixtureCompletion makes --fixture complete from the shared library.
func AddFixtureCompletion(cmd *cobra.Command) {
	registerFlagCompletion(cmd, "fixture", CompleteFixture)
}

// registerFlagCompletion wires a flag's values, ignoring a flag that is not
// there: registering completion for a flag a command does not have is a
// mistake worth nothing at a TAB press.
func registerFlagCompletion(
	cmd *cobra.Command,
	flag string,
	complete func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective),
) {
	if cmd.Flags().Lookup(flag) == nil {
		return
	}
	_ = cmd.RegisterFlagCompletionFunc(flag, complete)
}

// registryModules is the watchlist, or nothing at all when it cannot be read.
//
// The explicit check is equivalent to ignoring the error today, because the
// loader answers nil alongside one. It stays because "unreadable means suggest
// nothing" is the rule here, and leaning on another package's zero value for
// it is how that rule stops holding without anything saying so.
func registryModules(where *cockpit.Cockpit) map[string]cockpit.Module {
	modules, err := Modules(where)
	if err != nil {
		return nil
	}

	return modules
}

// provisionedModules is every module an environment exists for.
//
// Read off the disk rather than asked of the engine: a completion must not
// start a child process, and the directory names carry the answer.
func provisionedModules(where *cockpit.Cockpit, cmd *cobra.Command) []string {
	root := Flag(cmd, FlagProjectsRoot)
	if root == "" {
		root = where.ProjectsPath()
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if name := moduleInProjectName(entry.Name()); name != "" {
			names = append(names, name)
		}
	}

	return names
}

// moduleInProjectName reads a module's machine name back out of an engine
// project directory, or "" when the directory is not one.
//
// The hyphens go back to underscores, which is the one lossy step in the other
// direction — a name that came from `a_b` and one that came from `a-b` would
// look the same here, except that `a-b` is not a legal machine name, so there
// is only ever one candidate.
func moduleInProjectName(directory string) string {
	if !strings.HasPrefix(directory, projectPrefix) {
		return ""
	}

	rest := strings.TrimPrefix(directory, projectPrefix)
	cut := strings.LastIndex(rest, "-d")
	if cut < 1 {
		return ""
	}

	return strings.ReplaceAll(rest[:cut], "-", "_")
}

// projectPrefix is how an engine project directory starts.
const projectPrefix = "upkeep-"

// keysOf is a set as a slice.
func keysOf(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}

	return values
}

// recoverCompletion swallows a panic during completion.
//
// The last line of the same defence as the error handling above: this runs on
// every press of TAB, and a stack trace across somebody's prompt is a worse
// outcome than no suggestions. Anything that gets here is a bug, and the
// ordinary command run a moment later will hit it properly.
func recoverCompletion() { _ = recover() }

// AddModuleFlagCompletion makes a --module flag complete as a module name.
//
// The survey commands take the module as a flag rather than an argument, so
// they get this instead of AddModuleCompletion — same values, different place
// on the line.
func AddModuleFlagCompletion(cmd *cobra.Command) {
	registerFlagCompletion(cmd, "module", CompleteModule)
}
