package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFolderDashboardPreferencesRenameAndExport(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	picture := filepath.Join(app.libraryDir, "cover.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	if _, err := handler.linkTag("ideas", picture); err != nil {
		t.Fatal(err)
	}

	coverID := stableImageID(picture)
	payload, _ := json.Marshal(map[string]any{
		"kind": "tag", "value": "ideas", "favorite": true, "coverId": coverID,
		"cardSize": "huge", "sort": "name", "order": []string{folderPreferenceKey("tag", "ideas")},
	})
	response := httptest.NewRecorder()
	handler.saveFolderView(response, httptest.NewRequest(http.MethodPost, "/api/app/folders/view", bytes.NewReader(payload)))
	if response.Code != http.StatusOK {
		t.Fatalf("save folder view: status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.appFolders(response, httptest.NewRequest(http.MethodGet, "/api/app/folders", nil))
	var foldersResponse struct {
		Folders []struct {
			Value    string        `json:"value"`
			Favorite bool          `json:"favorite"`
			CoverID  string        `json:"coverId"`
			Images   []imageRecord `json:"images"`
		} `json:"folders"`
		View folderViewPreferences `json:"view"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &foldersResponse); err != nil {
		t.Fatal(err)
	}
	// The library itself is a folder now, so the tag is no longer the only one
	// in the response and cannot be read off the front of the list.
	var tagFolder *struct {
		Value    string        `json:"value"`
		Favorite bool          `json:"favorite"`
		CoverID  string        `json:"coverId"`
		Images   []imageRecord `json:"images"`
	}
	for index := range foldersResponse.Folders {
		if foldersResponse.Folders[index].Value == "ideas" {
			tagFolder = &foldersResponse.Folders[index]
		}
	}
	if tagFolder == nil || !tagFolder.Favorite || tagFolder.CoverID != coverID || tagFolder.Images[0].ID != coverID {
		t.Fatalf("folder preferences missing from response: %#v", foldersResponse)
	}
	if foldersResponse.View.CardSize != "huge" || foldersResponse.View.Sort != "name" {
		t.Fatalf("view preferences not persisted: %#v", foldersResponse.View)
	}

	rename, _ := json.Marshal(map[string]any{"action": "rename", "tag": "ideas", "into": "references"})
	response = httptest.NewRecorder()
	handler.appTags(response, httptest.NewRequest(http.MethodPost, "/api/app/tags", bytes.NewReader(rename)))
	if response.Code != http.StatusOK {
		t.Fatalf("rename folder: status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(app.tagsDir, "references")); err != nil {
		t.Fatalf("renamed folder missing: %v", err)
	}
	preferences := app.loadFolderViewPreferences()
	if !preferences.Favorites[folderPreferenceKey("tag", "references")] || preferences.Covers[folderPreferenceKey("tag", "references")] != coverID {
		t.Fatalf("rename lost folder preferences: %#v", preferences)
	}

	response = httptest.NewRecorder()
	handler.exportFolder(response, httptest.NewRequest(http.MethodGet, "/api/app/folders/export?kind=tag&value=references", nil))
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil || len(archive.File) != 1 || archive.File[0].Name != "cover.png" {
		t.Fatalf("folder export is invalid: files=%#v err=%v", archive, err)
	}
}
