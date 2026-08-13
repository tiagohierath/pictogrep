package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceLockIsExclusiveAndReleases(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	home := filepath.Join(root, "home")
	first, err := acquireInstanceLock(home)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireInstanceLock(home)
	if second != nil || !errors.Is(err, errInstanceLocked) {
		t.Fatalf("second lock = %#v, %v; want instance locked", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireInstanceLock(home)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunLocksBeforeApplicationStartup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	home := filepath.Join(root, "not-created")
	t.Setenv("PICTOGREP_HOME", home)
	lock, err := acquireInstanceLock(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err := run([]string{"paths"}); !errors.Is(err, errInstanceLocked) {
		t.Fatalf("run returned %v; want instance lock error", err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("application home was mutated before lock acquisition: %v", err)
	}
}
