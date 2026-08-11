//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopShortcutLifecycle(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	if err := installDesktopShortcut(); err != nil {
		t.Fatal(err)
	}
	desktopPath := filepath.Join(dataHome, "applications", "pictogrep.desktop")
	desktop, err := os.ReadFile(desktopPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(desktop), "Icon=pictogrep") || !strings.Contains(string(desktop), "Terminal=false") {
		t.Fatalf("unexpected desktop entry: %s", desktop)
	}
	iconPath := filepath.Join(dataHome, "icons", "hicolor", "512x512", "apps", "pictogrep.png")
	if info, err := os.Stat(iconPath); err != nil || info.Size() == 0 {
		t.Fatalf("icon was not installed: %v", err)
	}
	if err := uninstallDesktopShortcut(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{desktopPath, iconPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("desktop asset remains after uninstall: %s", path)
		}
	}
}
