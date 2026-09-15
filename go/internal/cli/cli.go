// Package cli is the command surface: the exit-code contract, the shared flag
// set, and the one resolution seam every command goes through.
//
// The PHP has these on a base class twenty-one commands extend. Go has no
// inheritance and cobra has no base command, so the same three jobs are done
// by this package instead — which is the better shape for them anyway, since
// what they actually share is *behaviour applied to a command*, not identity.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/workflow"
)

// Exit carries the code a command answers with.
//
// Returned as an error so it travels back through cobra's RunE without every
// command in between having to thread a second value. The code is the whole
// payload: anything worth printing has already been printed, by the command or
// by Run.
type Exit struct{ Code int }

func (e Exit) Error() string { return "exit " + strconv.Itoa(e.Code) }

// CodeOf is the exit code an error stands for.
//
// Anything that is not an Exit is a domain failure that got this far, which is
// exactly the documented way to report "upkeep could not do the job".
func CodeOf(err error) int {
	if err == nil {
		return workflow.OK
	}

	var exit Exit
	if errors.As(err, &exit) {
		return exit.Code
	}

	return workflow.Infrastructure
}

// Perform is a command's work: the exit code it answers with, or an error.
//
// Returning an error is the documented way to report an infrastructure
// failure; Run prints it and maps it to code 2. A command that wants any other
// code returns it explicitly, and has already said why.
type Perform func(cmd *cobra.Command, args []string) (int, error)

// Run adapts a Perform to cobra, and is where the exit-code contract lives.
//
// Every command answers with exactly one of three codes, mapped here rather
// than in twenty-one places: 0 it did what was asked, 1 the work it supervised
// failed, 2 upkeep could not do the job. Nothing returns cobra's own
// conventions, whose numbers would collide with the contract while meaning
// something else.
func Run(perform Perform) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		// Set here as well as on the root, so the property belongs to the
		// command rather than to how the tree happened to be assembled: a
		// command that refused because there is no cockpit has already said
		// something more useful than its own flag list.
		cmd.SilenceUsage = true

		code, err := perform(cmd, args)
		if err != nil {
			Errorf(cmd, "%s", err)

			return Exit{Code: workflow.Infrastructure}
		}

		return Exit{Code: code}
	}
}

// Errorf reports a failure to the operator, on stderr.
//
// Always stderr, never the command's own output stream: a command whose stdout
// is a path to capture or a table to pipe must stay pipeable even when it is
// also complaining.
func Errorf(cmd *cobra.Command, format string, args ...any) {
	fmt.Fprintf(cmd.ErrOrStderr(), "Error: "+format+"\n", args...)
}

// Warnf reports something the operator should know but that does not stop the
// command.
func Warnf(cmd *cobra.Command, format string, args ...any) {
	fmt.Fprintf(cmd.ErrOrStderr(), "Warning: "+format+"\n", args...)
}

// Printf writes the command's actual output.
func Printf(cmd *cobra.Command, format string, args ...any) {
	fmt.Fprintf(cmd.OutOrStdout(), format, args...)
}

// Println writes one line of the command's actual output.
func Println(cmd *cobra.Command, line string) {
	fmt.Fprintln(cmd.OutOrStdout(), line)
}

// The shared flag names. Declared once so a command cannot spell one
// differently from the command beside it.
const (
	FlagCockpit      = "cockpit"
	FlagProjectsRoot = "projects-root"
	FlagVersion      = "version"
	FlagNoOpen       = "no-open"
	FlagNoUpdate     = "no-update"
)

// AddCockpit adds --cockpit.
func AddCockpit(cmd *cobra.Command) {
	cmd.Flags().String(FlagCockpit, "", fmt.Sprintf(
		"Path to the cockpit directory (defaults to $%s, then the current directory)",
		cockpit.EnvVar,
	))
}

