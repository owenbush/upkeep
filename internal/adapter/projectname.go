package adapter

import (
	"fmt"
	"strings"

	"github.com/owenbush/upkeep/internal/naming"
)

// EngineProjectName is the per-(module x core-version) engine project naming
// convention: upkeep-<module>-d<core-major>.
//
// The module's machine-name underscores become hyphens because engine project
// names become DNS labels (<name>.ddev.site).
func EngineProjectName(moduleName, coreMajor string) (string, error) {
	if !naming.IsModuleName(moduleName) {
		return "", fmt.Errorf(
			"module name must be a Drupal machine name ([a-z][a-z0-9_]*), got %q", moduleName,
		)
	}
	if !naming.IsCoreMajor(coreMajor) {
		return "", fmt.Errorf("core version must be a whole major version number, got %q", coreMajor)
	}

	return fmt.Sprintf("upkeep-%s-d%s", strings.ReplaceAll(moduleName, "_", "-"), coreMajor), nil
}
