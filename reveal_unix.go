//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Open the system file manager on the folder a picture lives in.
//
// Only the folder is passed to the opener, never the file. Handing a file to
// xdg-open launches whatever application claims that image type, which is a
// surprising thing for a menu entry called "show in file manager" to do; the
// folder always opens a file manager. macOS gets -R, which does select the file
// because Finder is the thing being asked, not a guessed handler.
func revealInFileManager(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	opener, err := exec.LookPath("xdg-open")
	if err != nil {
		return fmt.Errorf("no file manager found on this system")
	}
	return exec.Command(opener, filepath.Dir(path)).Start()
}
