package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The dev dependencies a module declares for itself.
//
// Needed the moment upkeep started honouring a module's own phpcs and phpstan
// configuration, because those configurations reference packages the *module*
// requires rather than the site. field_visibility_conditions' ruleset says
//
//	<rule ref="./vendor/phpcompatibility/php-compatibility/PHPCompatibility"/>
//
// and its composer.json puts phpcompatibility/php-compatibility in
// require-dev. In CI that resolves because `composer install` runs in the
// module repository, so the module's dev dependencies are in its own vendor/.
// Under ddev-drupal-contrib the module is a path repository of the site, and
// **composer does not install a path dependency's require-dev** — so the sniff
// was simply absent, and the check failed with "Referenced sniff … does not
// exist" against a ruleset that is perfectly correct.
//
// Using a module's configuration and not installing what that configuration
// needs is half a feature. This is the other half.
//
// Read from the file rather than asked of composer: the working copy is on
// disk already, and shelling out for something a JSON decode answers would add
// a container round trip to every provision.

// ModuleDevRequirements is the package names in the module's require-dev.
//
// Platform requirements are dropped — php, ext-* and lib-* are not packages,
// and asking composer to require them into the site would fail a provision
// over something no install can satisfy.
//
// Anything unreadable yields none. A module with no composer.json, or a
// malformed one, is not a reason to refuse to check it: the checks still run,
// and a configuration referencing something missing reports its own failure
// clearly enough.
func ModuleDevRequirements(moduleDir string) []string {
	return modulePackages(moduleDir, "require-dev")
}

// ModuleRequirements is the module's own require, as composer require
// arguments — `name:constraint`, or the bare name where the module gave no
// constraint — which are what the site needs for the module to run.
//
// **The constraint travels with the name.** Requiring a bare name lets
// composer pick whatever release it likes best, which is the newest *stable*
// one: wrong in two directions. A module asking for drupal/votingapi ^3 got
// either nothing at all (votingapi has never had a stable release, so a bare
// require cannot resolve in a stable project) or would have got a 4.x the
// module never asked for, had one been stable. CI installs the module with its
// own constraints, and a check that resolves a different dependency than CI
// does is answering a different question.
//
// **drupal/core is dropped, and that is the point.** The module is wired in by
// a symlink rather than installed as a package, so its declared core
// constraint is not a requirement the environment has to satisfy — and it must
// not be, because a module declares the cores it *supports* and upkeep exists
// to test the one it does not support yet. Requiring it would refuse to build
// an environment for the compatibility work that environment is for, which is
// exactly what it used to do:
//
//	drupal/jumplinks 1.0.x-dev requires drupal/core ^10.3 || ^11 -> ... the
//	package is fixed to 12.0.0-beta1 (lock file version)
//
// The decision that question deserves is made once, at the orchestrator, by
// MrResolver.assertBranchDeclares — which can be reasoned about and overridden.
// Having composer enforce it a second time, inside provisioning, is a gate
// with no override and no diagnosis.
func ModuleRequirements(moduleDir string) []string {
	kept := []string{}
	for _, requirement := range moduleRequirements(moduleDir, "require") {
		if requirement.name == "drupal/core" {
			continue
		}
		if requirement.constraint == "" {
			kept = append(kept, requirement.name)
		} else {
			kept = append(kept, requirement.name+":"+requirement.constraint)
		}
	}

	return kept
}

// modulePackages reads the package names out of one requirement block of the
// module's manifest.
func modulePackages(moduleDir, block string) []string {
	packages := []string{}
	for _, requirement := range moduleRequirements(moduleDir, block) {
		packages = append(packages, requirement.name)
	}

	return packages
}

// requirement is one package entry of a composer requirement block. The
// constraint is empty when the manifest gave something that is not a string.
type requirement struct {
	name       string
	constraint string
}

// moduleRequirements reads one requirement block out of the module's manifest.
func moduleRequirements(moduleDir, block string) []requirement {
	contents, err := os.ReadFile(filepath.Join(moduleDir, "composer.json"))
	if err != nil {
		return []requirement{}
	}

	// Ordered, so the packages are required in the order the module lists
	// them — a composer failure naming the first one should name the first one
	// the module wrote.
	var manifest orderedJSON
	if err := json.Unmarshal(contents, &manifest); err != nil || !manifest.isObject {
		return []requirement{}
	}

	raw, present := manifest.values[block]
	if !present {
		return []requirement{}
	}

	var required orderedJSON
	if err := json.Unmarshal(raw, &required); err != nil || !required.isObject {
		return []requirement{}
	}

	requirements := []requirement{}
	for _, name := range required.keys {
		// A package name always has a vendor. php, ext-* and lib-* do not, and
		// that is exactly the test the PHP side uses.
		if !strings.Contains(name, "/") {
			continue
		}
		// A constraint that is not a string is not one composer would accept
		// either; the name alone is still worth requiring.
		var constraint string
		if err := json.Unmarshal(required.values[name], &constraint); err != nil {
			constraint = ""
		}
		requirements = append(requirements, requirement{name: name, constraint: strings.TrimSpace(constraint)})
	}

	return requirements
}

// DevRequirementsUnavailable is what to say when they cannot be installed.
//
// A warning rather than a refusal, and the reason is precise: without them the
// module's own configuration may reference a missing sniff, and phpcs says so
// itself, loudly, naming the file. Failing the whole provision instead would
// take down phpunit, the install check and the smoke test over a linting
// dependency — the checks that were going to pass.
func DevRequirementsUnavailable(packages []string) string {
	return fmt.Sprintf(
		"Could not install the module's own dev dependencies (%s). Its phpcs or phpstan configuration may "+
			"reference a sniff or extension that is now missing; the check will name it if so.",
		strings.Join(packages, ", "),
	)
}
