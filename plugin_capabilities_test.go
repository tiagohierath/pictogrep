package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Coverage for the two capabilities added for the paid Find-me plugin:
// images.reveal (an existing server route newly wired into the broker) and
// ui.openExternal (a new, non-HTTP capability handled entirely inside
// web/plugin-host.js). Neither had any test coverage before this file.
//
// What these tests actually verify vs. what stays manual is spelled out on
// each test below; see also plugin_host_test.mjs for a standalone Node check
// of plugin-host.js's own logic, which nothing in this Go package executes.

// TestPluginCapabilityAllowlistIncludesFindMeCapabilities pins the v1
// allowlist in plugins.go: if either capability is ever removed from
// pluginCapabilities, loadPlugins silently drops it from a manifest's
// permissions (see the loop in loadPlugins) and the plugin loses access
// without any visible error. That silent-drop behavior is exactly what
// TestManifestPermissionFilteringKeepsFindMeCapabilities exercises below.
func TestPluginCapabilityAllowlistIncludesFindMeCapabilities(t *testing.T) {
	for _, capability := range []string{"images.reveal", "ui.openExternal"} {
		if !pluginCapabilities[capability] {
			t.Fatalf("pluginCapabilities no longer allows %q", capability)
		}
	}
}

// TestManifestPermissionFilteringKeepsFindMeCapabilities loads a manifest
// declaring images.reveal and ui.openExternal alongside a made-up permission,
// and checks that loadPlugins keeps the two real ones and drops the fake one.
// This is the same filtering path TestInstalledPluginCanLoadInsideSandbox
// already checks for images.list/ui.panel; it just adds the two new
// capabilities to the set under test.
func TestManifestPermissionFilteringKeepsFindMeCapabilities(t *testing.T) {
	app := testApplication(t)
	directory := filepath.Join(app.pluginsDir, "find-me", "ui")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "id": "dev.pictogrep.findme",
  "name": "Find-me",
  "version": "1.0.0",
  "apiVersion": "0",
  "entry": "ui/index.html",
  "permissions": ["images.list", "images.reveal", "ui.openExternal", "filesystem.write"]
}`
	if err := os.WriteFile(filepath.Join(app.pluginsDir, "find-me", "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("find-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.reloadPlugins()
	plugins := app.plugins()
	loaded, found := plugins["dev.pictogrep.findme"]
	if !found {
		t.Fatal("find-me plugin was not loaded")
	}
	if got := strings.Join(loaded.Permissions, ","); got != "images.list,images.reveal,ui.openExternal" {
		t.Fatalf("permission filtering changed: got %q", got)
	}
}

// TestPluginHostRoutesRevealThroughTheRealServerRoute locks in the shape of
// the images.reveal entry in web/plugin-host.js's CAPABILITY_ROUTES: POST to
// /api/app/images/reveal with the id under the imageId field, the same field
// server.go's revealImage handler decodes. If a future edit changes the verb,
// path, or body field, this fails instead of silently breaking every plugin
// that calls pictogrep.images.reveal(id).
func TestPluginHostRoutesRevealThroughTheRealServerRoute(t *testing.T) {
	host, err := embeddedFiles.ReadFile("web/plugin-host.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(host)
	if !strings.Contains(source, `"images.reveal": {`) {
		t.Fatal("images.reveal capability route is missing from plugin-host.js")
	}
	if !strings.Contains(source, `path: () => "/api/app/images/reveal"`) {
		t.Fatal("images.reveal no longer points at /api/app/images/reveal")
	}
	if !strings.Contains(source, `body: (args) => ({imageId: args.id})`) {
		t.Fatal("images.reveal no longer sends {imageId: args.id}, which is what revealImage decodes")
	}
	// permissionFor(method) falls through to the method name itself for any
	// method not prefixed with "storage.kv", exactly like images.tag. That
	// function is shared code, but assert the fallthrough here so a future
	// special case for images.reveal doesn't accidentally bypass the
	// permission check without a test noticing.
	if !strings.Contains(source, `function permissionFor(method) {`) || !strings.Contains(source, `return method;`) {
		t.Fatal("permissionFor no longer falls through to the capability name; images.reveal's permission check may have changed")
	}
}

// TestPluginHostGatesOpenExternalOnPermissionAndScheme locks in the two
// safety checks openExternal() performs before ever calling window.open:
// the manifest must declare ui.openExternal, and the URL must start with
// https://. This can't invoke the real function (it lives in a browser-only
// IIFE with no exports, and touches window/DOM), so it asserts the checks are
// present in the shipped source. See plugin_host_test.mjs for an actual
// executable check of this logic under Node.
func TestPluginHostGatesOpenExternalOnPermissionAndScheme(t *testing.T) {
	host, err := embeddedFiles.ReadFile("web/plugin-host.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(host)
	if !strings.Contains(source, `if (method === "ui.openExternal") return openExternal(id, permissions, args);`) {
		t.Fatal("handle() no longer dispatches ui.openExternal before the CAPABILITY_ROUTES lookup")
	}
	if !strings.Contains(source, `if (!permissions.includes("ui.openExternal")) {`) {
		t.Fatal("openExternal no longer checks the manifest permission before opening a tab")
	}
	if !strings.Contains(source, `if (!/^https:\/\//i.test(url)) throw new Error`) {
		t.Fatal("openExternal no longer restricts URLs to https://")
	}
	if !strings.Contains(source, `window.open(url, "_blank", "noopener,noreferrer")`) {
		t.Fatal("openExternal no longer opens with noopener,noreferrer")
	}
}

// TestPluginSDKExposesFindMeCapabilities checks the plugin-facing wrapper in
// web/plugin-sdk.js calls the right broker method names, so a plugin author
// calling pictogrep.images.reveal(id) or pictogrep.ui.openExternal(url)
// actually reaches the capabilities checked above rather than some renamed
// or dropped one.
func TestPluginSDKExposesFindMeCapabilities(t *testing.T) {
	sdk, err := embeddedFiles.ReadFile("web/plugin-sdk.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sdk)
	if !strings.Contains(source, `reveal: (id) => call("images.reveal", { id })`) {
		t.Fatal("pictogrep.images.reveal no longer calls the images.reveal capability")
	}
	if !strings.Contains(source, `openExternal: (url) => call("ui.openExternal", { url })`) {
		t.Fatal("pictogrep.ui.openExternal no longer calls the ui.openExternal capability")
	}
}

// TestRevealImageRejectsUnknownID is the one part of this feature pair that
// is a plain server route rather than broker wiring: revealImage resolves
// the plugin-supplied id through the library index (imagePathByID) rather
// than trusting a path from the request, so an id that was never indexed
// must be rejected before anything reaches the filesystem or an external
// process. This is what actually stops a plugin from asking the host to
// reveal an arbitrary path on disk.
func TestRevealImageRejectsUnknownID(t *testing.T) {
	app := testApplication(t)
	server, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()
	body := strings.NewReader(`{"imageId":"not-a-real-image"}`)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/api/app/images/reveal", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("revealImage accepted an unindexed id: status=%d body=%s", response.Code, response.Body.String())
	}
}

// TestRevealImageResolvesKnownIDToItsIndexedPath checks the success path:
// a real, indexed image id resolves to that image's actual path rather than
// anything the caller supplied directly. Whether the reveal itself succeeds
// depends on whether this machine has a file manager opener (xdg-open) on
// PATH, which is not guaranteed in a CI/sandbox environment, so the test
// accepts either outcome but pins the one thing that must always be true:
// on success, the reported path is the picture's real path, not something
// echoed back from the request.
func TestRevealImageResolvesKnownIDToItsIndexedPath(t *testing.T) {
	app := testApplication(t)
	pictures := t.TempDir()
	picture := filepath.Join(pictures, "reference.png")
	writeTestPNG(t, picture)
	if err := app.indexFolders([]string{pictures}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()
	id := stableImageID(picture)
	body := strings.NewReader(`{"imageId":"` + id + `"}`)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/api/app/images/reveal", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	_, lookErr := exec.LookPath("xdg-open")
	if lookErr != nil {
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected reveal to fail without a file manager opener, got status=%d body=%s", response.Code, response.Body.String())
		}
		return
	}
	if response.Code != http.StatusOK {
		t.Fatalf("revealImage status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Path != picture {
		t.Fatalf("revealImage reported %q, want the indexed path %q", payload.Path, picture)
	}
}
