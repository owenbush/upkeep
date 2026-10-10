package maintenance

import (
	"strconv"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/proc"
)

// diskUsageTimeout bounds one measurement. A base-artifacts tree is gigabytes
// of small files and a cold cache makes that slow; a hang must not take a
// status listing with it.
const diskUsageTimeout = 5 * time.Minute

// DiskSizer measures real on-disk usage the way an operator would check it.
//
// `du -sk`, which is the spelling both BSD and GNU accept, reported in bytes.
// Real usage rather than apparent size: a base tree is mostly small files, so
// the block count and the byte count differ by enough to make a prune's "you
// will get back" figure wrong.
//
// Anything that does not answer measures as zero. A size is a number in a
// table beside something that certainly exists, and refusing to render the
// table because one directory could not be measured would be the wrong trade;
// the scanner's warnings are where "I could not look" is reported.
//
// Takes the runner rather than reaching for a package-level one, so the same
// rule holds here as everywhere else: every child process goes through the
// package that strips the credential and redacts the output, and it is handed
// in at the composition root rather than installed into a global that a test
// has to remember to set.
func DiskSizer(runner proc.Runner) Sizer {
	return func(path string) int64 {
		output, ok := runner.TryRun([]string{"du", "-sk", path}, "", diskUsageTimeout)
		if !ok {
			return 0
		}

		// `du -sk` prints "<blocks>\t<path>", and a path can contain spaces,
		// so only the first field is ever read.
		fields := strings.FieldsFunc(strings.TrimSpace(output), func(r rune) bool {
			return r == '\t' || r == ' ' || r == '\n'
		})
		if len(fields) == 0 {
			return 0
		}

		kilobytes, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || kilobytes < 0 {
			return 0
		}

		return kilobytes * 1024
	}
}
