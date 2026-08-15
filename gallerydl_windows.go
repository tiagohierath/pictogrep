//go:build windows

package main

import "os/exec"

// Windows releases bundle a single-executable gallery-dl, so the process Go
// started is the one doing the downloading.
func groupProcess(command *exec.Cmd) {}

func killProcessGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
