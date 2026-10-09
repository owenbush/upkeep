package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/owenbush/upkeep/internal/drupal"
)

// The extra arguments a module's own .gitlab-ci.yml adds to a check.
//
// CI reads `_PHPCS_EXTRA`, `_PHPSTAN_EXTRA` and `_PHPUNIT_EXTRA` out of the
// pipeline variables and splices each into its tool's command line. They are
// the documented way a project tunes a check — an extra ignore, an excluded
// sniff, a narrowed path — and a module using one gets a different verdict
// from CI than from a tool that ignores them. Which is the whole complaint
// this answers: a check that disagrees with CI is worse than no check.
//
// **Read from the module's file, and treated as untrusted.** CI interpolates
// these unquoted too, but CI runs a project's own pipeline on its own runner.
// upkeep runs them against *other people's* contributions — a Project Update
// Bot branch, a patch from a stranger — so a value here is attacker-controlled
// in a way CI's is not. It lands inside a `bash -c`, so anything that can end
// one command and start another is refused rather than quoted: the values
// worth honouring are flags, and a flag needs none of it.
//
// Refused rather than stripped, because a check that silently dropped half of
// somebody's configuration would be the same class of mismatch again, only
// quieter.

// CiExtras are the per-check extra arguments, already vetted.
type CiExtras struct {
	PhpCs   string
	PhpStan string
	PhpUnit string
	// Rejected names the variables that were ignored because they carry shell
	// metacharacters, so a run can say so rather than quietly disagreeing.
	Rejected []string
}

// shellMetacharacter is anything that could end the tool's command and begin
// another, or read a file into it. A flag and its value need none of these.
var shellMetacharacter = regexp.MustCompile("[;&|<>`$()\\\\\n\r\"']")

// ciExtraVariables maps the pipeline variable to the field it fills.
var ciExtraVariables = []struct {
	name  string
	field func(*CiExtras) *string
}{
	{"_PHPCS_EXTRA", func(e *CiExtras) *string { return &e.PhpCs }},
	{"_PHPSTAN_EXTRA", func(e *CiExtras) *string { return &e.PhpStan }},
	{"_PHPUNIT_EXTRA", func(e *CiExtras) *string { return &e.PhpUnit }},
}

// ReadCiExtras reads them out of the module's .gitlab-ci.yml.
//
// Anything unreadable yields none. A module with no .gitlab-ci.yml, or one
// whose YAML will not parse, is not a reason to refuse to check it — the same
// stance ModuleRequirements takes, and for the same reason: returning less
// configuration is a worse failure than returning none, but refusing to run
// at all is worse than both.
func ReadCiExtras(moduleDir string) CiExtras {
	contents, err := os.ReadFile(filepath.Join(moduleDir, ".gitlab-ci.yml"))
	if err != nil {
		return CiExtras{}
	}

	var pipeline struct {
		Variables map[string]yaml.Node `yaml:"variables"`
	}
	if err := yaml.Unmarshal(contents, &pipeline); err != nil {
		return CiExtras{}
	}

	extras := CiExtras{}
	for _, variable := range ciExtraVariables {
		node, declared := pipeline.Variables[variable.name]
		if !declared {
			continue
		}

		// A variable can be a bare string or GitLab's {value, description}
		// mapping, which is what the template itself uses.
		value := node.Value
		if node.Kind == yaml.MappingNode {
			var described struct {
				Value string `yaml:"value"`
			}
			if err := node.Decode(&described); err != nil {
				continue
			}
			value = described.Value
		}

		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if shellMetacharacter.MatchString(value) {
			extras.Rejected = append(extras.Rejected, variable.name)

			continue
		}
		*variable.field(&extras) = value
	}

	return extras
}

// RejectedExtrasWarning is what to say about the ones that were ignored.
//
// Said rather than swallowed: the verdict will differ from CI's by exactly
// those arguments, and a mismatch nobody mentioned is the thing this whole
// mechanism exists to stop.
func RejectedExtrasWarning(rejected []string) string {
	return "Ignoring " + strings.Join(rejected, ", ") +
		" from the module's .gitlab-ci.yml: the value contains shell metacharacters. " +
		"These are spliced into a command line, and a contribution is not trusted to do that. " +
		"This check may therefore differ from CI by those arguments."
}

// suffixed appends extra arguments to a command, or returns it unchanged.
func suffixed(command, extra string) string {
	if extra == "" {
		return command
	}

	return command + " " + extra
}

