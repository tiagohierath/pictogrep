package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

var errInstanceLocked = errors.New("Pictogrep library is already in use")

type instanceLock interface {
	Close() error
}

// canonicalHomeIdentity resolves every existing path component. That makes a
// symlinked spelling of a data home share the same process lock as its real
// path, including before the final Pictogrep directory has been created.
func canonicalHomeIdentity(value string) string {
	absolute, err := filepath.Abs(value)
	if err != nil {
		absolute = filepath.Clean(value)
	}
	absolute = filepath.Clean(absolute)
	current := absolute
	missing := []string{}
	for {
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			absolute = filepath.Clean(resolved)
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	return absolute
}

func instanceLockKey(home string) string {
	digest := sha256.Sum256([]byte(canonicalHomeIdentity(home)))
	return fmt.Sprintf("%x", digest[:16])
}

func sameDataHome(left, right string) bool {
	return canonicalHomeIdentity(left) == canonicalHomeIdentity(right)
}
