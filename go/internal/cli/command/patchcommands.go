package command

import (
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/patches"
	"github.com/owenbush/upkeep/internal/results"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewPatchApply builds the patch:apply command.
//
// Download a patch from a drupal.org issue, apply it in the module's
// environment, and get out of the way. The manual half of the patch flow: for
// looking at the change, clicking through the site, or running something the
// suite does not cover.
//
// Prints the environment path on stdout and nothing else, so it composes:
// `cd $(upkeep patch:apply widget 3597808)`.
func NewPatchApply(surface PatchSurface) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "patch:apply <module> <issue>",
		Short: "Download a patch from a drupal.org issue and apply it in the module environment",
		Long: "Download a patch from a drupal.org issue and apply it in the module environment.\n\n" +
			"`patch:check` is the same resolution and apply followed by the full suite.\n\n" +
			"stdout carries the environment path and nothing else, so it composes:\n" +
			"  cd $(upkeep patch:apply widget 3597808)",
		Args: cobra.ExactArgs(2),
	}
	addPatchSurface(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runPatchApply(cmd, surface, args[0], args[1])
	})

	return cmd
}

func runPatchApply(
	cmd *cobra.Command, surface PatchSurface, moduleName, rawNid string,
) (int, error) {
	where, context, err := resolvePatchContext(cmd, surface, moduleName, rawNid)
	if err != nil {
		return 0, err
	}
	describePatchContext(cmd, context, cli.Flag(cmd, cli.FlagVersion))

	engine, err := cli.Engine(cmd, surface.Engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := ensureEnvironment(cmd, engine, context.Module, context.CoreMajor)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Patch")
	if err := engine.ApplyPatch(environment, context.Application(), cli.BaseRefresh(cmd)); err != nil {
		return 0, err
	}

	cli.Progressf(cmd,
		"Applied %s on branch %s. Run checks with: upkeep patch:check %s %d --version=%s",
		context.Patch.Name, context.Application().BranchName(),
		context.Module.Name, context.Issue.Nid, context.CoreMajor)

	cli.Println(cmd, environment.ProjectPath)

	return workflow.OK, nil
}

// NewPatchCheck builds the patch:check command.
//
// The patch-side workhorse: one patch file through the same isolated flow a
// merge request gets. Deliberately the mirror of `check`, because the point is
// that a patch contribution should cost a maintainer no more than a branch
// does.
//
// Results are cached exactly as a merge request's are, but under the patch key
// namespace and keyed by the patch's revision — so the dashboard can show a
// patch row's local state and tell a fresh verdict from one about a superseded
// re-roll. The namespace is what keeps that evidence away from the fast-lane
// gate, which only ever reads merge-request entries: a patch is not something
// upkeep can merge, and its verdict must never look like grounds for merging a
// branch.
func NewPatchCheck(surface PatchSurface) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "patch:check <module> <issue>",
		Short: "Run one drupal.org patch through the full isolated check flow",
		Long: `The patch-side counterpart of check: downloads a patch from a drupal.org
issue, applies it onto a branch off the base, and runs the full suite.

  upkeep patch:check pathauto 3597857
  upkeep patch:check pathauto 3597857 --latest   take the newest patch without asking
  upkeep patch:check pathauto 3597857 --file=NAME

With several patches on the issue and no terminal to ask at, the newest is
taken, and said so.

Exits 0 all green, 1 a check failed, 2 it could not run at all — which
includes a patch that does not apply, because no verdict on the contribution
was produced.`,
		Args: cobra.ExactArgs(2),
	}
	addPatchSurface(cmd)
	cmd.Flags().String("fixture", "",
		"Load this named fixture into the database before running checks")
	cli.AddFixtureCompletion(cmd)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runPatchCheck(cmd, surface, args[0], args[1])
	})

	return cmd
}

