package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func updateTestServer(t *testing.T) (*application, *server) {
	t.Helper()
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	return app, handler
}

func replaceableUpdate() updateState {
	return updateState{
		CurrentVersion: version, LatestVersion: "99.0.0", Available: true,
		Action: "replace", AssetURL: "https://example.invalid/pictogrep", AssetSHA256: "abc",
	}
}

// The whole point of the automatic check: nobody pressed anything.
func TestAutomaticCheckInstallsAWaitingUpdate(t *testing.T) {
	_, handler := updateTestServer(t)
	installed := 0
	handler.checkUpdate = func() (updateState, error) { return replaceableUpdate(), nil }
	handler.applyUpdate = func(updateState) error { installed++; return nil }

	if !handler.checkOnce(context.Background()) {
		t.Fatal("a check with an update waiting did nothing")
	}
	if installed != 1 {
		t.Fatalf("the update was installed %d times", installed)
	}
	status := handler.updates.snapshot()
	if status["installedVersion"] != "99.0.0" || status["restartRequired"] != true {
		t.Fatalf("the installed version was not recorded: %#v", status)
	}
	// Installed is installed. Checking again must not download it twice, and
	// must not keep telling the window an update is waiting to be fetched.
	if handler.checkOnce(context.Background()) || installed != 1 {
		t.Fatalf("an already installed update was installed again: %d", installed)
	}
	if status["available"] != false {
		t.Fatalf("an installed update still reads as available: %#v", status)
	}
}

func TestAutomaticCheckStopsWhenTurnedOff(t *testing.T) {
	app, handler := updateTestServer(t)
	if err := app.saveUpdateSettings(updateSettings{AutoUpdate: false}); err != nil {
		t.Fatal(err)
	}
	checked := false
	handler.checkUpdate = func() (updateState, error) { checked = true; return replaceableUpdate(), nil }
	handler.applyUpdate = func(updateState) error { t.Fatal("an update was installed with the switch off"); return nil }
	if handler.checkOnce(context.Background()) || checked {
		t.Fatal("the switch was off and it checked anyway")
	}
}

// A Nix or system package is somebody else's to update. Pictogrep says a new
// version exists and stops there.
func TestAutomaticCheckNeverTouchesAManagedInstallation(t *testing.T) {
	_, handler := updateTestServer(t)
	handler.checkUpdate = func() (updateState, error) {
		state := replaceableUpdate()
		state.Action, state.UpdateMethod = "managed", "Nix"
		return state, nil
	}
	handler.applyUpdate = func(updateState) error {
		t.Fatal("Pictogrep wrote over a package manager's copy")
		return nil
	}
	if !handler.checkOnce(context.Background()) {
		t.Fatal("a managed installation was not even checked")
	}
	status := handler.updates.snapshot()
	if status["available"] != true || status["latestVersion"] != "99.0.0" {
		t.Fatalf("a managed installation was not told a new version exists: %#v", status)
	}
	if _, waiting := status["installedVersion"]; waiting {
		t.Fatalf("a managed installation reported an installed update: %#v", status)
	}
}

// A failed check is worth reporting and worth retrying. It must not look like
// an update is ready, and it must not stop the next check from running.
func TestFailedCheckIsRecordedAndRetried(t *testing.T) {
	_, handler := updateTestServer(t)
	attempts := 0
	handler.checkUpdate = func() (updateState, error) {
		attempts++
		if attempts == 1 {
			return updateState{}, http.ErrServerClosed
		}
		return replaceableUpdate(), nil
	}
	installed := 0
	handler.applyUpdate = func(updateState) error { installed++; return nil }

	handler.checkOnce(context.Background())
	if status := handler.updates.snapshot(); status["error"] == nil {
		t.Fatalf("a failed check was not recorded: %#v", status)
	}
	handler.checkOnce(context.Background())
	if installed != 1 {
		t.Fatal("a failed check stopped the next one from installing")
	}
	if status := handler.updates.snapshot(); status["error"] != nil {
		t.Fatalf("a recovered check still reports the old failure: %#v", status)
	}
}

