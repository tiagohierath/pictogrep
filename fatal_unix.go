//go:build !windows

package main

import (
	"fmt"
	"os"
)

func reportFatal(err error) {
	fmt.Fprintln(os.Stderr, "pictogrep:", err)
}
