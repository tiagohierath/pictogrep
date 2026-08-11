//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func desktopDataHome() (string, error) {
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		return dataHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

func desktopExec(path string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", "\\$").Replace(path) + `"`
}

func installDesktopShortcut() error {
	dataHome, err := desktopDataHome()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	applicationsDir := filepath.Join(dataHome, "applications")
	iconDir := filepath.Join(dataHome, "icons", "hicolor", "512x512", "apps")
	if err := os.MkdirAll(applicationsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return err
	}
	icon, err := embeddedFiles.ReadFile("assets/pictogrep.png")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(iconDir, "pictogrep.png"), icon, 0o644); err != nil {
		return err
	}
	desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Pictogrep
Comment=Find and storyboard your pictures
Exec=%s
Icon=pictogrep
Terminal=false
Categories=Graphics;Photography;
Keywords=images;search;storyboard;
`, desktopExec(executable))
	if err := os.WriteFile(filepath.Join(applicationsDir, "pictogrep.desktop"), []byte(desktop), 0o644); err != nil {
		return err
	}
	fmt.Println("Added Pictogrep to the applications menu.")
	return nil
}

func uninstallDesktopShortcut() error {
	dataHome, err := desktopDataHome()
	if err != nil {
		return err
	}
	for _, path := range []string{
		filepath.Join(dataHome, "applications", "pictogrep.desktop"),
		filepath.Join(dataHome, "icons", "hicolor", "512x512", "apps", "pictogrep.png"),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
