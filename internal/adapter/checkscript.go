package adapter

import (
	"fmt"
	"strings"
)

// The command lines for the two checks a module can configure for itself.
//
// **No shell variable of our own, and no cd.** Both rules are paid for.
//
// `ddev exec` re-joins its arguments and hands the result to a shell that
// expands the string *before* the inner shell runs it, so an assignment and
// its use in the same command cannot work: `ROOT=$(pwd) && … "$ROOT/x"`
// expands $ROOT while the string is being built, where it is unset, and dies
// with "ROOT: unbound variable". The same thing killed an earlier MODULE=….
// Only variables already in the container's environment — $DDEV_DOCROOT,
// $DRUPAL_PROJECTS_PATH — survive, because expanding them early produces the
// same value. That is why the long-standing scripts worked and both rewrites
// did not.
//
// The cd had to go for a different reason. CI runs these from inside the
// module because in CI the module repo root *is* where composer install put
// vendor/. Under ddev-drupal-contrib the module is a checkout symlinked into a
// site whose vendor/ lives at the project root, so from inside the module
// every vendor-relative path in a ruleset breaks — observed as
// `Referenced sniff "./vendor/drupal/coder/coder_sniffer/Drupal" does not
// exist` against a real module's own phpcs.xml.dist.
//
// So the working directory stays at the project root, where vendor/ is, and
// the module's own configuration is named explicitly instead of discovered.
// That keeps the part that matters: **a module's own configuration is used,
// and the gitlab_templates default is only the fallback** — which is the
// behaviour CI has and upkeep did not.
//
// The fallback config is written at the project root and never into the
// module: that directory is a git checkout whose cleanliness the next patch
// apply or work-branch start refuses on.
const templatesBase = "https://git.drupalcode.org/project/gitlab_templates/-/raw/default-ref/assets"

var (
	// PhpstanConfigs is PHPStan's own precedence, and the order CI tests them
	// in.
	PhpstanConfigs = []string{"phpstan.neon", "phpstan.neon.dist", "phpstan.dist.neon"}
	// PhpcsConfigs is the four names phpcs looks for.
	//
	// CI's own order is `ls {.,}phpcs.xml{.dist,} | head -1`, and `ls` sorts
	// its arguments — so CI effectively prefers .phpcs.xml. Only a module
	// shipping more than one can tell the difference, and phpcs's own
	// documented precedence is the one worth matching.
	PhpcsConfigs = []string{"phpcs.xml", "phpcs.xml.dist", ".phpcs.xml", ".phpcs.xml.dist"}

	// PhpcsTemplateOverrides are where a module may keep its own copy of the
	// gitlab_templates default, in place of the fetched one.
	//
	// CI's get-file-via-curl.sh looks in .gitlab-ci/ and then .gitlab/ before
	// curling anything, so a module keeping its ruleset there gets its own
	// rules from CI — and, until this existed, the generic default from
	// upkeep. Checked only when the module ships no root-level ruleset, which
	// is the same precedence CI has.
	PhpcsTemplateOverrides = []string{
		".gitlab-ci/assets/phpcs.xml.dist", ".gitlab/assets/phpcs.xml.dist",
	}

	// PhpstanTemplateOverrides is the same for PHPStan.
	PhpstanTemplateOverrides = []string{
		".gitlab-ci/assets/phpstan.neon", ".gitlab/assets/phpstan.neon",
	}
)

// ConfigProbe is a single test for "does the module ship this one?".
//
// One command per candidate, run in order, because the *name* is the answer —
// it has to be passed to the tool afterwards, and parsing it out of shell
// output would put a second fragile thing where this one already is. Exit
// status is all that is read.
func ConfigProbe(modulePath, name string) string {
	return fmt.Sprintf("test -f %s/%s", modulePath, name)
}