func runPatchCheck(
	cmd *cobra.Command, surface PatchSurface, moduleName, rawNid string,
) (int, error) {
	where, context, err := resolvePatchContext(cmd, surface, moduleName, rawNid)
	if err != nil {
		return 0, err
	}
	describePatchContext(cmd, context, cli.Flag(cmd, cli.FlagVersion))

	engine, err := cli.Engine(cmd, surface.Engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := ensureEnvironment(cmd, engine, context.Module, context.CoreMajor)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Patch")
	if err := engine.ApplyPatch(environment, context.Application(), cli.BaseRefresh(cmd)); err != nil {
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

	if !run.AllPassed() {
		// The drupal.org API is read-only, so the status change is the
		// maintainer's to make; naming the issue is the most this can do.
		cli.Printf(cmd,
			"Report it at https://www.drupal.org/node/%d — the drupal.org API is read-only, "+
				"so the status change is a browser action.\n", context.Issue.Nid)
	}

	key, err := results.PatchKey(context.Issue.Nid)
	if err != nil {
		cli.Warnf(cmd, "Results were NOT cached: %s", err)

		return workflow.ForRun(run), nil
	}

	revision := context.Revision()
	if err := results.NewCache(where.ResultsPath()).Store(
		context.Module.Name, key, context.CoreMajor, revision, run, time.Now(),
	); err != nil {
		cli.Warnf(cmd, "Results were NOT cached: %s", err)

		return workflow.ForRun(run), nil
	}

	cli.Progressf(cmd, "Results cached: %s/%s/%s/%s/%s.json",
		where.ResultsPath(), context.Module.Name, key.Segment, context.CoreMajor, revision)

	return workflow.ForRun(run), nil
}

// promoterEnvVar names whoever is doing the carrying, for the commit's
// courtesy line.
const promoterEnvVar = "UPKEEP_PROMOTER"

// NewPatchPromote builds the patch:promote command.
//
// Turns a patch contribution into a branch a merge request can be opened from.
// The gap it closes: a patch and a merge request carry the same work, but only
// one of them gets CI, review threads, or a fast lane, and re-rolling a patch
// into a branch by hand is a dozen git commands nobody enjoys.
//
// **It stops at the commit.** The branch is made locally and nothing leaves
// the machine; `upkeep publish` is the outward-facing half. Splitting there is
// not squeamishness — it is the same seam every other verb uses, and it means
// the step that publishes somebody else's work under your account is one a
// human types.
//
// **Attribution is the feature, not decoration.** Promoting moves another
// person's change into history under whoever pushes it. What the commit says
// is the only durable record of whose work it was, so the author is resolved
// before anything is applied and an unattributable patch is reported rather
// than quietly promoted as if it were yours.
func NewPatchPromote(surface PatchSurface) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "patch:promote <module> <issue>",
		Short: "Apply a drupal.org patch onto an issue work branch, credited to its author",
		Long: `Converts a patch contribution into a branch you can open a merge request from.

  upkeep patch:promote pathauto 3597857
  upkeep patch:promote pathauto 3597857 --latest
  upkeep patch:promote pathauto 3597857 --file=NAME
  upkeep patch:promote pathauto 3597857 --partial

The commit credits whoever posted the patch, by name, in the message —
promoting moves somebody else's work into history, and the commit is the
durable record of whose it is. Nothing is pushed: run upkeep publish
afterwards, which is the step that puts it on drupal.org.

Afterwards, upkeep check <module> --working-copy runs the suite against the
branch it made, and upkeep dev <module> prints the site URL. A patch that only
applied with reduced context is a weaker guarantee than a merge request
implies, so checking before you publish is worth the minutes.

--partial is for a patch that will not apply at all. Every hunk that still fits
lands on the branch and the rest is left as <file>.rej beside the file it could
not change, which is where a re-roll starts. Nothing is committed — the commit
carries the patch author's name, and half their patch is not what they wrote —
so resolve the rejects, delete the .rej files, commit, and publish. It exits 1,
because the patch did not apply.`,
		Args: cobra.ExactArgs(2),
	}
	addPatchSurface(cmd)
	cmd.Flags().String("branch", "",
		"Work branch to promote onto (default: the drupal.org <nid>-<slug> convention)")
	// The re-roll door. Without it a patch that no longer applies is a dead
	// end: the report says which files are stale and stops, while the work of
	// re-rolling is exactly the work the failed apply was doing.
	cmd.Flags().Bool("partial", false,
		"When the patch will not apply, keep the hunks that still fit and leave the rest as "+
			".rej files to resolve by hand — the start of a re-roll rather than a refusal")

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runPatchPromote(cmd, surface, args[0], args[1])
	})

	return cmd
}

