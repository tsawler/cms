//go:build unix

package cms

import (
	"os/exec"
	"syscall"
)

// stopGroup starts cmd in a process group of its own, and makes stopping
// it — on the build's timeout — a SIGTERM to the whole group: the command
// and everything it started. TERM rather than KILL so a wrapper script's
// cleanup trap still gets to run.
func stopGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

// killGroup SIGKILLs whatever is left of cmd's process group: a child
// that ignored the TERM and outlived its parent.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