// PhpstanScript runs PHPStan against the module, with whichever configuration
// applies.
//
// Relative paths inside a neon file resolve against the file's own directory,
// so a module's config keeps meaning what it means from here.
//
// modulePath is the module's in-container path, already quoted. ownConfig is
// the config the module ships, or "" when it ships none. phpVersionID is the
// PHP version to analyse for, as PHP_VERSION_ID (80300), or 0 to leave it to
// PHPStan — see PhpstanTarget.
func PhpstanScript(modulePath, ownConfig, extra string, phpVersionID int) string {
	if ownConfig != "" {
		config := modulePath + "/" + ownConfig
		steps := targeting(config, phpVersionID)

		return strings.Join(append(steps, suffixed(
			fmt.Sprintf("phpstan analyze %s -c %s", modulePath, configFor(config, phpVersionID)),
			extra)), " && ")
	}

	return strings.Join(append([]string{
		// Braced, because && and || bind equally and left to right:
		// ungrouped, a failed download falls through to the next step's || and
		// the analysis runs against a config nobody fetched.
		fmt.Sprintf("{ test -e phpstan.neon || curl -sSOL %s/phpstan.neon; }", templatesBase),
		"sed -i 's/BASELINE_PLACEHOLDER/phpstan-baseline.neon/g' phpstan.neon",
		"{ test -e phpstan-baseline.neon || touch phpstan-baseline.neon; }",
	}, append(targeting("phpstan.neon", phpVersionID), suffixed(
		fmt.Sprintf("phpstan analyze %s -c %s", modulePath, configFor("phpstan.neon", phpVersionID)),
		extra))...), " && ")
}

// The two files PhpstanTarget writes at the project root.
const (
	phpstanPhpVersionFile = "upkeep-php-version.neon"
	phpstanWrapperFile    = "upkeep-phpstan.neon"
)

// PhpstanTarget pins the PHP version PHPStan analyses for, without touching
// the configuration it was given.
//
// CI runs PHPStan on the minimum PHP the target core supports (gitlab_templates'
// _TARGET_PHP defaults to CORE_PHP_MIN), and PHPStan's default target is the
// PHP it runs on. An environment on a newer PHP therefore analyses for a
// different language: phpstan-drupal reports readonly properties on classes
// using DependencySerializationTrait only below 8.4, so liquid_voting's 22
// CI errors were zero on the environment's 8.4 (#74).
//
// Two files, in this order, because a later include overrides an earlier one:
// the pinned version first, then the real configuration — so a module that
// sets phpVersion itself still gets its own value, as it does in CI. Each is
// one line of inline neon, so no newline has to survive `ddev exec`.
func PhpstanTarget(config string, phpVersionID int) []string {
	return []string{
		fmt.Sprintf("echo 'parameters: {phpVersion: %d}' > %s", phpVersionID, phpstanPhpVersionFile),
		fmt.Sprintf("printf 'includes: [%s, %%s]' %s > %s",
			phpstanPhpVersionFile, config, phpstanWrapperFile),
	}
}

func targeting(config string, phpVersionID int) []string {
	if phpVersionID == 0 {
		return nil
	}

	return PhpstanTarget(config, phpVersionID)
}

func configFor(config string, phpVersionID int) string {
	if phpVersionID == 0 {
		return config
	}

	return phpstanWrapperFile
}

// PhpcsScript runs PHPCS against the module, with whichever ruleset applies.
//
// --basepath is the module, so reported paths are module-relative as CI's are
// rather than absolute container paths nobody can act on.
//
// modulePath is the module's in-container path, already quoted. ownConfig is
// the ruleset the module ships, or "" when it ships none.
func PhpcsScript(modulePath, ownConfig, extra string) string {
	report := fmt.Sprintf(
		"-s --report-full --report-summary --report-source --basepath=%s --ignore='*/.ddev/*'",
		modulePath,
	)

	if ownConfig != "" {
		return suffixed(fmt.Sprintf(
			"phpcs %s --standard=%s/%s %s", report, modulePath, ownConfig, modulePath), extra)
	}

	return strings.Join([]string{
		fmt.Sprintf("{ test -e phpcs.xml.dist || curl -sSOL %s/phpcs.xml.dist; }", templatesBase),
		suffixed(fmt.Sprintf("phpcs %s --standard=phpcs.xml.dist %s", report, modulePath), extra),
	}, " && ")
}
