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

// ModuleRequirements is the package names in the module's own require, which
// are what the site needs for the module to run.
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
	for _, name := range modulePackages(moduleDir, "require") {
		if name != "drupal/core" {
			kept = append(kept, name)
		}
	}

	return kept
}

// modulePackages reads one requirement block out of the module's manifest.
func modulePackages(moduleDir, block string) []string {
	contents, err := os.ReadFile(filepath.Join(moduleDir, "composer.json"))
	if err != nil {
		return []string{}
	}

	// Ordered, so the packages are required in the order the module lists
	// them — a composer failure naming the first one should name the first one
	// the module wrote.
	var manifest orderedJSON
	if err := json.Unmarshal(contents, &manifest); err != nil || !manifest.isObject {
		return []string{}
	}

	raw, present := manifest.values[block]
	if !present {
		return []string{}
	}

	var required orderedJSON
	if err := json.Unmarshal(raw, &required); err != nil || !required.isObject {
		return []string{}
	}

	packages := []string{}
	for _, name := range required.keys {
		// A package name always has a vendor. php, ext-* and lib-* do not, and
		// that is exactly the test the PHP side uses.
		if strings.Contains(name, "/") {
			packages = append(packages, name)
		}
	}

	return packages
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
