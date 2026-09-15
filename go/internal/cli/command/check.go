package command

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewCheck builds the check command.
//
// The single-merge-request workhorse: resolve the context, ensure the
// (module x core) environment, apply the merge request, optionally load a
// fixture, run the checks, report them, and cache the verdict where the
// dashboard and the fast-lane gate read it.
//
// Two modes. Given a merge request IID it does the above and caches. Given
// --working-copy it runs the same suite against whatever the module working
// copy is on, and caches **nothing**.
//
// That asymmetry is the point rather than an oversight. A cached verdict is
// keyed by a subject and a revision — a merge request and the SHA of its merge
// ref, a patch and its source URL — so the dashboard can tell a fresh pass
// from one about work that has since moved. A working copy has neither: it is
// mutable local state with no identity the next command could match against,
// and an entry keyed on a guess would put evidence in front of the fast-lane
// gate that nothing could ever invalidate.
func NewCheck(engines adapter.Factory, clients cli.GitlabClients) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <module> [mr]",
		Short: "Run one merge request through the full isolated check flow",
		Long: `Runs one merge request through the full isolated flow: provision the
(module x core) environment, apply the MR, run every check, and cache the
result where the dashboard and the fast-lane gate read it.

  upkeep check pathauto 12
  upkeep check pathauto 12 --version=11
  upkeep check pathauto 12 --fixture=sample-content

Or check what you are working on right now, whatever branch that is — after
upkeep start, or upkeep patch:promote, or your own edits:

  upkeep check pathauto --working-copy

That mode needs no merge request and no GitLab token, and caches nothing: a
working copy has no revision the dashboard could match a verdict against.

Exits 0 all green, 1 a check failed, 2 it could not run at all. Needs base
artifacts for that core: upkeep base-artifacts:build --version=11.`,
		Args: cobra.RangeArgs(1, 2),
	}
	cmd.Flags().Bool("working-copy", false,
		"Check the module working copy as it stands instead of a merge request (caches nothing)")
	cmd.Flags().String("fixture", "",
		"Load this named fixture into the database before running checks")
	addMrSurface(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runCheck(cmd, engines, clients, args)
	})

	return cmd
}

func runCheck(
	cmd *cobra.Command, engines adapter.Factory, clients cli.GitlabClients, args []string,
) (int, error) {
	moduleName := args[0]
	rawIID := ""
	if len(args) == 2 {
		rawIID = args[1]
	}
	workingCopy := cli.Switched(cmd, "working-copy")

	// A CLI framework can express "required" and "optional" but not "exactly
	// one of these two", so both refusals live here — both, because guessing
	// either way would run a different check than the one that was asked for.
	if workingCopy && rawIID != "" {
		return 0, fmt.Errorf(
			"give a merge request IID or --working-copy, not both: !%s names a specific branch to "+
				"fetch, while --working-copy means whatever the working copy already holds",
			rawIID,
		)
	}
	if !workingCopy && rawIID == "" {
		return 0, fmt.Errorf(
			"nothing to check. Name a merge request (upkeep check <module> <mr>), or pass " +
				"--working-copy to check what the module working copy is currently on",
		)
	}

	if workingCopy {
		return checkWorkingCopy(cmd, engines, moduleName)
	}

	return checkMergeRequest(cmd, engines, clients, moduleName, rawIID)
}

func checkMergeRequest(
	cmd *cobra.Command, engines adapter.Factory, clients cli.GitlabClients,
	moduleName, rawIID string,
) (int, error) {
	where, context, err := resolveMrContext(cmd, clients, moduleName, rawIID)
	if err != nil {
		return 0, err
	}
	describeContext(cmd, context, cli.Flag(cmd, cli.FlagVersion))

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := ensureEnvironment(cmd, engine, context.Module, context.CoreMajor)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Merge request")
	if err := engine.ApplyMr(environment, context.MergeRequest); err != nil {
		return 0, err
	}

	if err := loadFixtureIfAsked(cmd, engine, environment); err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Checks")
	run, err := engine.RunChecks(environment, nil)
	if err != nil {
		return 0, err
	}

	renderCheckSummary(cmd, run)
	cacheVerdict(cmd, where, context, run)

	return workflow.ForRun(run), nil
}

