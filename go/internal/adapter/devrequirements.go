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

	raw, present := manifest.values["require-dev"]
	if !present {
		return []string{}
	}

	var requireDev orderedJSON
	if err := json.Unmarshal(raw, &requireDev); err != nil || !requireDev.isObject {
		return []string{}
	}

	packages := []string{}
	for _, name := range requireDev.keys {
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
