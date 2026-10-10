package drupal

import (
	"regexp"
	"strings"
)

var stabilityPattern = regexp.MustCompile(`(?i)[.-]?(?:(beta|b|RC|alpha|a|patch|pl|p)(?:[.-]?\d+)*)(?:\+.*)?$`)

// Stability of a resolved version, as composer reads it: "" for a release,
// otherwise dev, alpha, beta or RC.
//
// Drupal majors spend months in alpha and beta, which is when compatibility
// work happens, so this decides whether a base artifact set has to be built
// with a pre-release constraint — and what the check toolchain installed on
// top of it is pinned to.
func Stability(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")

	if strings.HasPrefix(v, "dev-") || strings.HasSuffix(v, "-dev") {
		return "dev"
	}

	m := stabilityPattern.FindStringSubmatch(v)
	if m == nil {
		return ""
	}

	switch strings.ToLower(m[1]) {
	case "alpha", "a":
		return "alpha"
	case "beta", "b":
		return "beta"
	case "rc":
		return "RC"
	}

	return ""
}
