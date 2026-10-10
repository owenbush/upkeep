// Package command holds one file per CLI command.
package command

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/filesystem"
	"github.com/owenbush/upkeep/internal/workflow"
)

// registryTemplate is what a fresh cockpit's registry says.
//
// A commented example rather than an empty mapping alone: the first thing
// anybody does after `init` is add a module, and a file that shows the shape
// is the difference between that working and a trip to the docs.
const registryTemplate = `# Upkeep cockpit module registry.
#
# Each key under "modules" is a module machine name. Required fields:
#   project:       git.drupalcode.org project path, e.g. "project/token_or"
#   core_versions: non-empty list of Drupal core versions the module is
#                  maintained for, e.g. ["10", "11"]
#
# Example entry:
#
# modules:
#   token_or:
#     project: project/token_or
#     core_versions: ["10", "11"]
modules: {}
`

// NewInit builds the init command.
func NewInit() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Scaffold a new cockpit",
		Long: "Scaffold a new cockpit: module registry, base-artifacts/, fixtures/, " +
			"and projects/ directories.",
		Args: cobra.MaximumNArgs(1),
	}
	cmd.RunE = cli.Run(runInit)

	return cmd
}

func runInit(cmd *cobra.Command, args []string) (int, error) {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}

	where, err := cockpit.New(dir)
	if err != nil {
		return 0, err
	}

	// A regular file, not merely something existing: a *directory* named
	// registry.yml is not a cockpit, and claiming it is would send somebody
	// looking for one. The write below is what reports that path as unusable.
	if info, err := os.Stat(where.RegistryPath()); err == nil && info.Mode().IsRegular() {
		return 0, fmt.Errorf(
			"a cockpit already exists at %q (found %s)", where.Root, cockpit.RegistryFilename,
		)
	}

	// Every write is checked. A cockpit reported as created but missing its
	// registry is the worst outcome here, because the next command then
	// reports a missing registry instead of a failed init.
	if err := scaffold(where); err != nil {
		return 0, fmt.Errorf("could not scaffold the cockpit at %q: %w", where.Root, err)
	}

	cli.Printf(cmd, "Cockpit created at %q: %s, %s/, %s/, %s/.\n",
		where.Root, cockpit.RegistryFilename,
		cockpit.BaseArtifactsDir, cockpit.FixturesDir, cockpit.ProjectsDir)

	return workflow.OK, nil
}

// scaffold lays out the cockpit's directories and its registry.
func scaffold(where *cockpit.Cockpit) error {
	for _, dir := range []string{
		where.Root, where.BaseArtifactsPath(), where.FixturesPath(), where.ProjectsPath(),
	} {
		if err := filesystem.EnsureDirectory(dir, filesystem.ModeSharedDir); err != nil {
			return err
		}
	}

	if err := filesystem.Write(
		where.RegistryPath(), []byte(registryTemplate), filesystem.ModeShared,
	); err != nil {
		return err
	}

	// The three directories are empty by design and git does not track empty
	// directories, so a cockpit kept in version control would lose them.
	for _, dir := range []string{
		where.BaseArtifactsPath(), where.FixturesPath(), where.ProjectsPath(),
	} {
		if err := filesystem.Write(
			filepath.Join(dir, ".gitkeep"), nil, filesystem.ModeShared,
		); err != nil {
			return err
		}
	}

	return nil
}