// StandardsPackages are the ones whose version decides what a check finds.
//
// coder ships the Drupal and DrupalPractice rulesets, php_codesniffer runs
// them, and phpstan is the other half of the pair. A major bump in any of
// them changes the verdict on code nobody touched.
var StandardsPackages = []string{
	"drupal/coder", "squizlabs/php_codesniffer", "phpstan/phpstan",
}

// StandardsVersions is what the project's lock file pins the check toolchain
// to, as "name version" pairs in the order above.
//
// Reported because a check that disagrees with CI is the complaint this
// answers, and the commonest honest reason for it is not a misconfiguration
// but a different standard: `--version=12` installs drupal/core-dev:^12, which
// requires coder ^9, which requires PHP_CodeSniffer ^4 — while a module whose
// branch declares ^10.3 || ^11 has its CI running coder 8 on PHPCS 3. Both are
// right about their own core. Without the versions on the report that reads as
// a disagreement rather than as a newer ruleset.
//
// Read from composer.lock rather than asked of composer: the file is on disk
// at the project root, and a container round trip for something a JSON decode
// answers would be paid on every run.
func StandardsVersions(projectPath string) []string {
	contents, err := os.ReadFile(filepath.Join(projectPath, "composer.lock"))
	if err != nil {
		return nil
	}

	var lock struct {
		Packages    []lockedPackage `json:"packages"`
		PackagesDev []lockedPackage `json:"packages-dev"`
	}
	if err := json.Unmarshal(contents, &lock); err != nil {
		return nil
	}

	locked := map[string]string{}
	// Dev after runtime, because the toolchain is a dev requirement and a
	// package appearing in both should report the one actually installed.
	for _, packages := range [][]lockedPackage{lock.Packages, lock.PackagesDev} {
		for _, each := range packages {
			locked[each.Name] = each.Version
		}
	}

	versions := []string{}
	for _, name := range StandardsPackages {
		if version, present := locked[name]; present {
			versions = append(versions, name+" "+version)
		}
	}

	return versions
}

type lockedPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// StandardsPreview is the warning for a check run against a core the module
// does not declare.
//
// Reported from the worst kind of failure this tool can have: it gave advice
// that broke CI. A core-12 run uses coder 9 on PHP_CodeSniffer 4; a branch
// declaring ^10.3 || ^11 has its CI on coder 8 and PHPCS 3, and the two do not
// merely differ in strictness — on a real file they wanted **opposite**
// formatting. phpcbf under coder 9 produced a file coder 8 then rejected, so
// the fix had to be rolled back. The standards cannot both be satisfied.
//
// Which means the findings are a preview, not a defect list: they become real
// when the module's own CI starts running that core, and acting on them before
// then breaks the gate that decides whether the work merges. The run says so,
// and the checks still fail — "blocking is fine", because a maintainer wants
// to know, and because a verdict that went green on a standard it could not
// satisfy would be its own lie.
//
// Empty when the module declares the core, or declares nothing upkeep can
// read. Silence on not-knowing, like every other constraint read here:
// warning about a mismatch that may not exist would train people to ignore it.
func StandardsPreview(moduleName, coreMajor, constraint string) string {
	// Equivalent to letting CompatibilityFrom answer, which reports "not
	// known" for an empty constraint — a mutation that removes this survives,
	// deliberately. It stays because "nothing declared means no warning" is
	// the rule here, and leaning on another package's handling of the empty
	// string is how that rule stops holding without anything saying so.
	if constraint == "" {
		return ""
	}

	declared, known := drupal.CompatibilityFrom(constraint, []string{coreMajor})
	if !known || declared.Declares(coreMajor) {
		return ""
	}

	return fmt.Sprintf(
		"%s declares core_version_requirement %q, which does not include core %s. "+
			"The phpcs and phpstan findings below come from that core's coding standard, "+
			"which the module's own CI does not use — treat them as a preview of the work, "+
			"not as a defect list. Applying them, phpcbf included, can fail the CI that "+
			"gates this branch.",
		moduleName, constraint, coreMajor,
	)
}

// DeclaredCoreConstraint is the module working copy's own core constraint, or
// "" when there is none to read.
//
// From the file on disk rather than the API: this is about the code under
// test, which is whatever the working copy currently holds — including the
// uncommitted edits `check --working-copy` exists for, where no ref would have
// the answer.
func DeclaredCoreConstraint(moduleDir, moduleName string) string {
	contents, err := os.ReadFile(filepath.Join(moduleDir, moduleName+".info.yml"))
	if err != nil {
		return ""
	}

	return drupal.ConstraintIn(string(contents))
}
