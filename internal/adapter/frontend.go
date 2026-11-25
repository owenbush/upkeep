package adapter

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/owenbush/upkeep/internal/check"
)

// The front-end jobs drupal.org CI runs — eslint, stylelint and cspell — run
// here the way gitlab_templates runs them (#74): with core's own node
// modules, core's configs, and the module's own where it ships them.
//
// **Against a scratch copy of the module, never the working copy.** CI's jobs
// write into the project folder as they go — they append *.min.js to
// .eslintignore, link core's .prettierrc.json, write a .prettierignore, and
// rewrite .cspell.json — which is harmless on a throwaway CI checkout and
// would leave untracked or modified files in a working copy upkeep later
// commits and pushes from. So each job copies the module to
// <project>/.upkeep-lint/<module> and runs CI's steps verbatim there. The copy
// sits outside the docroot so Drupal never discovers a second copy of the
// module, and it takes the working copy as it is, uncommitted edits included,
// which is what --working-copy is for.
//
// **No variable assignments in these scripts.** `ddev exec` hands its command
// to an outer shell that expands $VARS before the script runs — measured: `X=1
// && echo $X` fails with "X: unbound variable" — so only the container's own
// environment ($DDEV_COMPOSER_ROOT, $DDEV_DOCROOT) is usable. Globs, on the
// other hand, expand in the inner shell after its `cd`, which is what makes
// stylelint's unquoted ./**/*.css mean what it means in CI: without globstar,
// CSS exactly one directory down.

// FrontendChecks run on core's node modules rather than the PHP toolchain.
var FrontendChecks = []check.Type{check.EsLint, check.StyleLint, check.CSpell}

const (
	lintRoot = `"$DDEV_COMPOSER_ROOT"/.upkeep-lint`
	coreDir  = `"$DDEV_COMPOSER_ROOT/$DDEV_DOCROOT"/core`

	// scriptsBase is where gitlab_templates keeps the helper scripts its jobs
	// fetch, beside the assets templatesBase points at.
	scriptsBase = "https://git.drupalcode.org/project/gitlab_templates/-/raw/default-ref/scripts"
)

// nodeBin is one of core's node_modules binaries, in-container.
func nodeBin(tool string) string {
	return coreDir + "/node_modules/.bin/" + tool
}

// NodeModulesScript installs core's node modules, as CI's get-node-modules
// step does before any front-end job. COREPACK_ENABLE_DOWNLOAD_PROMPT=0
// because nobody is there to answer corepack's prompt for yarn.
func NodeModulesScript() string {
	return `cd "$DDEV_DOCROOT"/core && corepack enable && COREPACK_ENABLE_DOWNLOAD_PROMPT=0 yarn install`
}

// lintCopy is the scratch copy every front-end job runs in, made fresh.
func lintCopy(module string) string {
	return lintRoot + "/" + QuoteShellArgument(module)
}

// LintCopyScript makes that copy. `/.` copies the contents, dotfiles
// included, through the symlink the module is reached by.
func LintCopyScript(modulePath, module string) string {
	return strings.Join([]string{
		"rm -rf " + lintRoot,
		"mkdir -p " + lintRoot,
		"cp -a " + modulePath + "/. " + lintCopy(module),
	}, " && ")
}

// EslintScript is gitlab_templates' eslint job.
//
// Core's .eslintrc.passing.json (which pulls in .eslintrc.json and the jQuery
// rules) is linked into the folder *above* the project, so the module's own
// .eslintrc.json still applies on top of it; prettier is configured with
// core's defaults unless the module has its own.
func EslintScript(modulePath, module, extra string) string {
	return strings.Join([]string{
		LintCopyScript(modulePath, module),
		"ln -s " + coreDir + "/.eslintrc.passing.json " + lintRoot + "/.eslintrc.json",
		"ln -s " + coreDir + "/.eslintrc.jquery.json " + lintRoot + "/.eslintrc.jquery.json",
		"cd " + lintCopy(module),
		"touch .eslintignore",
		"{ grep -qF .min.js .eslintignore || echo '*.min.js' >> .eslintignore; }",
		"{ test -e .prettierrc.json || ln -s " + coreDir + "/.prettierrc.json .; }",
		"{ test -e .prettierignore || echo '*.yml' > .prettierignore; }",
		suffixed(nodeBin("eslint")+" --no-error-on-unmatched-pattern --ignore-pattern='*.es6.js'"+
			" --resolve-plugins-relative-to="+coreDir+" --ext=.js,.yml", extra) + " .",
	}, " && ")
}

// StylelintScript is gitlab_templates' stylelint job.
//
// ownConfig is whether the module ships a stylelint config; without one,
// core's .stylelintrc.json. Core's .prettierrc.json is linked in when the
// module has no prettier config, because core's stylelint config loads
// prettier and reports a config error without it — which is the gotcha the
// issue named.
func StylelintScript(modulePath, module string, ownConfig bool, extra string) string {
	config := " --config " + coreDir + "/.stylelintrc.json"
	if ownConfig {
		config = ""
	}

	return strings.Join([]string{
		LintCopyScript(modulePath, module),
		"cd " + lintCopy(module),
		"{ ls -a | grep -qE '^[.]prettierrc|^prettier[.]config[.]' || ln -s " + coreDir + "/.prettierrc.json .; }",
		// Unquoted, so the inner shell expands it exactly as CI's does.
		suffixed(nodeBin("stylelint")+" --ignore-path ./.stylelintignore"+config+
			" ./**/*.css --formatter verbose", extra),
	}, " && ")
}

