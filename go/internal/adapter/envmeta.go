package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// EnvMetaFilename is the dotfile written into every provisioned environment.
const EnvMetaFilename = ".upkeep-env.yml"

// EnvironmentMeta records what an environment was provisioned for (module,
// core major), what it was seeded from (the exact core version of the base
// artifact), which engine add-on version it carries, and when it was last
// used.
//
// LastUsedAt is what `prune --older-than` filters on, so it is stamped on
// every reuse — without that, age falls back to creation time and prune
// deletes environments that are in active use.
//
// It doubles as the provisioning completion marker: it is written as the LAST
// provisioning step, so a missing or unparseable dotfile means a partial
// provision that must be torn down and rebuilt, never reused. Every write of
// it therefore goes through the atomic write path — the path must never stop
// existing, or a re-stamp would look like an interrupted provision and force a
// multi-gigabyte rebuild.
type EnvironmentMeta struct {
	ModuleName      string
	CoreMajor       string
	SeedCoreVersion string
	AddOnVersion    string
	CreatedAt       time.Time
	// LastUsedAt is the zero time when the environment has not been reused
	// since it was made.
	LastUsedAt time.Time
}

type envMetaFile struct {
	Module          string `yaml:"module"`
	CoreMajor       string `yaml:"core_major"`
	SeedCoreVersion string `yaml:"seed_core_version"`
	AddOnVersion    string `yaml:"addon_version"`
	CreatedAt       string `yaml:"created_at"`
	LastUsedAt      string `yaml:"last_used_at,omitempty"`
}

// EnvMetaFromYAML reads the dotfile.
func EnvMetaFromYAML(contents string) (EnvironmentMeta, error) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(contents), &node); err != nil {
		return EnvironmentMeta{}, fmt.Errorf("malformed environment meta YAML: %w", err)
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return EnvironmentMeta{}, fmt.Errorf("environment meta YAML must be a mapping")
	}

	fields := map[string]*yaml.Node{}
	mapping := node.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		fields[mapping.Content[i].Value] = mapping.Content[i+1]
	}

	values := map[string]string{}
	for _, key := range []string{"module", "core_major", "seed_core_version", "addon_version", "created_at"} {
		value, present := fields[key]
		if !present || value.Kind != yaml.ScalarNode || value.Tag == "!!null" {
			return EnvironmentMeta{}, fmt.Errorf("environment meta YAML is missing required scalar key %q", key)
		}
		values[key] = value.Value
	}

	createdAt, err := envTimestamp("created_at", values["created_at"])
	if err != nil {
		return EnvironmentMeta{}, err
	}

	meta := EnvironmentMeta{
		ModuleName:      values["module"],
		CoreMajor:       values["core_major"],
		SeedCoreVersion: values["seed_core_version"],
		AddOnVersion:    values["addon_version"],
		CreatedAt:       createdAt,
	}

	if lastUsed, present := fields["last_used_at"]; present && lastUsed.Tag != "!!null" {
		if lastUsed.Kind != yaml.ScalarNode {
			return EnvironmentMeta{}, fmt.Errorf("environment meta key \"last_used_at\" must be a timestamp")
		}
		meta.LastUsedAt, err = envTimestamp("last_used_at", lastUsed.Value)
		if err != nil {
			return EnvironmentMeta{}, err
		}
	}

	return meta, nil
}

// EnvMetaAttribution is a lenient read of the meta for a disk inventory:
// which module and core an environment was made for, and when it was last
// used. Anything missing or malformed is simply unknown.
//
// Deliberately not EnvMetaFromYAML, which is strict, because the two answer
// different questions. The reuse decision must refuse an environment whose
// identity it cannot fully establish — reusing one it has half-read is how a
// check ends up running against the wrong tree. An inventory is the opposite:
// a partial provision, or one written by an older upkeep before a field
// existed, is still disk usage to report, and it is exactly what prune exists
// to collect. Read strictly, such an environment loses its attribution and its
// age, which makes it invisible to `prune --module` and to `prune
// --older-than` — the two commands that would have cleaned it up.
//
// Found by mutation testing the inventory scanner, which had been handed the
// strict reader.
func EnvMetaAttribution(contents string) (module, coreMajor string, lastUsedAt time.Time) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(contents), &node); err != nil {
		return "", "", time.Time{}
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 ||
		node.Content[0].Kind != yaml.MappingNode {
		return "", "", time.Time{}
	}

	scalars := map[string]string{}
	mapping := node.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if value := mapping.Content[i+1]; value.Kind == yaml.ScalarNode && value.Tag != "!!null" {
			scalars[mapping.Content[i].Value] = value.Value
		}
	}

	// Age prefers the reuse stamp over the creation time: without that, an
	// environment in daily use ages from the day it was built.
	lastUsedAt, err := envTimestamp("last_used_at", scalars["last_used_at"])
	if err != nil {
		lastUsedAt, err = envTimestamp("created_at", scalars["created_at"])
		if err != nil {
			lastUsedAt = time.Time{}
		}
	}

	return scalars["module"], scalars["core_major"], lastUsedAt
}

