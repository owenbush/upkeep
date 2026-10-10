package cockpit

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// Editor changes an existing registry file: adds entries, changes one's core
// versions, removes one.
//
// Writes go through parse, merge, validate, publish: the merged registry is
// written to a sibling temporary file, re-validated *from that file* with the
// same rules the loader enforces, and only then renamed over the real
// registry. Both halves matter — validation stops a semantically bad entry,
// and the rename stops a torn write, so neither a rejected entry nor a crash
// mid-write can corrupt the tool's single source of truth.
//
// Dumping regenerates the YAML, so hand-written comments in the file do not
// survive an edit — the scaffold's commented example is consumed the first
// time this writes.
//
// Every edit rebuilds the whole document in file order and republishes it.
// That is more work than patching one node, and it is the reason the registry
// a person reads keeps its entry order through an edit it did not touch.
type Editor struct {
	registryPath string
}

// NewEditor opens the registry at path for editing.
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

	if err := e.publish(document); err != nil {
		return nil, err
	}

	return added, nil
}

// SetCoreVersions replaces one registered module's core versions with the ones
// given, in the order given, and leaves the rest of the file alone. It returns
// the module as it now stands.
//
// The module must already be registered. Inventing an entry here would hand
// every survey command a project path nobody chose, and `modules:add` is where
// a new entry comes from — so an unknown name is a refusal rather than an
// insert.
//
// The order is kept exactly as passed, never sorted: core_versions[0] is the
// core a command targets when --version is omitted (SelectCoreVersion), so
// sorting the list would quietly retarget every check of that module. Which
// also means this cannot be an "add" that appends on its own — what goes in
// the list and in what order is the caller's decision, and it is the one the
// maintainer made.
func (e *Editor) SetCoreVersions(name string, versions []string) (Module, error) {
	existing, err := RegistryFromFile(e.registryPath)
	if err != nil {
		return Module{}, err
	}

	updated, registered := existing.Find(name)
	if !registered {
		return Module{}, fmt.Errorf("module %q is not registered in %s", name, e.registryPath)
	}
	updated.CoreVersions = versions

	document := &yaml.Node{Kind: yaml.MappingNode}
	for _, each := range existing.Names() {
		module, _ := existing.Find(each)
		if each == name {
			module = updated
		}
		document.Content = append(document.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: module.Name},
			mappingFor(entry{Project: module.Project, CoreVersions: module.CoreVersions}),
		)
	}

	if err := e.publish(document); err != nil {
		return Module{}, err
	}

	return updated, nil
}

// Remove drops one registered module's entry entirely and returns what it
// held, so the caller can say what was given up.
//
// What was removed is returned rather than discarded, because the core list is
// a judgement somebody made and this file is the only place it lived; the
// command prints it so untracking is not a silent loss.
//
// Emptying the registry is allowed: `modules: {}` is what `init` scaffolds and
// what the loader accepts, so unwatching the last module leaves a cockpit in
// its first-run state rather than a broken one.
func (e *Editor) Remove(name string) (Module, error) {
	existing, err := RegistryFromFile(e.registryPath)
	if err != nil {
		return Module{}, err
	}

	removed, registered := existing.Find(name)
	if !registered {
		return Module{}, fmt.Errorf("module %q is not registered in %s", name, e.registryPath)
	}

	document := &yaml.Node{Kind: yaml.MappingNode}
	for _, each := range existing.Names() {
		if each == name {
			continue
		}
		module, _ := existing.Find(each)
		document.Content = append(document.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: module.Name},
			mappingFor(entry{Project: module.Project, CoreVersions: module.CoreVersions}),
		)
	}

	if err := e.publish(document); err != nil {
		return Module{}, err
	}

	return removed, nil
}

// publish renders the document and makes it the registry.
//
// Parse, merge, validate, publish — the last two steps, and the order of them
// is the whole safety property: the exact final bytes are written beside the
// registry, re-validated *from that file* through the real loader, and only
// then renamed into place. Shared by every edit rather than copied into each,
// because a second copy of this could drift from the first and the registry is
// the tool's single source of truth.
func (e *Editor) publish(document *yaml.Node) error {
	encoded, err := yaml.Marshal(map[string]*yaml.Node{"modules": document})
	if err != nil {
		return fmt.Errorf("cannot render the registry: %w", err)
	}

	temp, err := filesystem.WriteTemporary(e.registryPath, encoded, filesystem.KeepMode)
	if err != nil {
		return err
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
		return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), temp, e.registryPath))
	}

	if err := filesystem.Commit(temp, e.registryPath); err != nil {
		return err
	}
	published = true

	return nil
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
