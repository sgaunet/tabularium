//go:build unix

package archiver

import (
	"os/exec"
	"syscall"
	"time"
)

// waitDelay bounds a child that ignores the kill or holds the pipes open.
//
// Cmd.Wait blocks until the output pipes close, and a grandchild inheriting
// them keeps them open after the direct child is dead. Without this the tool
// hangs on exactly the misbehaving archiver the timeout exists to escape.
const waitDelay = 5 * time.Second

// configureProcessGroup puts the command in its own process group and kills the
// whole group on timeout.
//
// Killing only the direct child leaves any grandchild it backgrounded running —
// which is how a "timed out" archiver goes on writing to the terminal minutes
// later. Signalling the negated PID reaches the group (FR-056).
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