// StylelintConfigProbe succeeds when the module ships its own stylelint
// configuration, in any of the names stylelint looks for.
func StylelintConfigProbe(modulePath string) string {
	return "ls -a " + modulePath + " | grep -qE '^[.]stylelintrc|^stylelint[.]config[.]'"
}

// CspellScript is gitlab_templates' cspell job: the module's .cspell.json or
// the template default, prepared by the template's own prepare-cspell.php,
// then cspell over the project.
//
// prepare-cspell.php is driven by environment variables (CspellEnvironment);
// _WEB_ROOT points back to the site's docroot from the copy, because the
// dictionaries it adds are core's, at <web root>/core/misc/cspell, and paths in
// .cspell.json resolve against the file. The script itself is fetched beside
// the copy, not into it, so cspell does not read it.
func CspellScript(modulePath, module string, environment []string, extra string) string {
	prepare := lintRoot + "/prepare-cspell.php"

	return strings.Join([]string{
		LintCopyScript(modulePath, module),
		"cd " + lintCopy(module),
		"{ test -f .cspell.json || curl -sSfL " + templatesBase + "/.cspell.json -o .cspell.json; }",
		"curl -sSfL " + scriptsBase + "/prepare-cspell.php -o " + prepare,
		"env " + strings.Join(environment, " ") + " php " + prepare,
		suffixed(nodeBin("cspell")+" -c .cspell.json --show-suggestions --show-context --no-progress", extra) + " .",
	}, " && ")
}

// FrontendSettings is what a module's .gitlab-ci.yml says about the front-end
// jobs, over gitlab_templates' defaults.
type FrontendSettings struct {
	// Skipped is the jobs the module turns off with SKIP_ESLINT and friends.
	Skipped map[check.Type]bool
	// Extra is each job's _*_EXTRA, vetted like the PHP ones.
	Extra map[check.Type]string
	// CspellEnvironment is the _CSPELL_* variables prepare-cspell.php reads,
	// as quoted NAME=value assignments.
	CspellEnvironment []string
	// Rejected names variables ignored for carrying shell metacharacters.
	Rejected []string
}

// frontendVariables are the template's defaults, in the order they are read.
// _ESLINT_EXTRA defaults to --quiet, matching core's own eslint job, and a
// module setting it empty gets warnings — so declared-but-empty is honoured,
// unlike the PHP extras, where empty and absent mean the same thing.
var frontendVariables = []struct{ name, fallback string }{
	{"SKIP_ESLINT", "0"},
	{"SKIP_STYLELINT", "0"},
	{"SKIP_CSPELL", "0"},
	{"_ESLINT_EXTRA", "--quiet"},
	{"_STYLELINT_EXTRA", ""},
	{"_CSPELL_EXTRA", ""},
	{"_CSPELL_WORDS", ""},
	{"_CSPELL_DICTIONARY", ".cspell-project-words.txt"},
	{"_CSPELL_FLAGWORDS", ""},
	{"_CSPELL_MINWORDLENGTH", "4"},
	{"_CSPELL_IGNORE_PATHS", ""},
	{"_CSPELL_IGNORE_STANDARD_FILES", "1"},
}

// ReadFrontendSettings reads them out of the module's .gitlab-ci.yml.
// Unreadable means the defaults, for the reason ReadCiExtras gives.
//
// The extras are spliced into a command line and are refused on a shell
// metacharacter, as the PHP ones are. The _CSPELL_* values are passed quoted,
// as data, so a word list with quotes in it — which prepare-cspell.php
// strips — is not refused.
func ReadFrontendSettings(moduleDir, module string) FrontendSettings {
	declared := map[string]string{}
	if contents, err := os.ReadFile(filepath.Join(moduleDir, ".gitlab-ci.yml")); err == nil {
		var pipeline struct {
			Variables map[string]yaml.Node `yaml:"variables"`
		}
		if yaml.Unmarshal(contents, &pipeline) == nil {
			for name, node := range pipeline.Variables {
				if value, ok := variableValue(node); ok {
					declared[name] = strings.TrimSpace(value)
				}
			}
		}
	}

	settings := FrontendSettings{Skipped: map[check.Type]bool{}, Extra: map[check.Type]string{}}
	value := func(name, fallback string) string {
		if declaredValue, present := declared[name]; present {
			return declaredValue
		}

		return fallback
	}

	for _, variable := range frontendVariables {
		current := value(variable.name, variable.fallback)
		switch {
		case strings.HasPrefix(variable.name, "SKIP_"):
			settings.Skipped[skippedCheck(variable.name)] = current == "1"
		case strings.HasSuffix(variable.name, "_EXTRA"):
			if shellMetacharacter.MatchString(current) {
				settings.Rejected = append(settings.Rejected, variable.name)

				continue
			}
			settings.Extra[extraCheck(variable.name)] = current
		default:
			settings.CspellEnvironment = append(settings.CspellEnvironment,
				variable.name+"="+QuoteShellArgument(current))
		}
	}

	settings.CspellEnvironment = append(settings.CspellEnvironment,
		"CI_PROJECT_NAME="+QuoteShellArgument(module),
		`_WEB_ROOT=../../"$DDEV_DOCROOT"`,
	)

	return settings
}

