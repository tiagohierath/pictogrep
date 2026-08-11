package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func testHTTPServer(t *testing.T) (*application, *httptest.Server) {
	t.Helper()
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler.routes())
	t.Cleanup(httpServer.Close)
	return app, httpServer
}

func responseJSON(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestEmbeddedBrowserAndStoryboardPages(t *testing.T) {
	_, server := testHTTPServer(t)
	for _, path := range []string{"/", "/practice", "/assets/app.js", "/assets/app.css"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 || len(body) < 100 {
			t.Fatalf("%s: status=%d bytes=%d", path, response.StatusCode, len(body))
		}
	}
}

func TestUploadWorksWithoutPythonOrAI(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, picture)
	data, _ := os.ReadFile(picture)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/app/upload?name=My%20Picture.png", bytes.NewReader(data))
	request.Header.Set("Content-Type", "image/png")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	if response.StatusCode != 200 || value["ok"] != true {
		t.Fatalf("upload failed: status=%d %#v", response.StatusCode, value)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 1 {
		t.Fatalf("upload was not added to library: %#v", paths)
	}
	response, err = http.Get(server.URL + "/api/app/images?count=5")
	if err != nil {
		t.Fatal(err)
	}
	value = responseJSON(t, response)
	if value["total"].(float64) != 1 {
		t.Fatalf("uploaded image is not browsable: %#v", value)
	}
}

func TestManualFoldersWorkWithoutAI(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(app.libraryDir, "reference.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	payload := bytes.NewBufferString(`{"action":"add","tag":"Mood Board","imageId":0}`)
	response, err := http.Post(server.URL+"/api/app/tags", "application/json", payload)
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	if response.StatusCode != 200 || value["tag"] != "mood-board" {
		t.Fatalf("tag failed: status=%d %#v", response.StatusCode, value)
	}
	response, err = http.Get(server.URL + "/api/app/images?tag=" + url.QueryEscape("mood-board"))
	if err != nil {
		t.Fatal(err)
	}
	value = responseJSON(t, response)
	if value["total"].(float64) != 1 {
		t.Fatalf("tagged image missing: %#v", value)
	}
}

func TestNativeSemanticIndexRoundTrip(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(app.libraryDir, "reference.png")
	writeTestPNG(t, picture)
	app.addPath(picture)

	response, err := http.Get(server.URL + "/api/app/ai")
	if err != nil {
		t.Fatal(err)
	}
	state := responseJSON(t, response)
	missing := state["missing"].([]any)
	if len(missing) != 1 {
		t.Fatalf("expected one missing embedding: %#v", state)
	}
	item := missing[0].(map[string]any)
	vector := make([]float32, 512)
	vector[0] = 1
	payload, _ := json.Marshal(map[string]any{"items": []any{map[string]any{
		"path": picture, "mtime": int64(item["mtime"].(float64)), "vector": vector,
	}}})
	response, err = http.Post(server.URL+"/api/app/ai/embeddings", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != 200 || value["saved"].(float64) != 1 {
		t.Fatalf("embedding save failed: %#v", value)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if missing := reloaded.missingEmbeddings(); len(missing) != 0 {
		t.Fatalf("saved embedding did not survive restart: %#v", missing)
	}
	payload, _ = json.Marshal(map[string]any{"vector": vector, "limit": 10})
	response, err = http.Post(server.URL+"/api/app/ai/search", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	result := responseJSON(t, response)
	if response.StatusCode != 200 || len(result["images"].([]any)) != 1 {
		t.Fatalf("semantic search failed: %#v", result)
	}
}
