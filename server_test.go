package main

import (
	"bytes"
	"encoding/base64"
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
	for _, path := range []string{"/", "/practice", "/assets/app.js", "/assets/app.css", "/assets/pictogrep.png"} {
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

func TestUploadValidatesDataBeforeChangingLibrary(t *testing.T) {
	app, server := testHTTPServer(t)

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/app/upload?name=fake.webp", bytes.NewReader([]byte{
		'R', 'I', 'F', 'F', 4, 0, 0, 0, 'W', 'E', 'B', 'P',
	}))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusBadRequest || value["ok"] != false {
		t.Fatalf("fake WebP was accepted: status=%d %#v", response.StatusCode, value)
	}

	picture := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, picture)
	data, _ := os.ReadFile(picture)
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/app/upload?name=source.png&folder=!!!", bytes.NewReader(data))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusBadRequest || value["ok"] != false {
		t.Fatalf("invalid folder was accepted: status=%d %#v", response.StatusCode, value)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/app/upload?name=wrong.jpg", bytes.NewReader(data))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusBadRequest || value["ok"] != false {
		t.Fatalf("mismatched image type was accepted: status=%d %#v", response.StatusCode, value)
	}

	paths, _, _ := app.snapshot()
	entries, _ := os.ReadDir(app.libraryDir)
	if len(paths) != 0 || len(entries) != 0 {
		t.Fatalf("rejected uploads changed the library: paths=%#v entries=%d", paths, len(entries))
	}
}

func TestChunkedUploadWorks(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(t.TempDir(), "chunked.png")
	writeTestPNG(t, picture)
	data, _ := os.ReadFile(picture)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/app/upload?name=chunked.png", bytes.NewReader(data))
	request.ContentLength = -1
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusOK || value["ok"] != true {
		t.Fatalf("chunked upload failed: status=%d %#v", response.StatusCode, value)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 1 {
		t.Fatalf("chunked upload missing from library: %#v", paths)
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

func TestBadTagRequestsHaveNoSideEffects(t *testing.T) {
	app, server := testHTTPServer(t)
	for _, payload := range []string{
		`{"action":"unknown","tag":"ghost"}`,
		`{"action":"create","tag":"ghost"} {}`,
	} {
		response, err := http.Post(server.URL+"/api/app/tags", "application/json", bytes.NewBufferString(payload))
		if err != nil {
			t.Fatal(err)
		}
		if value := responseJSON(t, response); response.StatusCode != http.StatusBadRequest || value["ok"] != false {
			t.Fatalf("bad tag request was accepted: status=%d %#v", response.StatusCode, value)
		}
	}
	if _, err := os.Stat(filepath.Join(app.tagsDir, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("bad request created a folder: %v", err)
	}
}

func TestDecodeJSONEnforcesBodyLimit(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"ok":true} padded`))
	var value map[string]any
	if err := decodeJSON(request, &value, 12); err == nil {
		t.Fatal("oversized JSON request was accepted")
	}
}

func TestReferencesAndBoardsRejectInvalidImageData(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(app.libraryDir, "reference.png")
	writeTestPNG(t, picture)
	app.addPath(picture)

	invalidReference, _ := json.Marshal(map[string]any{
		"dataUrl": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image")),
		"name":    "fake.png",
	})
	response, err := http.Post(server.URL+"/api/references", "application/json", bytes.NewReader(invalidReference))
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusBadRequest || value["ok"] != false {
		t.Fatalf("invalid reference was accepted: status=%d %#v", response.StatusCode, value)
	}

	data, _ := os.ReadFile(picture)
	invalidBoard, _ := json.Marshal(map[string]any{
		"dataUrl":    "data:image/png;base64," + base64.StdEncoding.EncodeToString(data),
		"hasDrawing": true,
		"aspect":     "../../escape",
		"index":      1,
		"imageName":  "reference.png",
		"imageId":    0,
	})
	response, err = http.Post(server.URL+"/api/save", "application/json", bytes.NewReader(invalidBoard))
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusBadRequest || value["ok"] != false {
		t.Fatalf("invalid board aspect was accepted: status=%d %#v", response.StatusCode, value)
	}

	references, _ := os.ReadDir(app.referenceDir)
	boards, _ := os.ReadDir(app.boardsDir)
	boardFiles := 0
	for _, entry := range boards {
		if !entry.IsDir() {
			boardFiles++
		}
	}
	if len(references) != 0 || boardFiles != 0 {
		t.Fatalf("rejected image data changed files: references=%d boards=%d", len(references), boardFiles)
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

func TestSemanticQueryCacheRoundTrip(t *testing.T) {
	_, server := testHTTPServer(t)
	response, err := http.Get(server.URL + "/api/app/ai/query?q=red%20car")
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusOK || value["cached"] != false {
		t.Fatalf("unexpected initial query cache: status=%d %#v", response.StatusCode, value)
	}
	vector := make([]float32, semanticVectorSize)
	vector[4] = 1
	payload, _ := json.Marshal(map[string]any{"query": "  Red   Car ", "vector": vector})
	response, err = http.Post(server.URL+"/api/app/ai/query", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusOK || value["cached"] != true || value["query"] != "red car" {
		t.Fatalf("query cache save failed: status=%d %#v", response.StatusCode, value)
	}
	response, err = http.Get(server.URL + "/api/app/ai/query?q=RED%20%20CAR")
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	cached := value["vector"].([]any)
	if response.StatusCode != http.StatusOK || value["cached"] != true || len(cached) != semanticVectorSize || cached[4].(float64) != 1 {
		t.Fatalf("query cache lookup failed: status=%d %#v", response.StatusCode, value)
	}
}
