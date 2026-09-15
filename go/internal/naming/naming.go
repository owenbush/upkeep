// Package naming holds the identifier rules shared by everything that turns a
// module name or a core version into a path segment or a project name.
//
// The engine's own project-naming convention stays in the adapter; only the
// rules with callers outside it live here, so the results cache can validate a
// path component without depending on the engine.
package naming

import "regexp"

var (
	modulePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	corePattern   = regexp.MustCompile(`^\d+$`)
)

// IsModuleName reports whether a string is a Drupal machine name.
//
// The same rule has to hold wherever a module name becomes a path segment or a
// project name — the registry validates with it at load, so the failure never
// surfaces mid-prune.
func IsModuleName(moduleName string) bool { return modulePattern.MatchString(moduleName) }

// IsCoreMajor reports whether a string is a whole core major version number.
func IsCoreMajor(coreMajor string) bool { return corePattern.MatchString(coreMajor) }