// AddProjectsRoot adds --projects-root.
func AddProjectsRoot(cmd *cobra.Command) {
	cmd.Flags().String(FlagProjectsRoot, "", fmt.Sprintf(
		"Directory holding the engine environments (defaults to $%s, then <cockpit>/projects/ "+
			"if it exists, then ~/.upkeep/projects)",
		adapter.ProjectsRootEnvVar,
	))
}

// AddTargetCore adds --version as the *target core selector*: which core major
// the command acts against.
//
// Never an application-version flag. The two meanings would share a spelling,
// and the one people reach for on a maintenance tool is the core.
func AddTargetCore(cmd *cobra.Command) {
	cmd.Flags().String(FlagVersion, "", "Target core major version; must be one the module tracks. "+
		"Defaults to the first core version listed for it.")
}

// AddCoreFilter adds --version as a *filter* over already-assembled rows, for
// the reporting commands.
//
// Deliberately a separate seam from the selector: same flag name, different
// meaning, each with one canonical wording.
func AddCoreFilter(cmd *cobra.Command) {
	cmd.Flags().String(FlagVersion, "", "Only show rows targeting this core major version (e.g. 11)")
}

// AddNoOpen adds --no-open.
func AddNoOpen(cmd *cobra.Command) {
	cmd.Flags().Bool(FlagNoOpen, false, "Do not open the drupal.org issue in the browser")
}

// AddNoUpdate adds --no-update: opt out of bringing the base branch up to date
// before cutting from it.
//
// The default is to update, because not updating is what produced a green
// local check and a red pipeline with no visible difference between them:
// drupal.org's CI does not test your branch, it tests your branch merged into
// the *current* tip of the target, and a working copy is cloned once and then
// never fetched again. The flag is for working offline, and for reproducing a
// verdict against the tree as it was.
func AddNoUpdate(cmd *cobra.Command) {
	cmd.Flags().Bool(FlagNoUpdate, false,
		"Do not fetch the base branch first; check against the working copy's base as it stands")
}

// Flag is a string flag's value.
func Flag(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)

	return value
}

// Switched is whether a boolean flag was given.
func Switched(cmd *cobra.Command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)

	return value
}

// BaseRefresh is the base-refresh mode this run asked for.
func BaseRefresh(cmd *cobra.Command) adapter.BaseRefresh {
	return adapter.RefreshFromNoUpdateFlag(Switched(cmd, FlagNoUpdate))
}

// Cockpit resolves the cockpit: --cockpit, then the environment, then the
// working directory.
//
// The flag's value is passed through even when empty, deliberately: `--cockpit=`
// is a mistake worth naming, and the resolver names it.
func Cockpit(cmd *cobra.Command) (*cockpit.Cockpit, error) {
	value := Flag(cmd, FlagCockpit)
	if cmd.Flags().Changed(FlagCockpit) {
		return cockpit.Resolve(&value)
	}

	return cockpit.Resolve(nil)
}

// Modules is the registry's modules, loaded and validated up front.
//
// Up front including on the destructive commands, where a parse failure
// discovered halfway through would leave the operator staring at a plan that
// will not run.
func Modules(where *cockpit.Cockpit) (map[string]cockpit.Module, error) {
	registry, err := where.LoadRegistry()
	if err != nil {
		return nil, err
	}

	return registry.Modules(), nil
}

// RequireCockpit is the cockpit proven usable: resolved, and its registry
// parsed — so a command that needs a cockpit but not its contents still
// reports a missing or malformed one the way every other command does.
func RequireCockpit(cmd *cobra.Command) (*cockpit.Cockpit, error) {
	where, err := Cockpit(cmd)
	if err != nil {
		return nil, err
	}
	if _, err := Modules(where); err != nil {
		return nil, err
	}

	return where, nil
}

