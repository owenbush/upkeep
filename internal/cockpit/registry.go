package cockpit

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/owenbush/upkeep/internal/naming"
)

// Module is a single registered module: what it is called, where it lives,
// which cores it tracks.
type Module struct {
	Name         string
	Project      string
	CoreVersions []string

	// Watched says whether this came from the registry rather than being
	// derived.
	//
	// Provenance, because it changes what a refusal can honestly say. A
	// watched module's core_versions is a line in registry.yml a maintainer
	// wrote; a derived module's is the list of base artifacts on this machine.
	// Telling somebody to "add it to core_versions in registry.yml" for a
	// module that has no entry there sends them to edit a file that does not
	// mention it.
	Watched bool
}

// Registry is the loaded and validated cockpit module registry.
//
// Validation is total: every key and value that later becomes a path segment
// or a project name is checked here, at load, so downstream consumers — the
// results cache, the dashboard cache, prune — can treat registry content as
// trusted and no command has to re-validate it.
type Registry struct {
	modules map[string]Module
	// order is the machine names in the order the file listed them, because
	// Go map iteration is random and the surveys render in file order.
	order []string
}

// registryFile is the on-disk shape. The values stay yaml.Node so a wrong type
// is reported by this loader, with the module named, rather than by the
// decoder with a line number and no context.
type registryFile struct {
	Modules yaml.Node `yaml:"modules"`
}

// RegistryFromFile loads and validates a registry.
func RegistryFromFile(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"module registry not found at %q. Run \"upkeep init\" to create a cockpit, or point --cockpit / "+
					"%s at an existing one", path, EnvVar,
			)
		}

		return nil, fmt.Errorf("module registry %q cannot be read: %w", path, err)
	}

	var file registryFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("module registry %q is not valid YAML: %w", path, err)
	}

	// A missing top-level "modules" key leaves the node zero. An explicitly
	// null one (`modules:` with nothing under it) is the scaffold's own empty
	// registry and is legitimate.
	if file.Modules.Kind == 0 {
		return nil, fmt.Errorf(
			"module registry %q must be a YAML mapping with a top-level \"modules\" key", path,
		)
	}
	if file.Modules.Tag == "!!null" {
		return &Registry{modules: map[string]Module{}}, nil
	}
	if file.Modules.Kind != yaml.MappingNode {
		return nil, fmt.Errorf(
			"the \"modules\" key in %q must be a mapping of machine name to module definition", path,
		)
	}

	registry := &Registry{modules: map[string]Module{}}
	// A mapping node's Content alternates key, value.
	for i := 0; i+1 < len(file.Modules.Content); i += 2 {
		module, err := buildModule(path, file.Modules.Content[i].Value, file.Modules.Content[i+1])
		if err != nil {
			return nil, err
		}
		if _, seen := registry.modules[module.Name]; !seen {
			registry.order = append(registry.order, module.Name)
		}
		registry.modules[module.Name] = module
	}

	return registry, nil
}

func buildModule(path, name string, definition *yaml.Node) (Module, error) {
	// The machine name becomes a path segment (<cockpit>/results/<module>/,
	// <cockpit>/cache/dashboard/<module>.json) and an engine project name.
	// Validating it here, at load, is the single point that keeps a traversal
	// sequence out of every derived path — and keeps the failure from
	// surfacing deep inside prune, after environments have already been torn
	// down.
	if !naming.IsModuleName(name) {
		return Module{}, fmt.Errorf(
			"module key %q in %q is not a Drupal machine name ([a-z][a-z0-9_]*). Registry keys are used as "+
				"directory names and engine project names, so they must be machine names", name, path,
		)
	}

	if definition.Kind != yaml.MappingNode {
		return Module{}, fmt.Errorf(
			"module %q in %q must be a mapping with \"project\" and \"core_versions\" keys", name, path,
		)
	}

	var entry struct {
		Project      string    `yaml:"project"`
		CoreVersions yaml.Node `yaml:"core_versions"`
	}
	if err := definition.Decode(&entry); err != nil {
		return Module{}, fmt.Errorf(
			"module %q in %q must be a mapping with \"project\" and \"core_versions\" keys: %w", name, path, err,
		)
	}

	if entry.Project == "" {
		return Module{}, fmt.Errorf(
			"module %q in %q is missing a non-empty \"project\" (its git.drupalcode.org project path, "+
				"e.g. %q)", name, path, "project/"+name,
		)
	}

	if entry.CoreVersions.Kind != yaml.SequenceNode || len(entry.CoreVersions.Content) == 0 {
		return Module{}, fmt.Errorf(
			"module %q in %q must declare \"core_versions\" as a non-empty list (e.g. [\"10\", \"11\"])", name, path,
		)
	}

	versions := make([]string, 0, len(entry.CoreVersions.Content))
	for _, node := range entry.CoreVersions.Content {
		// Core versions are path segments too
		// (results/<module>/<subject>/<core>).
		if node.Kind != yaml.ScalarNode || !naming.IsCoreMajor(node.Value) {
			return Module{}, fmt.Errorf(
				"module %q in %q lists a \"core_versions\" entry that is not a whole major version number "+
					"(e.g. \"11\"): %s", name, path, describeNode(node),
			)
		}
		versions = append(versions, node.Value)
	}

	return Module{Name: name, Project: entry.Project, CoreVersions: versions, Watched: true}, nil
}

// describeNode quotes a scalar and names the shape of anything else, so the
// complaint says what was there rather than printing "Array".
func describeNode(node *yaml.Node) string {
	switch node.Kind {
	case yaml.ScalarNode:
		return fmt.Sprintf("%q", node.Value)
	case yaml.SequenceNode:
		return "a list"
	case yaml.MappingNode:
		return "a mapping"
	default:
		return "an unreadable value"
	}
}

// Modules is every registered module, keyed by machine name.
func (r *Registry) Modules() map[string]Module {
	copied := make(map[string]Module, len(r.modules))
	for name, module := range r.modules {
		copied[name] = module
	}

	return copied
}

// Names is the registered machine names in the order the file listed them.
func (r *Registry) Names() []string {
	names := make([]string, len(r.order))
	copy(names, r.order)

	return names
}

// Find is one registered module.
func (r *Registry) Find(name string) (Module, bool) {
	module, found := r.modules[name]

	return module, found
}
