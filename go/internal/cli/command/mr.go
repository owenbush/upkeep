package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/workflow"
)

// addMrSurface is the argument and flag set the single-merge-request commands
// share, so a description cannot drift between them.
func addMrSurface(cmd *cobra.Command) {
	cli.AddTargetCore(cmd)
	cli.AddCockpit(cmd)
	cli.AddProjectsRoot(cmd)
	cli.AddVerbose(cmd)
}

// resolveMrContext resolves the cockpit, the registry, the GitLab client and
// the merge request itself.
//
// Read-only throughout: check and review fetch a merge request and check it
// out, and change nothing on GitLab. drupalcode serves a public project's
// merge requests and refs anonymously, so a token is not required to look —
// which is why these two run without one and say what that costs.
func resolveMrContext(
	cmd *cobra.Command, clients cli.GitlabClients, moduleName, rawIID string,
) (*cockpit.Cockpit, workflow.MrContext, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return nil, workflow.MrContext{}, err
	}
	modules, err := cli.Modules(where)
	if err != nil {
		return nil, workflow.MrContext{}, err
	}

	iid, err := cli.MrIID(rawIID)
	if err != nil {
		return nil, workflow.MrContext{}, err
	}

	cli.Progressf(cmd, "Resolving MR !%d of %s via GitLab ...", iid, moduleName)

	// The disk answers for a module the registry does not carry: a merge
	// request is a subject like any other, and gating it on the watchlist was
	// the half of that split that got missed.
	context, err := workflow.NewMrResolver(
		modules, cli.ReadingClient(cmd, clients), cli.CoresOnDisk(where),
	).Resolve(moduleName, iid, cli.Flag(cmd, cli.FlagVersion))
	if err != nil {
		return nil, workflow.MrContext{}, err
	}

	return where, context, nil
}

// describeContext is the one-line summary printed before the long steps, so
// somebody watching a ten-minute provision can see it is provisioning the
// right thing.
func describeContext(cmd *cobra.Command, context workflow.MrContext, requestedCore string) {
	mergeRequest := context.MergeRequest

	head := mergeRequest.HeadSHA
	if head == "" {
		head = "unknown"
	}
	cli.Progressf(cmd, "MR !%d %q (%s -> %s) by %s, head %s",
		mergeRequest.IID, mergeRequest.Title,
		mergeRequest.SourceBranch, mergeRequest.TargetBranch,
		mergeRequest.AuthorUsername, head)

	defaulted := ""
	if requestedCore == "" {
		defaulted = " (default: the first core version listed for the module)"
	}
	cli.Progressf(cmd, "Target: Drupal core %s%s, module %s",
		context.CoreMajor, defaulted, context.Module.Name)
}

// renderCheckSummary is the per-check table and the failing output beneath it.
func renderCheckSummary(cmd *cobra.Command, run check.RunResult) {
	rows := make([][]string, 0, len(run.Results))
	for _, result := range run.Results {
		exit := "-"
		if result.ExitCode != nil {
			exit = fmt.Sprintf("%d", *result.ExitCode)
		}
		rows = append(rows, []string{
			string(result.Type),
			string(result.Status),
			exit,
			fmt.Sprintf("%.1fs", result.Duration.Seconds()),
		})
	}

	cli.Println(cmd, "")
	cli.Table{
		Headers: []string{"Check", "Status", "Exit", "Duration"},
		Rows:    rows,
	}.Render(cmd.OutOrStdout())

	// The output of what failed, because that is what somebody does next: a
	// table saying "phpcs FAILED" and nothing else sends them to run it again
	// by hand to find out what it said.
	for _, failure := range run.Failures() {
		cli.Printf(cmd, "\nFAILED: %s\n", failure.Type)
		excerpt := failure.OutputExcerpt(check.ExcerptBytes)
		if excerpt == "" {
			excerpt = "(no output captured)"
		}
		cli.Println(cmd, excerpt)
	}

	cli.Println(cmd, "")
	if run.AllPassed() {
		cli.Println(cmd, "All checks green.")

		return
	}
	cli.Printf(cmd, "%d check(s) failed.\n", len(run.Failures()))
}

// ensureEnvironment provisions or reuses the environment for a subject, with
// the stage line that says which is happening.
func ensureEnvironment(
	cmd *cobra.Command, engine adapter.Engine, module cockpit.Module, coreMajor string,
) (adapter.Environment, error) {
	cli.Progressf(cmd, "Environment")

	return engine.EnsureEnv(module, coreMajor)
}

// loadFixtureIfAsked restores a named fixture before any check runs.
//
// Before, not after: a fixture that cannot be loaded means the checks would
// run against the wrong database, and a green suite over the wrong data is
// worse than no suite at all.
func loadFixtureIfAsked(
	cmd *cobra.Command, engine adapter.Engine, environment adapter.Environment,
) error {
	fixture := cli.Flag(cmd, "fixture")
	if fixture == "" {
		return nil
	}

	cli.Progressf(cmd, "Fixture: %s", fixture)

	return engine.LoadFixture(environment, fixture)
}
