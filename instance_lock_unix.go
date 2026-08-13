//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type unixInstanceLock struct {
	file *os.File
}

func acquireInstanceLock(home string) (instanceLock, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("find Pictogrep lock directory: %w", err)
	}
	directory := filepath.Join(cache, "pictogrep", "locks")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create Pictogrep lock directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, instanceLockKey(home)+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Pictogrep library lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errInstanceLocked
		}
		return nil, fmt.Errorf("lock Pictogrep library: %w", err)
	}
	return &unixInstanceLock{file: file}, nil
}

func (lock *unixInstanceLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
