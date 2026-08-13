//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

const errorAlreadyExists syscall.Errno = 183

var createMutexW = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")

type windowsInstanceLock struct {
	handle syscall.Handle
}

func acquireInstanceLock(home string) (instanceLock, error) {
	name, err := syscall.UTF16PtrFromString(`Local\Pictogrep-` + instanceLockKey(home))
	if err != nil {
		return nil, err
	}
	handle, _, callErr := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, fmt.Errorf("create Pictogrep library lock: %w", callErr)
	}
	lock := &windowsInstanceLock{handle: syscall.Handle(handle)}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		_ = lock.Close()
		return nil, errInstanceLocked
	}
	return lock, nil
}

func (lock *windowsInstanceLock) Close() error {
	if lock == nil || lock.handle == 0 {
		return nil
	}
	err := syscall.CloseHandle(lock.handle)
	lock.handle = 0
	return err
}
