//go:build windows

package main

import "os/exec"

// Explorer's /select, opens the containing folder with the file highlighted,
// which is what "show in file manager" means on Windows.
func revealInFileManager(path string) error {
	return exec.Command("explorer", "/select,"+path).Start()
}
