package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
)

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

func testServerPort(t *testing.T, rawURL string) int {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestRunningURLOnlyReusesPictogrep(t *testing.T) {
	app, pictogrep := testHTTPServer(t)
	port := testServerPort(t, pictogrep.URL)
	if result := runningURL(port, "app", app.home, version); result != pictogrep.URL+"/" {
		t.Fatalf("did not recognize Pictogrep server: %q", result)
	}
	if result := runningURL(port, "practice", app.home, version); result != pictogrep.URL+"/practice" {
		t.Fatalf("did not recognize storyboard server: %q", result)
	}
	if result := runningURL(port, "app", filepath.Join(t.TempDir(), "other-home"), version); result != "" {
		t.Fatalf("reused a server for another data home: %q", result)
	}
	if result := runningURL(port, "app", app.home, version+"-other"); result != "" {
		t.Fatalf("reused a server running another version: %q", result)
	}

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("another local app"))
	}))
	defer other.Close()
	if result := runningURL(testServerPort(t, other.URL), "app", app.home, version); result != "" {
		t.Fatalf("mistook another local app for Pictogrep: %q", result)
	}
}