func envTimestamp(key, raw string) (time.Time, error) {
	for _, layout := range metaTimeLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("environment meta key %q is not a parseable timestamp: %q", key, raw)
}

// metaTimeLayouts are the shapes a timestamp can arrive in. Go needs the
// accepted forms listed, so the ones upkeep itself writes come first.
var metaTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05 -0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ToYAML renders the dotfile.
func (m EnvironmentMeta) ToYAML() (string, error) {
	file := envMetaFile{
		Module:          m.ModuleName,
		CoreMajor:       m.CoreMajor,
		SeedCoreVersion: m.SeedCoreVersion,
		AddOnVersion:    m.AddOnVersion,
		CreatedAt:       m.CreatedAt.Format(time.RFC3339),
	}
	if !m.LastUsedAt.IsZero() {
		file.LastUsedAt = m.LastUsedAt.Format(time.RFC3339)
	}

	encoded, err := yaml.Marshal(file)
	if err != nil {
		return "", fmt.Errorf("cannot render the environment meta: %w", err)
	}

	return string(encoded), nil
}

// WithLastUsedAt is this meta, restamped.
func (m EnvironmentMeta) WithLastUsedAt(at time.Time) EnvironmentMeta {
	m.LastUsedAt = at

	return m
}

// WriteTo writes this meta as the environment's dotfile, atomically.
func (m EnvironmentMeta) WriteTo(projectPath string) error {
	rendered, err := m.ToYAML()
	if err != nil {
		return err
	}

	return filesystem.Write(EnvMetaPath(projectPath), []byte(rendered), filesystem.ModeShared)
}

// EnvMetaPath is where the dotfile lives for one environment.
func EnvMetaPath(projectPath string) string {
	return filepath.Join(strings.TrimRight(projectPath, "/"), EnvMetaFilename)
}

// StampLastUsed records that the environment is in use right now, so
// `prune --older-than` measures time since last use rather than time since
// creation.
//
// A zero at means now.
func StampLastUsed(projectPath string, at time.Time) error {
	path := EnvMetaPath(projectPath)
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read the environment meta at %q: %w", path, err)
	}

	meta, err := EnvMetaFromYAML(string(contents))
	if err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now()
	}

	return meta.WithLastUsedAt(at).WriteTo(projectPath)
}

// StaleReasons is the reuse decision: what this environment was provisioned
// for and from, against what is being requested now.
//
// Any reason means the environment is stale and must be torn down and
// re-provisioned; an empty list means it is safe to reuse.
func (m EnvironmentMeta) StaleReasons(moduleName, coreMajor, seedCoreVersion, addOnVersion string) []string {
	reasons := []string{}

	if m.ModuleName != moduleName {
		reasons = append(reasons, fmt.Sprintf(
			"Environment was provisioned for module %q, requested %q.", m.ModuleName, moduleName,
		))
	}
	if m.CoreMajor != coreMajor {
		reasons = append(reasons, fmt.Sprintf(
			"Environment was provisioned for core %s, requested %s.", m.CoreMajor, coreMajor,
		))
	}
	if m.SeedCoreVersion != seedCoreVersion {
		reasons = append(reasons, fmt.Sprintf(
			"Seed skew: environment was seeded from base artifact core %s, the canonical artifact is now core %s.",
			m.SeedCoreVersion, seedCoreVersion,
		))
	}
	if m.AddOnVersion != addOnVersion {
		reasons = append(reasons, fmt.Sprintf(
			"Engine add-on skew: environment carries add-on %s, the adapter pins %s.",
			m.AddOnVersion, addOnVersion,
		))
	}

	return reasons
}
