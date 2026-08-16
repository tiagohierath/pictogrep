package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

// Work running inside an HTTP handler gets a safety net for free: net/http
// recovers a panicking handler, and only that one request dies. Work moved onto
// a background goroutine has no net at all, so one panic ends the process and
// takes the user's session, the library lock, and any running import with it.
// Background work reports its panic as a failed job instead of closing
// Pictogrep, and leaves the stack in the terminal for a bug report.
func guard(report func(error)) {
	value := recover()
	if value == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "pictogrep: recovered from an internal error: %v\n%s\n", value, debug.Stack())
	if report != nil {
		report(fmt.Errorf("Pictogrep hit an unexpected internal error and stopped this job"))
	}
}
