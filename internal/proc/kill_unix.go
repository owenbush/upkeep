//go:build unix

package proc

import (
	"os/exec"
	"syscall"
	"time"
)

// waitDelay bounds how long a killed child's pipes are waited on before they
// are closed from this side.
const waitDelay = 2 * time.Second

// killWholeGroup makes a timeout actually stop the work.
//
// exec.CommandContext kills the direct child only. Every command upkeep runs
// is a launcher — `ddev start` starts containers, `sh -c` starts what it was
// given — and the grandchildren keep the output pipes open, so cmd.Wait blocks
// until they finish of their own accord. A one-hour timeout on `ddev start`
// would have waited for ddev whatever the timeout said.
//
// The child gets its own process group, the cancel kills the group, and
// WaitDelay stops this side waiting on pipes some survivor still holds.
func killWholeGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}

		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
