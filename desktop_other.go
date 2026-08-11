//go:build !linux

package main

import "fmt"

func installDesktopShortcut() error {
	return fmt.Errorf("desktop-menu installation is only available on Linux")
}

func uninstallDesktopShortcut() error {
	return fmt.Errorf("desktop-menu installation is only available on Linux")
}
