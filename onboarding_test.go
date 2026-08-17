package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowseListsFoldersAndCountsImages(t *testing.T) {
	_, server := testHTTPServer(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "holiday", "beach"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.jpg", filepath.Join("holiday", "two.png"), filepath.Join("holiday", "beach", "three.webp")} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	response, err := http.Get(server.URL + "/api/app/browse?path=" + url.QueryEscape(root))
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d: %#v", response.StatusCode, value)
	}
	// Nested pictures count, because a folder is picked for everything under it.
	if count, _ := value["images"].(float64); count != 3 {
		t.Fatalf("expected 3 images under the folder, got %v", value["images"])
	}
	folders, _ := value["folders"].([]any)
	if len(folders) != 1 {
		t.Fatalf("expected only the visible subfolder, got %#v", value["folders"])
	}
	if name := folders[0].(map[string]any)["name"]; name != "holiday" {
		t.Fatalf("expected the holiday folder, got %v", name)
	}
}

func TestBrowseRejectsFilesAndMissingPaths(t *testing.T) {
	_, server := testHTTPServer(t)
	file := filepath.Join(t.TempDir(), "picture.jpg")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(t.TempDir(), "nowhere"), "relative/path"} {
		response, err := http.Get(server.URL + "/api/app/browse?path=" + url.QueryEscape(path))
		if err != nil {
			t.Fatal(err)
		}
		value := responseJSON(t, response)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected %q to be refused, got status %d: %#v", path, response.StatusCode, value)
		}
	}
}

func TestBrowseDefaultsToHome(t *testing.T) {
	_, server := testHTTPServer(t)
	response, err := http.Get(server.URL + "/api/app/browse")
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d: %#v", response.StatusCode, value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	if value["path"] != home {
		t.Fatalf("expected the home directory, got %v", value["path"])
	}
}

func TestOnboardingCompletionSurvivesInState(t *testing.T) {
	_, server := testHTTPServer(t)
	response, err := http.Get(server.URL + "/api/app/state")
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	onboarding, _ := value["onboarding"].(map[string]any)
	if onboarding == nil || onboarding["completed"] != false {
		t.Fatalf("a fresh library should report unfinished onboarding, got %#v", value["onboarding"])
	}

	post, err := http.Post(server.URL+"/api/app/onboarding", "application/json", strings.NewReader(`{"completed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if saved := responseJSON(t, post); post.StatusCode != http.StatusOK || saved["completed"] != true {
		t.Fatalf("unexpected save result: status=%d %#v", post.StatusCode, saved)
	}

	again, err := http.Get(server.URL + "/api/app/state")
	if err != nil {
		t.Fatal(err)
	}
	value = responseJSON(t, again)
	onboarding, _ = value["onboarding"].(map[string]any)
	if onboarding == nil || onboarding["completed"] != true {
		t.Fatalf("onboarding should stay finished, got %#v", value["onboarding"])
	}
}

// Saving onboarding writes the same config file every other setting uses, so it
// has to leave the rest of that file alone.
func TestOnboardingSaveKeepsOtherSettings(t *testing.T) {
	app := testApplication(t)
	if err := app.saveBrowserSettings(browserSettings{ThumbnailSize: "large", HomeOrder: "recent"}); err != nil {
		t.Fatal(err)
	}
	if err := app.saveOnboardingSettings(onboardingSettings{Completed: true}); err != nil {
		t.Fatal(err)
	}
	if settings := app.browserSettings(); settings.ThumbnailSize != "large" || settings.HomeOrder != "recent" {
		t.Fatalf("browser settings were lost: %#v", settings)
	}
	if !app.onboardingSettings().Completed {
		t.Fatal("onboarding completion was not saved")
	}
}
