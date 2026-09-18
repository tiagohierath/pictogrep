package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCompareVersions(t *testing.T) {
	for _, test := range []struct {
		a, b string
		want int
	}{
		{"0.4.2", "0.4.1", 1},
		{"v1.0.0", "1.0", 0},
		{"0.4.0", "0.4.1", -1},
		{"1.2.3-beta", "1.2.3", -1},
		{"1.2.3", "1.2.3-beta", 1},
		{"1.2.3+build.4", "1.2.3", 0},
	} {
		got := compareVersions(test.a, test.b)
		if got != test.want {
			t.Errorf("compareVersions(%q, %q) = %d; want %d", test.a, test.b, got, test.want)
		}
	}
}

func TestPackageManagerForExecutable(t *testing.T) {
	for _, test := range []struct {
		path string
		arch bool
		want string
	}{
		{"/nix/store/abc-pictogrep/bin/pictogrep", false, "Nix"},
		{"/usr/bin/pictogrep", true, "Arch/AUR"},
		{"/usr/bin/pictogrep", false, "system package manager"},
		{"/home/user/.local/bin/pictogrep", true, ""},
	} {
		if got := packageManagerForExecutable(test.path, test.arch); got != test.want {
			t.Errorf("packageManagerForExecutable(%q, %t) = %q; want %q", test.path, test.arch, got, test.want)
		}
	}
}

func TestCheckForUpdateReadsLatestGitHubRelease(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "Pictogrep/") {
			t.Error("missing Pictogrep user agent")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v9.8.7","html_url":"https://example.test/release","assets":[]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	oldAPI, oldClient, oldVersion := releasesAPI, updateHTTPClient, version
	releasesAPI, updateHTTPClient, version = "https://example.test/latest", client, "1.2.3"
	t.Cleanup(func() { releasesAPI, updateHTTPClient, version = oldAPI, oldClient, oldVersion })

	state, err := checkForUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Available || state.CurrentVersion != "1.2.3" || state.LatestVersion != "9.8.7" {
		t.Fatalf("unexpected update state: %#v", state)
	}
}

func TestInstallUpdateRequiresExplicitConfirmation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/app/update", nil)
	response := httptest.NewRecorder()
	(&server{}).installAppUpdate(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"ok":false`) {
		t.Fatalf("unconfirmed update was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
}

// A manual install has to end with the server quitting itself: it is a
// background process behind a browser tab, so closing the tab (the only
// "close the app" a user can actually do) never stops it, and the next launch
// of the just-installed binary would otherwise find the old process still
// sitting on the library lock and the port, and refuse to start.
func TestInstallingAnUpdateShutsTheServerDownAfterward(t *testing.T) {
	handler := &server{
		checkUpdate:       func() (updateState, error) { return updateState{Available: true, LatestVersion: "9.9.9"}, nil },
		applyUpdate:       func(updateState) error { return nil },
		shutdownRequested: make(chan struct{}),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/app/update", nil)
	request.Header.Set("X-Pictogrep-Action", "install-update")
	response := httptest.NewRecorder()
	handler.installAppUpdate(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"updated":true`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	select {
	case <-handler.shutdownRequested:
	case <-time.After(2 * time.Second):
		t.Fatal("a successful manual update never asked the server to shut down")
	}
}

func TestFailedUpdateNeverShutsTheServerDown(t *testing.T) {
	handler := &server{
		checkUpdate:       func() (updateState, error) { return updateState{Available: true, LatestVersion: "9.9.9"}, nil },
		applyUpdate:       func(updateState) error { return errors.New("disk full") },
		shutdownRequested: make(chan struct{}),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/app/update", nil)
	request.Header.Set("X-Pictogrep-Action", "install-update")
	response := httptest.NewRecorder()
	handler.installAppUpdate(response, request)
	if response.Code == http.StatusOK {
		t.Fatalf("a failed install was reported as OK: %s", response.Body.String())
	}
	select {
	case <-handler.shutdownRequested:
		t.Fatal("a failed install asked the server to shut down anyway")
	case <-time.After(600 * time.Millisecond):
	}
}

func TestReleaseAssetSHA256Validation(t *testing.T) {
	digest := sha256.Sum256([]byte("pictogrep release"))
	want := fmt.Sprintf("%x", digest)
	if got, ok := releaseAssetSHA256("sha256:" + want); !ok || got != want {
		t.Fatalf("valid GitHub digest = %q, %t; want %q, true", got, ok, want)
	}
	for _, invalid := range []string{"", want, "sha512:" + want, "sha256:not-hex", "sha256:" + want[:62]} {
		if got, ok := releaseAssetSHA256(invalid); ok {
			t.Errorf("invalid digest %q accepted as %q", invalid, got)
		}
	}
}

func TestVerifyUpdateDigestRejectsChangedBinary(t *testing.T) {
	binary := []byte("authentic Pictogrep binary")
	digest := sha256.Sum256(binary)
	expected := fmt.Sprintf("%x", digest)
	if err := verifyUpdateDigest(binary, expected); err != nil {
		t.Fatalf("matching digest was rejected: %v", err)
	}
	if err := verifyUpdateDigest([]byte("changed binary"), expected); err == nil {
		t.Fatal("changed update binary passed digest verification")
	}
	if err := verifyUpdateDigest(binary, "not-a-digest"); err == nil {
		t.Fatal("invalid expected digest was accepted")
	}
}
