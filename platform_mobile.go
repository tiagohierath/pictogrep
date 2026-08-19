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
	// No board importer in the app, at all: not the panel, not the routes, not
	// the weekly sync.
	//
	// Play's Device and Network Abuse policy is the one that removes apps for
	// downloading from a service in a way that service's terms forbid, and
	// Pinterest's terms forbid automated collection. The risk is not a rejection
	// at submission, which costs a resubmission; it is a takedown months later,
	// after a complaint, when people already have the app, and a developer
	// account that cannot be got back. Against that, the feature is worth
	// nothing here: gallery-dl needs a Python this app does not carry, so the
	// importer could not run on a phone even if it were offered.
	//
	// The desktop keeps it. It is not distributed by a store, and a program a
	// person runs on their own machine to fetch pictures they can already see is
	// the thing every browser does with "save image".
	offersPinterest = false
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