func runPatchPromote(
	cmd *cobra.Command, surface PatchSurface, moduleName, rawNid string,
) (int, error) {
	where, context, err := resolvePatchContext(cmd, surface, moduleName, rawNid)
	if err != nil {
		return 0, err
	}
	describePatchContext(cmd, context, cli.Flag(cmd, cli.FlagVersion))

	attribution := attribute(cmd, surface, context)
	cli.Progressf(cmd, "%s", attribution.Subject())

	branch := adapter.IssueBranchFor(context.Issue.Nid, context.Issue.Title)
	if named := cli.Flag(cmd, "branch"); named != "" {
		branch = adapter.NamedIssueBranch(context.Issue.Nid, named)
	}

	engine, err := cli.Engine(cmd, surface.Engines, where)
	if err != nil {
		return 0, err
	}

	environment, err := ensureEnvironment(cmd, engine, context.Module, context.CoreMajor)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Promote")
	promotion, err := engine.PromotePatch(
		environment, context.Application(), branch, attribution.Message(),
		cli.BaseRefresh(cmd), cli.Switched(cmd, "partial"),
	)
	if err != nil {
		return 0, err
	}

	if !promotion.IsComplete() {
		return reportPartialPromotion(cmd, context, branch, promotion), nil
	}

	sha, err := promotion.RequireSHA()
	if err != nil {
		return 0, err
	}

	cli.Printf(cmd, "%s now carries the patch at %s.\n", branch.Name, shortSHA(sha))
	cli.Println(cmd, attribution.Message())
	cli.Println(cmd,
		"Nothing has been pushed. Run the checks against it, look at the site, then publish:")
	cli.Printf(cmd, "  upkeep check %s --working-copy\n", context.Module.Name)
	cli.Printf(cmd, "  upkeep dev %s\n", context.Module.Name)
	cli.Printf(cmd, "  upkeep publish %s %d\n", context.Module.Name, context.Issue.Nid)

	return workflow.OK, nil
}

// reportPartialPromotion says what landed, what did not, and what to do about
// it.
//
// Exit 1 rather than 0. The patch did not apply, which is the supervised work
// failing — the same answer patch:check gives — and a script that treated this
// as success would push a half-applied patch. Nothing here is an upkeep
// failure, so it is not exit 2 either.
func reportPartialPromotion(
	cmd *cobra.Command,
	context workflow.PatchContext,
	branch adapter.IssueBranch,
	promotion adapter.PatchPromotion,
) int {
	cli.Warnf(cmd,
		"The patch did not apply cleanly. %d file(s) landed on %s; %d still need doing by hand.",
		len(promotion.Applied), branch.Name, len(promotion.Rejected))

	if len(promotion.Applied) > 0 {
		cli.Println(cmd, "Applied:")
		for _, file := range promotion.Applied {
			cli.Println(cmd, "  "+file)
		}
	}

	cli.Println(cmd,
		"Rejected — each has a .rej file beside it holding the hunks that did not fit:")
	for _, file := range promotion.Rejected {
		cli.Printf(cmd, "  %s  (%s.rej)\n", file, file)
	}

	cli.Println(cmd, "")
	cli.Println(cmd,
		"Nothing is committed: the commit carries the patch author's name, and this is not")
	cli.Println(cmd, "their work yet. Resolve the rejects, delete the .rej files, then:")
	cli.Printf(cmd, "  upkeep dev %s\n", context.Module.Name)
	cli.Printf(cmd, "  upkeep check %s --working-copy\n", context.Module.Name)
	cli.Printf(cmd, "  upkeep publish %s %d\n", context.Module.Name, context.Issue.Nid)

	return workflow.Failed
}

// attribute resolves the patch's author, and says plainly when it cannot.
//
// A miss is not fatal — the patch is still somebody's work and the commit
// still says so — but it is never silent, because a commit that dropped the
// name is indistinguishable from one that never had a name to drop.
func attribute(
	cmd *cobra.Command, surface PatchSurface, context workflow.PatchContext,
) patches.Attribution {
	var author *drupal.User
	if context.Patch.OwnerUID != 0 {
		if found, ok := surface.Issues.Issues().User(context.Patch.OwnerUID); ok {
			author = &found
		}
	}

	if author == nil {
		cli.Warnf(cmd,
			"drupal.org records no readable account for this patch file, so the commit cannot "+
				"name its author. It will say the work is not the promoter's and point at the "+
				"issue — credit them there, on the issue, which is where drupal.org allocates "+
				"credit anyway.")
	} else {
		cli.Progressf(cmd, "Patch posted by %s (%s).", author.Name, author.ProfileURL)
	}

	return patches.AttributionFor(context.Issue, context.Patch, author, promoter())
}

// promoter is who is doing the carrying.
//
// Best-effort: it is a courtesy line in a commit message, so an unset identity
// omits it rather than failing the promotion.
func promoter() string { return strings.TrimSpace(os.Getenv(promoterEnvVar)) }

// shortSHA abbreviates a commit for a progress line.
func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}

	return sha[:8]
}
