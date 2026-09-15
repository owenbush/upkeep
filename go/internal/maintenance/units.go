// Package maintenance is the disk inventory and the prune surface: what is on
// this machine, what of it is disposable, and what a prune would reclaim.
package maintenance

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// HumanBytes renders a size for the maintenance tables, in binary units —
// what `du` measures.
func HumanBytes(bytes int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)

	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/gib)
	case bytes >= mib:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/mib)
	case bytes >= kib:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/kib)
	default:
		return strconv.FormatInt(bytes, 10) + " B"
	}
}

// durationSyntax is <number><unit>, and nothing else.
var durationSyntax = regexp.MustCompile(`^(\d+)([wdhms])$`)

var unitSeconds = map[string]time.Duration{
	"w": 7 * 24 * time.Hour,
	"d": 24 * time.Hour,
	"h": time.Hour,
	"m": time.Minute,
	"s": time.Second,
}

// ParseDuration reads the --older-than syntax (30d, 12h, 90m, 45s, 2w).
//
// Deliberately strict: a bare number is rejected so a typo like
// --older-than=30 never silently means "30 seconds".
func ParseDuration(input string) (time.Duration, error) {
	match := durationSyntax.FindStringSubmatch(input)
	if match == nil {
		return 0, fmt.Errorf(
			"invalid duration %q. Use <number><unit> with unit one of w, d, h, m, s (e.g. \"30d\", \"12h\")",
			input,
		)
	}

	count, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", input, err)
	}

	return time.Duration(count) * unitSeconds[match[2]], nil
}
