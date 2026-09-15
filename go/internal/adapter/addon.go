package adapter

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Identity and pinning of the engine add-on (ddev-drupal-contrib).
//
// EngineAddOnVersion is the single configuration point for the pinned add-on
// release. Upgrading is a deliberate adapter-maintenance event, never routine:
//
//  1. Read the new release's diff — especially install.yaml,
//     config.contrib.yaml, and the commands/ scripts the adapter shells out to
//     (phpunit, phpcs, phpstan, core-version).
//  2. Refresh the shipped-config fixture in the tests with the new
//     config.contrib.yaml and make AdaptContribConfig pass against it.
//  3. Bump the version here; existing environments then self-report add-on
//     skew through EnvironmentMeta.StaleReasons and are re-provisioned on the
//     next ensure rather than being silently reused.
//  4. Live-verify one full ensure, run-checks, teardown cycle.
const (
	EngineAddOnName    = "ddev/ddev-drupal-contrib"
	EngineAddOnVersion = "1.1.5"

	// EngineAddOnConfigFilename is the add-on config file it installs into
	// <project>/.ddev/, adapted after every add-on installation.
	EngineAddOnConfigFilename = "config.contrib.yaml"

	// EngineProjectsPath is where the add-on's check commands look for the
	// module, relative to the docroot: the adaptation repoints
	// DRUPAL_PROJECTS_PATH here, and the adapter's check invocations scope
	// themselves to <PROJECTS_PATH>/<module> beneath it.
	EngineProjectsPath = "modules/contrib"
)

// AdaptContribConfig adapts the shipped config.contrib.yaml to upkeep's
// seeded-tree layout.
//
// The add-on assumes the MODULE is the project root ("module as the centre of
// the universe"): its post-start hook symlinks all project-root files into the
// docroot via symlink-project, and its check commands target
// web/<DRUPAL_PROJECTS_PATH>. Upkeep environments instead seed the project
// root from the canonical base tree and wire the module in with a Composer
// path repository, so:
//
//   - the post-start symlink-project hook is removed (against a full project
//     tree it would symlink the whole codebase into itself), and
//   - DRUPAL_PROJECTS_PATH is repointed at modules/contrib, where the
//     path-repository install lands the module symlink — the add-on's
//     phpunit/phpcs/phpstan commands then target exactly the module under
//     maintenance, the seeded tree being otherwise module-free.
//
// The #ddev-generated marker is kept: a future add-on installation may clobber
// the file, which is why the adapter re-runs this adaptation after every one.
func AdaptContribConfig(shippedYAML string) (string, error) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(shippedYAML), &node); err != nil {
		return "", fmt.Errorf("engine add-on config is not valid YAML; refusing to adapt it: %w", err)
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("engine add-on config is not a YAML mapping; refusing to adapt it")
	}

	// Edited as nodes rather than decoded into a map, so every key the add-on
	// ships survives in the order it shipped them — the file is one a person
	// may read, and a round trip through a Go map would reorder it and drop
	// anything this does not model.
	mapping := node.Content[0]
	adapted := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]

		if key.Value == "hooks" {
			continue
		}
		if key.Value == "web_environment" {
			value = repointProjectsPath(value)
		}
		adapted.Content = append(adapted.Content, key, value)
	}

	// Comments are dropped, which is what makes re-adapting idempotent: the
	// adapter re-runs this after every add-on installation, and the header
	// below is prepended each time. PHP gets this for free — its YAML dump
	// emits data and nothing else — while yaml.v3 nodes carry the comments
	// they were parsed with, so a second pass would stack a second header on
	// the first.
	stripComments(adapted)

	encoded, err := yaml.Marshal(adapted)
	if err != nil {
		return "", fmt.Errorf("cannot render the adapted add-on config: %w", err)
	}

	return "#ddev-generated\n" +
		"# Adapted by upkeep for the seeded-tree layout (see internal/adapter).\n" +
		string(encoded), nil
}

// stripComments clears every comment in a node tree.
func stripComments(node *yaml.Node) {
	if node == nil {
		return
	}
	node.HeadComment, node.LineComment, node.FootComment = "", "", ""
	for _, child := range node.Content {
		stripComments(child)
	}
}

// repointProjectsPath rewrites the DRUPAL_PROJECTS_PATH entry and leaves every
// other one exactly as found.
//
// Parsed YAML is untrusted: entries are KEY=value strings in every add-on
// release seen so far, but anything else is left alone rather than coerced.
func repointProjectsPath(environment *yaml.Node) *yaml.Node {
	if environment.Kind != yaml.SequenceNode {
		return environment
	}

	rewritten := &yaml.Node{Kind: yaml.SequenceNode}
	for _, entry := range environment.Content {
		if entry.Kind == yaml.ScalarNode && strings.HasPrefix(entry.Value, "DRUPAL_PROJECTS_PATH=") {
			rewritten.Content = append(rewritten.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: "DRUPAL_PROJECTS_PATH=" + EngineProjectsPath,
			})

			continue
		}
		rewritten.Content = append(rewritten.Content, entry)
	}

	return rewritten
}