// CoresOnDisk is the core majors base artifacts exist for, ascending.
//
// What gives a module the registry does not carry its usable cores. An
// unreadable base-artifacts directory yields none rather than failing: a
// module with a registry entry does not need this at all, and refusing there
// would turn a directory problem into a refusal to work on a registered
// module.
//
// The explicit check is equivalent to ignoring the error today, because the
// layout answers nil alongside one. It stays because "an error means no cores"
// is the rule, and leaning on another package's zero value for it is how that
// rule stops holding without anything saying so.
func CoresOnDisk(where *cockpit.Cockpit) []string {
	versions, err := baseartifact.NewLayout(where.BaseArtifactsPath()).VersionsOnDisk()
	if err != nil {
		return nil
	}

	return versions
}

// ResolveModule is the module a *subject* command acts on, registered or not.
//
// The registry is a watchlist, not a gate: `project/<name>` is drupal.org's
// convention and the cores a run can use are the ones base artifacts exist
// for, so a module nobody has registered is workable. A registry entry still
// wins where there is one — a maintainer's core_versions is a deliberate
// statement and outranks anything inferred.
func ResolveModule(
	where *cockpit.Cockpit, modules map[string]cockpit.Module, name string,
) (cockpit.Module, error) {
	return cockpit.ResolveModule(modules, name, CoresOnDisk(where))
}

// TargetCore is the core major this invocation acts against.
func TargetCore(cmd *cobra.Command, module cockpit.Module) (string, error) {
	return workflow.SelectCoreVersion(module, Flag(cmd, FlagVersion))
}

// MrIID is the one merge-request-IID rule: a positive integer.
//
// `!0` is not a merge request, so it is refused here rather than turned into a
// confusing 404 from GitLab.
func MrIID(raw string) (int, error) {
	iid, err := strconv.Atoi(raw)
	if err != nil || iid < 1 || strings.ContainsAny(raw, "+-") {
		return 0, fmt.Errorf(
			"the <mr> argument must be a merge request IID (a positive integer), got %q", raw,
		)
	}

	return iid, nil
}

// ReportScanWarnings surfaces what a drupal.org scan could not read.
//
// That client degrades by returning *less data*, which at the call site is
// indistinguishable from there being less data — a dropped attachment looks
// like an issue with fewer patches, a truncated page like a project with fewer
// issues. So the shortfall is stated, and the counts printed alongside it are
// named as the lower bounds they are.
func ReportScanWarnings(cmd *cobra.Command, warnings []string) {
	if len(warnings) == 0 {
		return
	}

	shown := warnings
	if len(shown) > 5 {
		shown = append(append([]string{}, warnings[:5]...),
			fmt.Sprintf("... and %d more.", len(warnings)-5))
	}

	Warnf(cmd, "%d drupal.org request(s) did not answer.", len(warnings))
	for _, warning := range shown {
		fmt.Fprintln(cmd.ErrOrStderr(), "  - "+warning)
	}
	fmt.Fprintln(cmd.ErrOrStderr(),
		"  Counts below are lower bounds: re-run to pick up what was missed.")
}

// Execute runs the root command and reports the code the process should exit
// with.
//
// Errors and usage are both silenced, so everything a failure prints goes
// through here and lands on stderr. Cobra's own default puts the usage block
// on *stdout*, which is the same defect as a diagnostic there: `upkeep
// env:path` exists to be captured with `cd $(upkeep env:path …)`, and a flag
// typo would put forty lines of synopsis inside the capture. So a usage
// mistake gets its own words plus a pointer to --help, on stderr, and the
// synopsis is printed only when somebody asks for it.
func Execute(root *cobra.Command, stderr io.Writer) int {
	root.SilenceErrors = true
	root.SilenceUsage = true

	failed, err := root.ExecuteC()

	var exit Exit
	if err != nil && !errors.As(err, &exit) {
		// Cobra's own refusals — an unknown flag, a missing argument — reach
		// here unprinted. They are usage mistakes, which the contract calls an
		// infrastructure failure.
		fmt.Fprintf(stderr, "Error: %s\n", err)
		if failed != nil {
			fmt.Fprintf(stderr, "Run \"%s --help\" for usage.\n", failed.CommandPath())
		}
	}

	return CodeOf(err)
}
