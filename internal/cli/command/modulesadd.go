package command

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/workflow"
)

// NewModulesAdd builds the modules:add command.
//
// Registers maintained modules into the cockpit registry from the token
// holder's GitLab project memberships — list what you maintain, opt in, done.
// Read-only against GitLab; the only thing it writes is registry.yml.
func NewModulesAdd(clients cli.GitlabClients, prompts func(*cobra.Command) cli.Prompt) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "modules:add [module...]",
		Short: "Register maintained modules from your git.drupalcode.org project memberships",
		Long: `Lists the projects you are a member of on git.drupalcode.org and registers
the ones you choose, so the survey commands cover them.

  upkeep modules:add                    pick from your memberships
  upkeep modules:add token pathauto     register these without prompting
  upkeep modules:add --core-versions=10,11

Reads GitLab and writes registry.yml, and nothing else.`,
	}
	cli.AddCockpit(cmd)
	cmd.Flags().String("core-versions", "11",
		`Comma-separated core majors the new entries track (e.g. "10,11")`)

	cmd.RunE = cli.Run(func(cmd *cobra.Command, args []string) (int, error) {
		return runModulesAdd(cmd, clients, prompts, args)
	})

	return cmd
}

func runModulesAdd(
	cmd *cobra.Command, clients cli.GitlabClients, prompts func(*cobra.Command) cli.Prompt,
	requested []string,
) (int, error) {
	where, err := cli.Cockpit(cmd)
	if err != nil {
		return 0, err
	}
	registered, err := cli.Modules(where)
	if err != nil {
		return 0, err
	}

	// Memberships are a question about *you*, which GitLab will not answer
	// anonymously — the one read on this surface that genuinely needs a
	// credential.
	client, err := cli.WritingClient(cmd, clients)
	if err != nil {
		return 0, err
	}

	// Checked before the round trip: an empty --core-versions would register
	// entries tracking nothing, and finding that out after the listing wastes
	// the listing.
	coreVersions := splitCoreVersions(cli.Flag(cmd, "core-versions"))
	if len(coreVersions) == 0 {
		return 0, fmt.Errorf(
			`--core-versions must name at least one core major, e.g. "11" or "10,11"`)
	}

	projects, failure := client.MembershipProjects()
	if failure != nil {
		return 0, fmt.Errorf("could not list your project memberships [%s]: %s",
			failure.ShortCode(), failure.Message)
	}

	candidates, names := unregisteredContrib(projects, registered)
	if len(names) == 0 {
		cli.Progressf(cmd, "Every project/ membership is already registered — nothing to add.")

		return workflow.OK, nil
	}

	chosen, err := chooseModules(cmd, prompts, requested, candidates, names, registered)
	if err != nil {
		return 0, err
	}
	if len(chosen) == 0 {
		cli.Progressf(cmd, "Nothing selected; registry unchanged.")

		return workflow.OK, nil
	}

	modules := make([]cockpit.Module, 0, len(chosen))
	for _, name := range chosen {
		modules = append(modules, cockpit.Module{
			Name: name, Project: candidates[name].PathWithNamespace,
			CoreVersions: coreVersions, Watched: true,
		})
	}

	// A registry that was not written must never be reported as "Registered N
	// module(s)", so the write's failure is the command's.
	added, err := cockpit.NewEditor(where.RegistryPath()).Add(modules)
	if err != nil {
		return 0, err
	}

	cli.Progressf(cmd, "Registered %d module(s) tracking core %s: %s",
		len(added), strings.Join(coreVersions, ", "), strings.Join(added, ", "))
	cli.Progressf(cmd, "Run `upkeep dashboard` to see their open merge requests.")

	return workflow.OK, nil
}

// unregisteredContrib is the memberships worth offering, and their names in
// the order they are offered.
//
// Contrib modules live under project/; the machine name is the path. A
// membership of anything else — a group's own repository, an issue fork, a
// sandbox — is not a module this tool maintains.
func unregisteredContrib(
	projects []gitlab.Project, registered map[string]cockpit.Module,
) (map[string]gitlab.Project, []string) {
	candidates := map[string]gitlab.Project{}
	for _, project := range projects {
		if !strings.HasPrefix(project.PathWithNamespace, "project/") {
			continue
		}
		if _, already := registered[project.Path]; already {
			continue
		}
		candidates[project.Path] = project
	}

	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	// Name order, so two runs offer the same list in the same order and a
	// number typed at the prompt means the same thing twice.
	sort.Strings(names)

	return candidates, names
}

// chooseModules is which of the candidates this run registers.
func chooseModules(
	cmd *cobra.Command,
	prompts func(*cobra.Command) cli.Prompt,
	requested []string,
	candidates map[string]gitlab.Project,
	names []string,
	registered map[string]cockpit.Module,
) ([]string, error) {
	if len(requested) > 0 {
		return namedModules(requested, candidates, registered)
	}

	prompt := prompts(cmd)
	if !prompt.Interactive() {
		return nil, fmt.Errorf(
			"pass module machine names as arguments when running non-interactive "+
				"(no prompt available).\nYours to choose from: %s", strings.Join(names, ", "))
	}

	return prompt.ChooseMany(fmt.Sprintf(
		"Which modules should be registered? (%d unregistered membership(s) found)", len(names),
	), names), nil
}

// namedModules resolves names given on the command line.
//
// A name already registered is accepted and quietly does nothing: re-running
// with the same arguments is how somebody adds one more to a list they
// already typed, and refusing that would make the command unrepeatable. A name
// that is neither registered nor a membership is refused, because it is a typo
// or a project somebody does not maintain, and inventing a registry entry for
// it would send every survey command looking for a project that is not theirs.
func namedModules(
	requested []string, candidates map[string]gitlab.Project, registered map[string]cockpit.Module,
) ([]string, error) {
	var unknown, chosen []string
	for _, name := range requested {
		switch {
		case candidates[name].Path != "":
			chosen = append(chosen, name)
		case registered[name].Name != "":
			// Already there; nothing to do for it.
		default:
			unknown = append(unknown, name)
		}
	}

	if len(unknown) > 0 {
		return nil, fmt.Errorf(
			"not among your project/ memberships: %s. Run without arguments to pick interactively",
			strings.Join(unknown, ", "))
	}

	return chosen, nil
}

// splitCoreVersions reads the comma-separated list, discarding blanks.
func splitCoreVersions(raw string) []string {
	versions := []string{}
	for _, version := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(version); trimmed != "" {
			versions = append(versions, trimmed)
		}
	}

	return versions
}