// checkWorkingCopy runs the same suite against whatever the working copy is
// on.
//
// Deliberately touches neither GitLab nor drupal.org: the question this
// answers — "is what I have in front of me green?" — is one a maintainer has
// every right to ask offline, and on a branch no remote has heard of.
func checkWorkingCopy(cmd *cobra.Command, engines adapter.Factory, moduleName string) (int, error) {
	where, module, coreMajor, err := resolveSubject(cmd, moduleName)
	if err != nil {
		return 0, err
	}

	engine, err := cli.Engine(cmd, engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := ensureEnvironment(cmd, engine, module, coreMajor)
	if err != nil {
		return 0, err
	}

	branch, err := describeWorkingCopy(cmd, engine, module, coreMajor)
	if err != nil {
		return 0, err
	}

	if err := loadFixtureIfAsked(cmd, engine, environment); err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Checks")
	run, err := engine.RunChecks(environment, nil)
	if err != nil {
		return 0, err
	}

	renderCheckSummary(cmd, run)

	on := ""
	if branch != "" {
		on = " on " + branch
	}
	cli.Printf(cmd,
		"Checked the working copy%s. Nothing was cached: a working copy has no revision the "+
			"dashboard could tell fresh from stale.\n", on)

	return workflow.ForRun(run), nil
}

// describeWorkingCopy names the branch under test, and warns about a dirty
// one.
//
// A tree with uncommitted changes is checkable but not *reportable*: the run
// would describe a state that exists only in that moment, and the first thing
// anybody does with a green result is act on it. Saying what is uncommitted
// costs one line and prevents that.
func describeWorkingCopy(
	cmd *cobra.Command, engine adapter.Engine, module cockpit.Module, coreMajor string,
) (string, error) {
	status, found := engine.InspectWorkingCopy(module.Name, coreMajor)
	if !found {
		return "", fmt.Errorf(
			"the working copy for %s on core %s could not be read, so there is nothing to check. "+
				"Start something first: upkeep start %s <issue>",
			module.Name, coreMajor, module.Name,
		)
	}

	branch := status.CurrentBranch
	shown := branch
	if shown == "" {
		shown = "(detached HEAD)"
	}
	cli.Progressf(cmd, "Working copy branch: %s", shown)

	if status.IsDirty() {
		cli.Warnf(cmd, "Uncommitted changes are included in this run:")
		for _, reason := range status.Describe() {
			cli.Progressf(cmd, "  - %s", reason)
		}
	}

	return branch, nil
}

// cacheVerdict persists the run where the dashboard's LOCAL column and the
// fast-lane gate read it.
//
// Keyed on the merge, not the branch: that is the tree that was checked, and
// it moves when *either* side does. Keyed on the head SHA alone, the entry
// would still read as current after the target gained a commit — which is the
// same evidence-about-another-tree problem the merge ref exists to solve.
//
// No revision at all means no entry: an entry keyed on a guess is one
// staleness checks could never match, which is worse than no evidence.
func cacheVerdict(
	cmd *cobra.Command, where *cockpit.Cockpit, context workflow.MrContext, run check.RunResult,
) {
	sha := gitlab.MergeRevision(context.MergeRefSHA, context.MergeRequest.HeadSHA)
	if sha == "" {
		cli.Warnf(cmd,
			"The MR has no merge-ref or head SHA; results were NOT cached "+
				"(the dashboard could never tell fresh from stale).")

		return
	}

	key, err := results.MergeRequestKey(context.MergeRequest.IID)
	if err != nil {
		cli.Warnf(cmd, "Results were NOT cached: %s", err)

		return
	}

	if err := results.NewCache(where.ResultsPath()).Store(
		context.Module.Name, key, context.CoreMajor, sha, run, time.Now(),
	); err != nil {
		cli.Warnf(cmd, "Results were NOT cached: %s", err)

		return
	}

	cli.Progressf(cmd, "Results cached: %s/%s/%s/%s/%s.json",
		where.ResultsPath(), context.Module.Name, key.Segment, context.CoreMajor, sha)
}
