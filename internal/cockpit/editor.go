package cockpit

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// Editor appends modules to an existing registry file.
//
// Writes go through parse, merge, validate, publish: the merged registry is
// written to a sibling temporary file, re-validated *from that file* with the
// same rules the loader enforces, and only then renamed over the real
// registry. Both halves matter — validation stops a semantically bad entry,
// and the rename stops a torn write, so neither a rejected entry nor a crash
// mid-write can corrupt the tool's single source of truth.
//
// Dumping regenerates the YAML, so hand-written comments in the file do not
// survive an add — the scaffold's commented example is consumed the first time
// this writes.
type Editor struct {
	registryPath string
}

// NewEditor opens the registry at path for appending.
func NewEditor(registryPath string) *Editor { return &Editor{registryPath: registryPath} }

// entry is the on-disk shape of one module, and the order of its fields is the
// order they are written in.
type entry struct {
	Project      string   `yaml:"project"`
	CoreVersions []string `yaml:"core_versions"`
}

// Add adds the given modules, skipping any machine name already registered
// (existing definitions always win). It returns the names actually added.
func (e *Editor) Add(modules []Module) ([]string, error) {
	existing, err := RegistryFromFile(e.registryPath)
	if err != nil {
		return nil, err
	}

	// A YAML mapping node, built by hand rather than from a Go map, because a
	// map has no order and the registry is a file a person reads: existing
	// entries keep their positions and new ones are appended.
	document := &yaml.Node{Kind: yaml.MappingNode}
	present := map[string]bool{}
	appendEntry := func(module Module) {
		document.Content = append(document.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: module.Name},
			mappingFor(entry{Project: module.Project, CoreVersions: module.CoreVersions}),
		)
		present[module.Name] = true
	}

	for _, name := range existing.Names() {
		module, _ := existing.Find(name)
		appendEntry(module)
	}

	added := []string{}
	for _, module := range modules {
		if present[module.Name] {
			continue
		}
		appendEntry(module)
		added = append(added, module.Name)
	}

	if len(added) == 0 {
		return []string{}, nil
	}

	encoded, err := yaml.Marshal(map[string]*yaml.Node{"modules": document})
	if err != nil {
		return nil, fmt.Errorf("cannot render the registry: %w", err)
	}

	// The exact final bytes, written next to the registry. They are validated
	// from that file — through the real loader, so what is checked is what
	// will be published — and then renamed into place.
	temp, err := filesystem.WriteTemporary(e.registryPath, encoded, filesystem.KeepMode)
	if err != nil {
		return nil, err
	}

	// Anything that stops the temporary file becoming the registry — a
	// rejected entry, a failed rename, an error from the parser — must also
	// stop it being left next to the registry as debris.
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temp)
		}
	}()

	if _, err := RegistryFromFile(temp); err != nil {
		// Report the registry the user asked to change, not the temporary
		// file.
		return nil, fmt.Errorf("%s", strings.ReplaceAll(err.Error(), temp, e.registryPath))
	}

	if err := filesystem.Commit(temp, e.registryPath); err != nil {
		return nil, err
	}
	published = true

	return added, nil
}

// mappingFor renders one module's definition as a node, so it can be placed in
// an ordered document.
func mappingFor(value entry) *yaml.Node {
	node := &yaml.Node{}
	// Encoding a struct cannot fail for these field types; the error is
	// returned rather than ignored so a future field change cannot pass one
	// silently.
	if err := node.Encode(value); err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Value: err.Error()}
	}

	return node
}
