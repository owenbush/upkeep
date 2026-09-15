package adapter

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// everyScript is every command line this package builds, in both arms.
func everyScript() map[string]string {
	module := QuoteShellArgument("/var/www/html/web/modules/contrib/pathauto")

	scripts := map[string]string{
		"phpstan, module's own config": PhpstanScript(module, "phpstan.neon"),
		"phpstan, fallback":            PhpstanScript(module, ""),
		"phpcs, module's own ruleset":  PhpcsScript(module, "phpcs.xml.dist"),
		"phpcs, fallback":              PhpcsScript(module, ""),
	}
	for _, name := range PhpstanConfigs {
		scripts["probe "+name] = ConfigProbe(module, name)
	}
	for _, name := range PhpcsConfigs {
		scripts["probe "+name] = ConfigProbe(module, name)
	}

	return scripts
}

// `ddev exec` re-joins its arguments and hands the result to a shell that
// expands the string *before* the inner shell runs it, so a variable of our
// own cannot work: it is expanded while the string is being built, where it is
// unset, and the command dies with "unbound variable".
//
// Only variables already in the container's environment survive, because
// expanding them early produces the same value.
func TestNoScriptDefinesAShellVariableOfItsOwn(t *testing.T) {
	// Anything that looks like a variable reference or an assignment.
	reference := regexp.MustCompile(`\$\{?[A-Za-z_][A-Za-z0-9_]*`)
	assignment := regexp.MustCompile(`(^|[\s;&|(])[A-Za-z_][A-Za-z0-9_]*=`)

	// The ones the container already has. Nothing may add to this list without
	// the comment above being re-read.
	fromTheContainer := map[string]bool{
		"$DDEV_DOCROOT": true, "$DRUPAL_PROJECTS_PATH": true, "$DDEV_APPROOT": true,
	}

	for name, script := range everyScript() {
		for _, found := range reference.FindAllString(script, -1) {
			if !fromTheContainer[found] {
				t.Errorf("%s uses %s, which is not in the container's environment:\n  %s", name, found, script)
			}
		}
		// --basepath=… and --standard=… are flags, not assignments, so the
		// pattern requires the name to start a word.
		for _, found := range assignment.FindAllString(script, -1) {
			t.Errorf("%s assigns %q, which expands before the inner shell runs:\n  %s",
				name, strings.TrimSpace(found), script)
		}
	}
}

// The working directory stays at the project root, where vendor/ is. From
// inside the module every vendor-relative path in a ruleset breaks.
func TestNoScriptChangesDirectory(t *testing.T) {
	cd := regexp.MustCompile(`(^|[\s;&|(])cd\s`)

	for name, script := range everyScript() {
		if cd.MatchString(script) {
			t.Errorf("%s changes directory:\n  %s", name, script)
		}
	}
}

// Every script is one line: ddev exec re-joins its arguments, so a newline
// would not survive to the inner shell as one.
func TestEveryScriptIsOneLine(t *testing.T) {
	for name, script := range everyScript() {
		if strings.ContainsAny(script, "\n\r") {
			t.Errorf("%s spans more than one line:\n  %s", name, script)
		}
	}
}

// && and || bind equally and left to right: ungrouped, a failed download falls
// through to the next step's || and the analysis runs against a config nobody
// fetched.
func TestTheGuardedStepsAreBraced(t *testing.T) {
	fallbacks := map[string]string{
		"phpstan": PhpstanScript(QuoteShellArgument("/m"), ""),
		"phpcs":   PhpcsScript(QuoteShellArgument("/m"), ""),
	}

	for name, script := range fallbacks {
		for _, step := range strings.Split(script, " && ") {
			if !strings.Contains(step, "||") {
				continue
			}
			if !strings.HasPrefix(step, "{ ") || !strings.HasSuffix(step, "; }") {
				t.Errorf("%s has an unbraced guarded step:\n  %s", name, step)
			}
		}
	}
}

// And the whole thing still parses as shell — a brace or a quote out of place
// would only show up in a container otherwise.
func TestEveryScriptParsesAsShell(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}

	for name, script := range everyScript() {
		// -n parses without running anything.
		if out, err := exec.Command(bash, "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse: %v\n  %s\n  %s", name, err, script, out)
		}
	}
}

// A module's own configuration is used, and the gitlab_templates default is
// only the fallback — which is the behaviour CI has and upkeep did not.
func TestAModulesOwnConfigurationIsNamedRatherThanDiscovered(t *testing.T) {
	module := QuoteShellArgument("/var/www/html/web/modules/contrib/pathauto")

	stan := PhpstanScript(module, "phpstan.neon")
	if !strings.Contains(stan, "-c "+module+"/phpstan.neon") {
		t.Errorf("phpstan does not name the module's config:\n  %s", stan)
	}
	if strings.Contains(stan, templatesBase) {
		t.Errorf("phpstan downloaded the default over the module's own:\n  %s", stan)
	}

	cs := PhpcsScript(module, "phpcs.xml.dist")
	if !strings.Contains(cs, "--standard="+module+"/phpcs.xml.dist") {
		t.Errorf("phpcs does not name the module's ruleset:\n  %s", cs)
	}
	if strings.Contains(cs, templatesBase) {
		t.Errorf("phpcs downloaded the default over the module's own:\n  %s", cs)
	}
}

// The fallback config is written at the project root and never into the
// module: that directory is a git checkout whose cleanliness the next patch
// apply refuses on.
func TestTheFallbackConfigIsNeverWrittenIntoTheModule(t *testing.T) {
	module := QuoteShellArgument("/var/www/html/web/modules/contrib/pathauto")

	for name, script := range map[string]string{
		"phpstan": PhpstanScript(module, ""),
		"phpcs":   PhpcsScript(module, ""),
	} {
		// curl -O writes to the working directory, which is the project root.
		if strings.Contains(script, "-o "+module) || strings.Contains(script, "> "+module) {
			t.Errorf("%s writes a config into the module:\n  %s", name, script)
		}
		if !strings.Contains(script, "curl -sSOL") {
			t.Errorf("%s does not fetch the default to the working directory:\n  %s", name, script)
		}
	}
}

// Reported paths are module-relative as CI's are, rather than absolute
// container paths nobody can act on.
func TestPhpcsReportsModuleRelativePaths(t *testing.T) {
	module := QuoteShellArgument("/var/www/html/web/modules/contrib/pathauto")

	for _, script := range []string{PhpcsScript(module, ""), PhpcsScript(module, "phpcs.xml")} {
		if !strings.Contains(script, "--basepath="+module) {
			t.Errorf("no module basepath:\n  %s", script)
		}
	}
}

// One command per candidate, in the tools' own precedence, because the name is
// the answer and has to be passed to the tool afterwards.
func TestTheProbesFollowTheToolsOwnPrecedence(t *testing.T) {
	if PhpstanConfigs[0] != "phpstan.neon" {
		t.Errorf("phpstan precedence starts with %q", PhpstanConfigs[0])
	}
	if PhpcsConfigs[0] != "phpcs.xml" {
		t.Errorf("phpcs precedence starts with %q", PhpcsConfigs[0])
	}

	probe := ConfigProbe(QuoteShellArgument("/m"), "phpcs.xml")
	if !strings.HasPrefix(probe, "test -f ") {
		t.Errorf("the probe is not a bare test: %q", probe)
	}
	// Exit status is all that is read, so nothing is printed to parse.
	if strings.ContainsAny(probe, "|>") {
		t.Errorf("the probe produces output to parse: %q", probe)
	}
}
