package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testRemotePNG(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote.png")
	writeTestPNG(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testRemoteImportServer(t *testing.T, app *application, image remoteImage) *httptest.Server {
	t.Helper()
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler.remoteFetcher = func(context.Context, string) (remoteImage, error) { return image, nil }
	server := httptest.NewServer(handler.routes())
	t.Cleanup(server.Close)
	return server
}

func postRemoteImport(t *testing.T, serverURL string, payload map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(serverURL+"/api/app/import-url", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return response, responseJSON(t, response)
}

func TestRemoteImportRejectsPrivateAndUnsafeURLs(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/image.png",
		"http://[::1]/image.png",
		"http://10.0.0.1/image.png",
		"http://100.64.0.1/image.png",
		"http://169.254.169.254/latest/meta-data",
		"http://192.168.1.2/image.png",
		"ftp://images.example/image.png",
		"https://user:password@images.example/image.png",
		"https://images.example:8443/image.png",
	} {
		if _, err := validateRemoteImageURL(raw); err == nil {
			t.Errorf("unsafe remote URL was accepted: %s", raw)
		}
	}
	if _, err := validateRemoteImageURL("https://images.example/photo.png?size=large"); err != nil {
		t.Fatalf("ordinary public image URL was rejected: %v", err)
	}
}

func TestRemoteImportPreservesBytesTagsAndRejectsDuplicate(t *testing.T) {
	app := testApplication(t)
	data := testRemotePNG(t)
	server := testRemoteImportServer(t, app, remoteImage{Data: data, Name: "Browser picture.png"})
	payload := map[string]string{"url": "https://images.example/picture.png", "folder": "Film references"}

	response, imported := postRemoteImport(t, server.URL, payload)
	if response.StatusCode != http.StatusOK || imported["ok"] != true || imported["folder"] != "film-references" {
		t.Fatalf("remote image was not imported: status=%d %#v", response.StatusCode, imported)
	}
	path := imported["path"].(string)
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatalf("remote original bytes changed: err=%v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(app.tagsDir, "film-references", "images.json"))
	if err != nil {
		t.Fatal(err)
	}
	var images []string
	if err := json.Unmarshal(manifest, &images); err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0] != path {
		t.Fatalf("remote image was not linked to active collection: %#v", images)
	}

	response, duplicate := postRemoteImport(t, server.URL, payload)
	if response.StatusCode != http.StatusOK || duplicate["ok"] != true || duplicate["duplicate"] != true || duplicate["linked"] != false {
		t.Fatalf("existing collection duplicate was not reused: status=%d %#v", response.StatusCode, duplicate)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 1 {
		t.Fatalf("duplicate changed the library: %#v", paths)
	}
}

func TestRemoteImportWritesIntoActiveIndexedSourceFolder(t *testing.T) {
	app := testApplication(t)
	root := t.TempDir()
	destination := filepath.Join(root, "references")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := app.indexFolders([]string{root}); err != nil {
		t.Fatal(err)
	}
	server := testRemoteImportServer(t, app, remoteImage{Data: testRemotePNG(t), Name: "source.png"})
	response, imported := postRemoteImport(t, server.URL, map[string]string{
		"url": "https://images.example/source.png", "source": destination,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("source-folder import failed: status=%d %#v", response.StatusCode, imported)
	}
	if path := imported["path"].(string); filepath.Dir(path) != destination {
		t.Fatalf("image was saved outside active source folder: %s", path)
	}
}

func TestRemoteImportRejectsSourceSymlinkEscapeBeforeDownload(t *testing.T) {
	app := testApplication(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := app.indexFolders([]string{root}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	fetched := false
	handler.remoteFetcher = func(context.Context, string) (remoteImage, error) {
		fetched = true
		return remoteImage{Data: testRemotePNG(t), Name: "escape.png"}, nil
	}
	server := httptest.NewServer(handler.routes())
	defer server.Close()
	response, result := postRemoteImport(t, server.URL, map[string]string{
		"url": "https://images.example/escape.png", "source": link,
	})
	if response.StatusCode != http.StatusBadRequest || result["ok"] != false || fetched {
		t.Fatalf("symlink escape was not rejected before download: status=%d fetched=%v %#v", response.StatusCode, fetched, result)
	}
}
