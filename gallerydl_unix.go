//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// gallery-dl runs behind a launcher that spawns the real downloader as a child,
// so killing just the process Go started leaves that child downloading forever.
// Giving the launcher its own process group lets a cancel take the whole tree.
func groupProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
		_ = command.Process.Kill()
	}
}
