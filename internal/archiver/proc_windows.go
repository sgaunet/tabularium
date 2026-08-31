//go:build windows

package archiver

import (
	"os/exec"
	"syscall"
	"time"
)

// waitDelay bounds a child that holds the output pipes open. See the unix file
// for why it is needed.
const waitDelay = 5 * time.Second

// configureProcessGroup gives the command its own process group.
//
// Setpgid does not exist on Windows; CREATE_NEW_PROCESS_GROUP is the
// equivalent, and it is what lets the group be signalled as a unit. This file
// exists so the windows targets in .goreleaser.yaml still compile (FR-056).
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Kill terminates the process; the new process group means a child it
		// spawned does not inherit this console's group and is torn down with
		// the job the OS associates with it.
		return cmd.Process.Kill()
	}
}
