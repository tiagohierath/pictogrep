//go:build !android && !pictogrep_android

package main

// Pictogrep on a desktop owns the whole machine account: it can be started
// twice, it outlives the browser tab that opened it, and it updates itself.
// None of those are true inside an Android app, so the handful of behaviours
// that depend on them are gathered here and switched off in platform_mobile.go
// rather than scattered as runtime.GOOS checks.
const (
	// A second launch must not attach to a library another process is writing.
	locksTheLibrary = true
	// A server nobody has touched for an hour has no browser tab left.
	closesWhenIdle = true
	// Self-updating binaries are fine from a tarball, banned from an app store.
	updatesItself = true
	// There is a filesystem to point at, and a Python to run gallery-dl with.
	// The interface offers both.
	runsOnPhone = false
	// The board importer, which is a desktop feature and stays one. See
	// platform_mobile.go for why the app build has none of it.
	offersPinterest = true
)

// A desktop launch has no parent watching it, and often no stdin at all: a
// launcher shortcut hands the process a closed one, which would read as "the
// parent is gone" the instant Pictogrep started. See platform_mobile.go.
func superviseParent(func()) {}
