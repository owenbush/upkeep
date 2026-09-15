package baseartifact

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Meta is the meta.yml sidecar of a per-core-version base artifact set.
//
// It records the exact identity the artifact was built against, so ensuring an
// environment and materialising a snapshot can detect skew before reusing it.
type Meta struct {
	CoreVersion string
	CoreMajor   string
	PHPVersion  string
	DBEngine    string
	BuiltAt     time.Time
}

// metaFile is the on-disk shape, and the field order is the order it is
// written in.
type metaFile struct {
	CoreVersion string `yaml:"core_version"`
	CoreMajor   string `yaml:"core_major"`
	PHPVersion  string `yaml:"php_version"`
	DBEngine    string `yaml:"db_engine"`
	BuiltAt     string `yaml:"built_at"`
}

// metaTimeLayouts are the shapes a built_at can arrive in. PHP's
// DateTimeImmutable takes almost anything; Go needs the accepted forms listed,
// so the ones upkeep itself writes come first.
var metaTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05 -0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// MetaFromYAML reads a meta sidecar.
func MetaFromYAML(contents string) (Meta, error) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(contents), &node); err != nil {
		return Meta{}, fmt.Errorf("malformed meta YAML: %w", err)
	}

	// A document node wraps the value; an empty file has no content at all.
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return Meta{}, fmt.Errorf("meta YAML must be a mapping of build metadata keys")
	}

	// Read as scalars first so a missing or structured value is reported by
	// name, which is what somebody looking at a half-written artifact needs.
	fields := map[string]*yaml.Node{}
	mapping := node.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		fields[mapping.Content[i].Value] = mapping.Content[i+1]
	}

	values := map[string]string{}
	for _, key := range []string{"core_version", "core_major", "php_version", "db_engine", "built_at"} {
		value, present := fields[key]
		if !present || value.Kind != yaml.ScalarNode || value.Tag == "!!null" {
			return Meta{}, fmt.Errorf("meta YAML is missing required scalar key %q", key)
		}
		values[key] = value.Value
	}

	builtAt, err := parseMetaTime(values["built_at"])
	if err != nil {
		return Meta{}, fmt.Errorf(
			"meta YAML key \"built_at\" is not a parseable timestamp: %q", values["built_at"],
		)
	}

	return Meta{
		CoreVersion: values["core_version"],
		CoreMajor:   values["core_major"],
		PHPVersion:  values["php_version"],
		DBEngine:    values["db_engine"],
		BuiltAt:     builtAt,
	}, nil
}

func parseMetaTime(raw string) (time.Time, error) {
	for _, layout := range metaTimeLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("no layout matched %q", raw)
}

// ToYAML renders the sidecar.
func (m Meta) ToYAML() (string, error) {
	encoded, err := yaml.Marshal(metaFile{
		CoreVersion: m.CoreVersion,
		CoreMajor:   m.CoreMajor,
		PHPVersion:  m.PHPVersion,
		DBEngine:    m.DBEngine,
		BuiltAt:     m.BuiltAt.Format(time.RFC3339),
	})
	if err != nil {
		return "", fmt.Errorf("cannot render the meta: %w", err)
	}

	return string(encoded), nil
}

// SkewAgainst compares this artifact's recorded build identity against a live
// environment.
//
// PHP is compared at major.minor granularity — patch releases are
// ABI-compatible and not skew — and the database engine identity must match
// exactly. It returns human-readable reasons; empty means no skew.
func (m Meta) SkewAgainst(phpVersion, dbEngine string) []string {
	reasons := []string{}

	recorded := phpMajorMinor(m.PHPVersion)
	live := phpMajorMinor(phpVersion)
	if recorded != live {
		reasons = append(reasons, fmt.Sprintf(
			"PHP version skew: artifact built on %s, environment runs %s.", recorded, live,
		))
	}

	if m.DBEngine != dbEngine {
		reasons = append(reasons, fmt.Sprintf(
			"DB engine skew: artifact built on %s, environment runs %s.", m.DBEngine, dbEngine,
		))
	}

	return reasons
}

func phpMajorMinor(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) > 2 {
		parts = parts[:2]
	}

	return strings.Join(parts, ".")
}

// CoreVersionFromLock extracts the exact resolved drupal/core version from a
// base tree's composer.lock, for recording in the meta.
func CoreVersionFromLock(lockJSON string) (string, error) {
	var lock struct {
		Packages []json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal([]byte(lockJSON), &lock); err != nil {
		return "", fmt.Errorf("composer.lock is not valid JSON: %w", err)
	}

	// A lock file is untrusted input: only a package entry that is an object
	// naming drupal/core with a string version answers the question. Anything
	// else falls through to the failure below rather than being coerced into a
	// version that was never resolved.
	for _, raw := range lock.Packages {
		var pkg struct {
			Name    string `json:"name"`
			Version any    `json:"version"`
		}
		if err := json.Unmarshal(raw, &pkg); err != nil || pkg.Name != "drupal/core" {
			continue
		}
		if version, isString := pkg.Version.(string); isString {
			return version, nil
		}
	}

	return "", fmt.Errorf("composer.lock does not contain a resolved drupal/core package")
}
