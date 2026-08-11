package main

import "testing"

func TestSplitCommandDefaultsToWeb(t *testing.T) {
	command, args := splitCommand(nil)
	if command != "web" || len(args) != 0 {
		t.Fatalf("unexpected default command: %q %#v", command, args)
	}
}

func TestSplitCommandPreservesArguments(t *testing.T) {
	command, args := splitCommand([]string{"web", "--no-open", "--port", "9000"})
	if command != "web" || len(args) != 3 || args[0] != "--no-open" {
		t.Fatalf("unexpected command split: %q %#v", command, args)
	}
}
