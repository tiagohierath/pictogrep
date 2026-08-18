//go:build android || pictogrep_android

package main

import (
	"io"
	"os"
)

// Inside an Android app the server is not a program the user started, it is a
// part of the app process. The system guarantees a single instance, the app
// decides when the server dies, and Google Play forbids a binary that replaces
// itself. See platform_desktop.go for what these switch off.
const (
	// filesDir belongs to this app alone, and a stale lock file left by a crash
	// would lock the user out of their own library with nothing to kill.
	locksTheLibrary = false
	// Backgrounding the app is not the same as closing it. A server that shut
	// itself down after an hour would leave a dead WebView on resume.
	closesWhenIdle = false
	// Play policy: apps do not download and execute their own new code.
	updatesItself = false
	// The interface asks the server what it is running on, because the two
	// answers it gives here are different screens: there is no folder on a phone
	// the app is allowed to read, and no Python for gallery-dl to run in. The
	// library is this app's own directory, and the share sheet is what fills it.
	runsOnPhone = true
)

// Android kills an app process without warning and without running anything on
// the way out, so a server started as a child process would survive its own app
// and keep the library open. The one thing the system does clean up is the
// pipe: when the app dies, the write end of this process's stdin closes and the
// read below returns. That makes the pipe the app's heartbeat, and costs the
// shell nothing but holding the handle open.
func superviseParent(shutdown func()) {
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		shutdown()
	}()
}
