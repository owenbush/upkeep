package gitlab

import (
	"fmt"
	"os"
	"strings"
)

// DefaultEnvVar is the environment variable a token is read from.
const DefaultEnvVar = "UPKEEP_GITLAB_TOKEN"

// TokenResolver resolves the GitLab PAT without ever printing or persisting
// it.
//
// Sources, in order:
//  1. the UPKEEP_GITLAB_TOKEN environment variable (non-empty);
//  2. the config file ~/.config/upkeep/drupal-pat (or
//     $XDG_CONFIG_HOME/upkeep/drupal-pat).
//
// In both cases only the first non-empty line is taken, and a value carrying
// characters illegal in an HTTP header value is refused — otherwise it reaches
// the HTTP client as a PRIVATE-TOKEN header and fails with an error naming
// neither the cause nor the source.
//
// Resolve returns "" when neither source yields a usable token; callers decide
// how to report that, and must not echo any token material while doing so.
//
// Diagnostics — a group/world-readable token file, an unusable value — go to
// an injected warning sink so this stays testable. The warnings never contain
// token material.
type TokenResolver struct {
	envVar     string
	configFile string
	warn       func(string)

	// warnedAboutPermissions keeps it to one permission warning per resolver,
	// however often Resolve is called.
	warnedAboutPermissions bool
}

// NewTokenResolver builds a resolver. An empty envVar or configFile takes the
// default; a nil warn is a no-op sink.
func NewTokenResolver(envVar, configFile string, warn func(string)) *TokenResolver {
	if envVar == "" {
		envVar = DefaultEnvVar
	}
	if configFile == "" {
		configFile = DefaultConfigFile()
	}
	if warn == nil {
		warn = func(string) {}
	}

	return &TokenResolver{envVar: envVar, configFile: configFile, warn: warn}
}

// Resolve returns the token, or "" when no source yields a usable one.
func (r *TokenResolver) Resolve() string {
	if fromEnv := os.Getenv(r.envVar); strings.TrimSpace(fromEnv) != "" {
		return r.usable(firstLine(fromEnv), fmt.Sprintf("env var %s", r.envVar))
	}

	content, err := os.ReadFile(r.configFile)
	if err != nil {
		return ""
	}
	r.warnIfReadableByOthers()
	if value := firstLine(string(content)); value != "" {
		return r.usable(value, fmt.Sprintf("token file %s", r.configFile))
	}

	return ""
}

// DescribeSources is a human-readable description of the sources consulted,
// safe to print: it names sources, never token material.
func (r *TokenResolver) DescribeSources() string {
	return describeSources(r.envVar, r.configFile)
}

// DescribeDefaultSources is the same description for callers that have no
// resolver — the unauthorized failure, raised deep inside the client, is the
// one such caller.
func DescribeDefaultSources() string {
	return describeSources(DefaultEnvVar, DefaultConfigFile())
}

func describeSources(envVar, configFile string) string {
	return fmt.Sprintf("env var %s, config file %s", envVar, configFile)
}

// DefaultConfigFile is where the token is read from when nothing says
// otherwise.
func DefaultConfigFile() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "~"
		}
		configHome = home + "/.config"
	}

	return configHome + "/upkeep/drupal-pat"
}

// firstLine is the first non-empty line, trimmed — so a .netrc-style block, a
// trailing comment, or a stray newline cannot travel into a header value.
func firstLine(content string) string {
	// Every line break PCRE's \R matches, since that is what the PHP side
	// splits on. Not spaces: a space is legal inside a header value, and
	// splitting on one would silently truncate a token rather than refuse it.
	isBreak := func(r rune) bool {
		switch r {
		case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
			return true
		default:
			return false
		}
	}

	for _, line := range strings.FieldsFunc(content, isBreak) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

// usable refuses a value that cannot legally be an HTTP header value. The
// diagnostic names the source, never the value.
func (r *TokenResolver) usable(value, source string) string {
	if value == "" {
		return ""
	}

	for _, c := range []byte(value) {
		if c < 0x20 || c == 0x7F {
			r.warn(fmt.Sprintf(
				"The GitLab token from %s contains characters that cannot appear in an HTTP header "+
					"and was ignored. Store the token alone, on one line.",
				source,
			))

			return ""
		}
	}

	return value
}

func (r *TokenResolver) warnIfReadableByOthers() {
	if r.warnedAboutPermissions {
		return
	}

	info, err := os.Stat(r.configFile)
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return
	}

	r.warnedAboutPermissions = true
	r.warn(fmt.Sprintf(
		"The GitLab token file %s is readable by other accounts on this machine (mode %04o). "+
			"Restrict it with: chmod 600 %s",
		r.configFile,
		info.Mode().Perm(),
		r.configFile,
	))
}