// A download that fails halfway is not an installed update.
func TestFailedInstallDoesNotClaimSuccess(t *testing.T) {
	_, handler := updateTestServer(t)
	handler.checkUpdate = func() (updateState, error) { return replaceableUpdate(), nil }
	handler.applyUpdate = func(updateState) error { return os.ErrPermission }
	handler.checkOnce(context.Background())
	status := handler.updates.snapshot()
	if _, waiting := status["installedVersion"]; waiting {
		t.Fatalf("a failed install reported a version waiting: %#v", status)
	}
	if status["error"] == nil {
		t.Fatalf("a failed install was not reported: %#v", status)
	}
}

func TestAutoUpdateSwitchIsSavedAndServed(t *testing.T) {
	app, handler := updateTestServer(t)
	if !app.updateSettings().AutoUpdate {
		t.Fatal("updating should be on until it is turned off")
	}
	routes := handler.routes()
	body, err := json.Marshal(map[string]any{"autoUpdate": false})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/app/settings/update", bytes.NewReader(body))
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("saving the switch failed: status=%d body=%s", response.Code, response.Body)
	}
	if app.updateSettings().AutoUpdate {
		t.Fatal("the switch did not stay off")
	}
	// It shares one config file with everything else, so it has to leave the
	// rest of that file alone.
	if err := app.saveWebSettings(webSettings{AutoSync: false}); err != nil {
		t.Fatal(err)
	}
	if app.updateSettings().AutoUpdate {
		t.Fatal("saving another setting brought automatic updates back")
	}
	if payload := handler.updateStatePayload(); payload["autoUpdate"] != false {
		t.Fatalf("app state does not report the switch: %#v", payload)
	}
}

// The marker file install.sh writes was the only way to earn an update, so a
// binary somebody built and dropped in their own bin directory could never be
// updated: no button, and an automatic check that could never act.
func TestABinaryInAWritableDirectoryCanBeReplaced(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("self replacement is a Linux path")
	}
	if !executableIsReplaceable() {
		t.Skip("the test binary itself is not in a writable directory")
	}
	channel := currentInstallationChannel()
	if channel.Action != "replace" {
		t.Fatalf("a writable standalone binary was not replaceable: %#v", channel)
	}
}

func TestPackageManagerCopiesAreNeverReplaced(t *testing.T) {
	for executable, want := range map[string]string{
		"/nix/store/abc123-pictogrep-0.8.3/bin/pictogrep": "Nix",
		"/usr/bin/pictogrep":                              "system package manager",
		"/bin/pictogrep":                                  "system package manager",
	} {
		if manager := packageManagerForExecutable(executable, false); manager != want {
			t.Fatalf("%s reported %q, wanted %q", executable, manager, want)
		}
	}
	if manager := packageManagerForExecutable(filepath.Join(t.TempDir(), "pictogrep"), false); manager != "" {
		t.Fatalf("a standalone binary was mistaken for a %s package", manager)
	}
}

// The refusal is the last line of defence: even handed a "replace" state, an
// installation Pictogrep does not own must not be written over.
func TestInstallRefusesWhatItDoesNotOwn(t *testing.T) {
	state := replaceableUpdate()
	state.Action = "managed"
	if err := installUpdate(state); err == nil {
		t.Fatal("a managed installation was updated in place")
	}
	missingAsset := replaceableUpdate()
	missingAsset.AssetURL = ""
	if err := installUpdate(missingAsset); err == nil {
		t.Fatal("an update with no asset was installed")
	}
	unverifiable := replaceableUpdate()
	unverifiable.AssetSHA256 = ""
	if err := installUpdate(unverifiable); err == nil {
		t.Fatal("an update with no checksum was installed")
	}
}