// variableValue is a pipeline variable's value: a bare scalar or GitLab's
// {value, description} mapping.
func variableValue(node yaml.Node) (string, bool) {
	if node.Kind == yaml.MappingNode {
		var described struct {
			Value string `yaml:"value"`
		}
		if node.Decode(&described) != nil {
			return "", false
		}

		return described.Value, true
	}

	return node.Value, node.Kind == yaml.ScalarNode
}

func skippedCheck(name string) check.Type {
	return check.Type(strings.ToLower(strings.TrimPrefix(name, "SKIP_")))
}

func extraCheck(name string) check.Type {
	return check.Type(strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(name, "_"), "_EXTRA")))
}

// HasFilesFor is whether the module has the files a front-end job's CI rule
// requires before the job runs at all: **/*.js or **/*.yml for eslint,
// **/*.css for stylelint. cspell has no such rule.
//
// Walked on the host, skipping .git, so deciding costs no container round
// trip.
func HasFilesFor(moduleDir string, one check.Type) bool {
	extensions := map[check.Type][]string{
		check.EsLint:    {".js", ".yml"},
		check.StyleLint: {".css"},
	}[one]
	if extensions == nil {
		return true
	}

	found := false
	_ = filepath.WalkDir(moduleDir, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case entry.IsDir() && entry.Name() == ".git":
			return filepath.SkipDir
		case !entry.IsDir() && hasAnySuffix(entry.Name(), extensions):
			found = true

			return filepath.SkipAll
		}

		return nil
	})

	return found
}

func hasAnySuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}

// ensureNodeModules installs core's node modules when they are not there,
// returning why it could not, or "" once they are.
//
// Probed on the host, so an environment that has them costs a stat. Not
// installed by the base artifact build, which would put several hundred
// megabytes into every environment whether or not anyone lints in it.
func (d *DdevContrib) ensureNodeModules(environment Environment) string {
	bin := filepath.Join(environment.ProjectPath, "web", "core", "node_modules", ".bin")
	if isRegularFile(filepath.Join(bin, "eslint")) && isRegularFile(filepath.Join(bin, "stylelint")) &&
		isRegularFile(filepath.Join(bin, "cspell")) {
		return ""
	}

	d.log("Installing core's node modules for the front-end checks (yarn install, once per environment) ...")
	process := d.runner.Capture(
		[]string{"ddev", "exec", "bash", "-c", NodeModulesScript()}, environment.ProjectPath, checkTimeout,
	)
	if process.TimedOut || process.ExitCode == nil || *process.ExitCode != 0 {
		return "Core's node modules could not be installed (yarn install in core), " +
			"so this check could not run:\n" + process.Output
	}

	return ""
}

// runFrontendCheck runs one of CI's front-end jobs, or says why CI would not.
func (d *DdevContrib) runFrontendCheck(
	environment Environment, one check.Type, nodeFailure string,
) check.Result {
	workingCopy := moduleWorkingCopy(environment.ProjectPath)
	settings := ReadFrontendSettings(workingCopy, environment.ModuleName)

	// Unavailable, which passes: CI does not run the job, so it cannot fail
	// there either.
	if settings.Skipped[one] {
		return check.NotAvailable(one, "The module's .gitlab-ci.yml sets SKIP_"+
			strings.ToUpper(string(one))+"=1, so drupal.org CI does not run "+string(one)+".")
	}
	if !HasFilesFor(workingCopy, one) {
		return check.Result{
			Type: one, Status: check.NoTests,
			Output: "The module has no files " + string(one) + " checks, so drupal.org CI does not run it.",
		}
	}
	// Failed, never unavailable: the job would run in CI, and a check that
	// could not run here has not passed.
	if nodeFailure != "" {
		return check.Result{Type: one, Status: check.Failed, Output: nodeFailure}
	}

	modulePath := containerModulePath(environment)
	var script string
	switch one {
	case check.EsLint:
		script = EslintScript(modulePath, environment.ModuleName, settings.Extra[one])
	case check.StyleLint:
		_, ownConfig := d.runner.TryRun([]string{
			"ddev", "exec", "bash", "-c", StylelintConfigProbe(modulePath),
		}, environment.ProjectPath, 0)
		script = StylelintScript(modulePath, environment.ModuleName, ownConfig, settings.Extra[one])
	default:
		script = CspellScript(modulePath, environment.ModuleName, settings.CspellEnvironment, settings.Extra[one])
	}

	return d.runCommandCheck(environment, one, []string{"ddev", "exec", "bash", "-c", script})
}
