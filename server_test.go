package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestLocalRequestProtection(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name      string
		method    string
		host      string
		origin    string
		fetchSite string
		status    int
	}{
		{name: "local page", method: http.MethodGet, host: "127.0.0.1:8765", status: http.StatusNoContent},
		{name: "non-local host", method: http.MethodGet, host: "pictogrep.attacker.example", status: http.StatusForbidden},
		{name: "same origin mutation", method: http.MethodPost, host: "127.0.0.1:8765", origin: "http://127.0.0.1:8765", status: http.StatusNoContent},
		{name: "localhost origin", method: http.MethodPost, host: "localhost:8765", origin: "http://localhost:8765", status: http.StatusNoContent},
		{name: "CLI mutation", method: http.MethodPost, host: "127.0.0.1:8765", status: http.StatusNoContent},
		{name: "foreign origin", method: http.MethodPost, host: "127.0.0.1:8765", origin: "https://attacker.example", status: http.StatusForbidden},
		{name: "wrong local port", method: http.MethodPost, host: "127.0.0.1:8765", origin: "http://127.0.0.1:9999", status: http.StatusForbidden},
		{name: "null origin", method: http.MethodDelete, host: "127.0.0.1:8765", origin: "null", status: http.StatusForbidden},
		{name: "cross-site fetch", method: http.MethodPost, host: "127.0.0.1:8765", fetchSite: "cross-site", status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://"+test.host+"/api/app/index", nil)
			request.Host = test.host
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestBrowserBackgroundLogEndpoint(t *testing.T) {
	_, server := testHTTPServer(t)
	response, err := http.Post(server.URL+"/api/app/log", "application/json", strings.NewReader(`{
		"level":"warning","event":"search-index-image","message":"could not load preview","path":"/pictures/example.jpg"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("background log status=%d, want %d", response.StatusCode, http.StatusNoContent)
	}
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

func TestEmbeddedAIRuntimeIsLocalAndPinned(t *testing.T) {
	_, server := testHTTPServer(t)
	response, err := http.Get(server.URL + "/api/app/state")
	if err != nil {
		t.Fatal(err)
	}
	state := responseJSON(t, response)
	model := state["embeddingModel"].(map[string]any)
	if model["key"] != defaultEmbeddingModel.Key || model["backend"] != defaultEmbeddingModel.Backend ||
		model["modelId"] != defaultEmbeddingModel.ModelID || model["revision"] != defaultEmbeddingModel.Revision ||
		model["dimensions"] != float64(defaultEmbeddingModel.Dimensions) {
		t.Fatalf("server published unexpected embedding model: %#v", model)
	}
	searchIndex := state["searchIndex"].(map[string]any)
	if searchIndex["automatic"] != true || searchIndex["indexed"] != float64(0) || searchIndex["total"] != float64(0) {
		t.Fatalf("server published unexpected search index state: %#v", searchIndex)
	}
	plugins := state["plugins"].(map[string]any)
	commandPalette := plugins["commandPalette"].(map[string]any)
	if commandPalette["enabled"] != false {
		t.Fatalf("command palette should be disabled by default: %#v", commandPalette)
	}
	assets := []struct {
		path        string
		contentType string
		minimumSize int64
	}{
		{path: "/assets/transformers.web.min.js", contentType: "text/javascript", minimumSize: 400_000},
		{path: "/assets/ort.wasm.bundle.min.mjs", contentType: "text/javascript", minimumSize: 40_000},
		{path: "/assets/ort-wasm-simd-threaded.mjs", contentType: "text/javascript", minimumSize: 20_000},
		{path: "/assets/ort-wasm-simd-threaded.wasm", contentType: "application/wasm", minimumSize: 11_000_000},
		{path: "/licenses", contentType: "text/plain", minimumSize: 300_000},
	}
	for _, asset := range assets {
		request, _ := http.NewRequest(http.MethodHead, server.URL+asset.path, nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), asset.contentType) || response.ContentLength < asset.minimumSize {
			t.Errorf("%s: status=%d type=%q size=%d", asset.path, response.StatusCode, response.Header.Get("Content-Type"), response.ContentLength)
		}
	}

	response, err = http.Get(server.URL + "/assets/ai-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	source := string(worker)
	if strings.Contains(source, "cdn.jsdelivr.net") || !strings.Contains(source, `from "./transformers.web.min.js"`) || !strings.Contains(source, "embeddingBackends") {
		t.Fatal("AI worker does not use the local runtime and embedding backend boundary")
	}
	runtime, err := embeddedFiles.ReadFile("web/transformers.web.min.js")
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource := string(runtime[:min(len(runtime), 256)])
	if strings.Contains(runtimeSource, `from"onnxruntime-`) || !strings.Contains(runtimeSource, `from"./ort.wasm.bundle.min.mjs"`) {
		t.Fatal("browser AI runtime contains an unresolved package import")
	}
}

func TestBrowserSmoke(t *testing.T) {
	browser := ""
	if configured := os.Getenv("PICTOGREP_BROWSER"); configured != "" {
		resolved, err := exec.LookPath(configured)
		if err != nil {
			t.Fatalf("PICTOGREP_BROWSER is unavailable: %v", err)
		}
		browser = resolved
	} else {
		for _, candidate := range []string{"google-chrome", "chromium", "chromium-browser"} {
			if resolved, err := exec.LookPath(candidate); err == nil {
				browser = resolved
				break
			}
		}
		if browser == "" {
			t.Skip("Chrome or Chromium is not installed")
		}
	}

	_, server := testHTTPServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, browser,
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--user-data-dir="+t.TempDir(),
		"--virtual-time-budget=2500",
		"--dump-dom",
		server.URL+"/",
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("browser smoke test failed: %v", err)
	}
	html := string(output)
	for _, expected := range []string{`id="searchQuery"`, `id="imagesEmpty"`, "No pictures yet.", "navylily.tv/pictogrep"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("rendered app is missing %q", expected)
		}
	}
	if strings.Contains(html, `id="imagesEmpty" hidden`) {
		t.Fatal("browser JavaScript did not finish loading the empty library")
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
	payload, _ := json.Marshal(map[string]any{"action": "add", "tag": "Mood Board", "imageId": stableImageID(picture)})
	response, err := http.Post(server.URL+"/api/app/tags", "application/json", bytes.NewReader(payload))
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

func TestPromptFolderUsesCachedSemanticEmbeddings(t *testing.T) {
	app, server := testHTTPServer(t)
	paths := []string{
		filepath.Join(app.libraryDir, "cat.png"),
		filepath.Join(app.libraryDir, "kitten.png"),
		filepath.Join(app.libraryDir, "car.png"),
	}
	records := map[string]embeddingRecord{}
	for index, path := range paths {
		writeTestPNG(t, path)
		app.addPath(path)
		vector := make([]float32, defaultEmbeddingModel.Dimensions)
		vector[0] = []float32{1, 0.8, 0.1}[index]
		vector[1] = []float32{0, 0.2, 0.9}[index]
		records[path] = embeddingRecord{Mtime: embeddingMtime(path), Vector: vector}
	}
	if err := app.updateEmbeddings(records); err != nil {
		t.Fatal(err)
	}
	query := make([]float32, defaultEmbeddingModel.Dimensions)
	query[0] = 1
	payload, _ := json.Marshal(map[string]any{
		"action": "fill", "model": defaultEmbeddingModel.Key, "tag": "Cats", "prompt": " cats ", "limit": 2, "vector": query,
	})
	response, err := http.Post(server.URL+"/api/app/tags", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	if response.StatusCode != http.StatusOK || value["tag"] != "cats" || value["added"].(float64) != 2 || value["matched"].(float64) != 2 {
		t.Fatalf("prompt folder failed: status=%d %#v", response.StatusCode, value)
	}
	response, err = http.Get(server.URL + "/api/app/images?tag=cats&count=10")
	if err != nil {
		t.Fatal(err)
	}
	value = responseJSON(t, response)
	images := value["images"].([]any)
	if len(images) != 2 || images[0].(map[string]any)["name"] != "cat.png" || images[1].(map[string]any)["name"] != "kitten.png" {
		t.Fatalf("prompt folder used the wrong ranking: %#v", images)
	}
}

func TestFoldersAPIIncludesNestedSourceStructure(t *testing.T) {
	app, server := testHTTPServer(t)
	source := t.TempDir()
	rootImage := filepath.Join(source, "root.png")
	firstImage := filepath.Join(source, "animals", "cat.png")
	deepImage := filepath.Join(source, "animals", "favorites", "sleeping.png")
	if err := os.MkdirAll(filepath.Dir(deepImage), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{rootImage, firstImage, deepImage} {
		writeTestPNG(t, path)
	}
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}

	response, err := http.Get(server.URL + "/api/app/folders")
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	folders := value["folders"].([]any)
	if len(folders) != 3 {
		t.Fatalf("expected source and two nested folders, got %#v", folders)
	}
	for index, expected := range []struct {
		name  string
		value string
		depth float64
		count float64
	}{
		{filepath.Base(source), source, 0, 3},
		{"animals", filepath.Join(source, "animals"), 1, 2},
		{"favorites", filepath.Join(source, "animals", "favorites"), 2, 1},
	} {
		folder := folders[index].(map[string]any)
		if folder["name"] != expected.name || folder["value"] != expected.value || folder["depth"] != expected.depth || folder["count"] != expected.count {
			t.Fatalf("folder %d does not match hierarchy: %#v", index, folder)
		}
	}
}

func TestFolderCanvasPositionsPersistWithoutMovingImages(t *testing.T) {
	app, server := testHTTPServer(t)
	source := t.TempDir()
	paths := []string{filepath.Join(source, "one.png"), filepath.Join(source, "nested", "two.png")}
	if err := os.MkdirAll(filepath.Dir(paths[1]), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		writeTestPNG(t, path)
	}
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	query := "?source=" + url.QueryEscape(source)
	response, err := http.Get(server.URL + "/api/app/canvas" + query)
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	images := value["images"].([]any)
	if response.StatusCode != http.StatusOK || len(images) != 2 || len(value["positions"].(map[string]any)) != 0 {
		t.Fatalf("unexpected initial canvas: status=%d %#v", response.StatusCode, value)
	}
	firstID := images[0].(map[string]any)["id"].(string)
	payload, _ := json.Marshal(map[string]any{
		"source":    source,
		"positions": []map[string]any{{"id": firstID, "x": 123.5, "y": -45.25}},
	})
	response, err = http.Post(server.URL+"/api/app/canvas", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if saved := responseJSON(t, response); response.StatusCode != http.StatusOK || saved["saved"] != float64(1) {
		t.Fatalf("canvas did not save: status=%d %#v", response.StatusCode, saved)
	}
	response, err = http.Get(server.URL + "/api/app/canvas" + query)
	if err != nil {
		t.Fatal(err)
	}
	value = responseJSON(t, response)
	point := value["positions"].(map[string]any)[firstID].(map[string]any)
	if point["x"] != 123.5 || point["y"] != -45.25 {
		t.Fatalf("canvas position did not persist: %#v", value)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("canvas changed an image: %v", err)
		}
	}
}

func TestCanvasRejectsFoldersOutsideIndexedSources(t *testing.T) {
	_, server := testHTTPServer(t)
	response, err := http.Get(server.URL + "/api/app/canvas?source=" + url.QueryEscape(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusBadRequest || value["ok"] != false {
		t.Fatalf("unknown canvas folder was accepted: status=%d %#v", response.StatusCode, value)
	}
}

func TestCanvasThumbnailIsGeneratedLocally(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(app.libraryDir, "thumbnail.png")
	writeTestPNG(t, picture)
	original, err := os.ReadFile(picture)
	if err != nil {
		t.Fatal(err)
	}
	app.addPath(picture)
	id := stableImageID(picture)
	response, err := http.Get(server.URL + "/thumbnail/" + id + "?size=960")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/jpeg" || len(data) < 100 {
		t.Fatalf("thumbnail failed: status=%d type=%q bytes=%d", response.StatusCode, response.Header.Get("Content-Type"), len(data))
	}
	response, err = http.Get(server.URL + "/thumbnail/" + id + "?size=1920")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	cache, err := filepath.Glob(filepath.Join(app.thumbnailDir, "*.jpg"))
	if err != nil || len(cache) != 2 {
		t.Fatalf("preview sizes did not use separate cache entries: files=%#v err=%v", cache, err)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 1 || paths[0] != picture {
		t.Fatalf("thumbnail changed the library: %#v", paths)
	}
	after, err := os.ReadFile(picture)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatalf("thumbnail changed original bytes: err=%v", err)
	}
}

func TestDeleteImageRequiresConfirmationAndExactPath(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(app.libraryDir, "delete-me.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	id := stableImageID(picture)
	tagPayload, _ := json.Marshal(map[string]any{"action": "add", "tag": "to-delete", "imageId": id})
	response, err := http.Post(server.URL+"/api/app/tags", "application/json", bytes.NewReader(tagPayload))
	if err != nil {
		t.Fatal(err)
	}
	_ = responseJSON(t, response)

	deleteRequest, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/app/images/"+id, bytes.NewBufferString(`{"path":`+strconv.Quote(picture)+`}`))
	deleteRequest.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if result := responseJSON(t, response); response.StatusCode != http.StatusForbidden || result["ok"] != false {
		t.Fatalf("unconfirmed deletion was accepted: status=%d %#v", response.StatusCode, result)
	}
	if _, err := os.Stat(picture); err != nil {
		t.Fatalf("unconfirmed deletion changed file: %v", err)
	}

	deleteRequest, _ = http.NewRequest(http.MethodDelete, server.URL+"/api/app/images/"+id, bytes.NewBufferString(`{"path":"/wrong/image.png"}`))
	deleteRequest.Header.Set("Content-Type", "application/json")
	deleteRequest.Header.Set("X-Pictogrep-Action", "delete-image")
	response, err = http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if result := responseJSON(t, response); response.StatusCode != http.StatusConflict || result["ok"] != false {
		t.Fatalf("stale delete target was accepted: status=%d %#v", response.StatusCode, result)
	}

	deleteRequest, _ = http.NewRequest(http.MethodDelete, server.URL+"/api/app/images/"+id, bytes.NewBufferString(`{"path":`+strconv.Quote(picture)+`}`))
	deleteRequest.Header.Set("Content-Type", "application/json")
	deleteRequest.Header.Set("X-Pictogrep-Action", "delete-image")
	response, err = http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if result := responseJSON(t, response); response.StatusCode != http.StatusOK || result["deleted"] != true {
		t.Fatalf("confirmed deletion failed: status=%d %#v", response.StatusCode, result)
	}
	if _, err := os.Stat(picture); !os.IsNotExist(err) {
		t.Fatalf("confirmed deletion left original file: %v", err)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 0 {
		t.Fatalf("confirmed deletion left library state: %#v", paths)
	}
	manifest, err := os.ReadFile(filepath.Join(app.tagsDir, "to-delete", "images.json"))
	if err != nil || bytes.Contains(manifest, []byte(picture)) {
		t.Fatalf("confirmed deletion left collection reference: %s err=%v", manifest, err)
	}
}

func TestBadTagRequestsHaveNoSideEffects(t *testing.T) {
	app, server := testHTTPServer(t)
	for _, payload := range []string{
		`{"action":"unknown","tag":"ghost"}`,
		`{"action":"create","tag":"ghost"} {}`,
		`{"action":"fill","model":"` + defaultEmbeddingModel.Key + `","tag":"ghost","prompt":"cats","vector":[1]}`,
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
		"imageId":    stableImageID(picture),
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
	if item["id"] != stableImageID(picture) || item["url"] != "/image/"+stableImageID(picture) {
		t.Fatalf("missing embedding did not use its stable image URL: %#v", item)
	}
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
	vector[0] = 1
	payload, _ := json.Marshal(map[string]any{"model": defaultEmbeddingModel.Key, "items": []any{map[string]any{
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
	payload, _ = json.Marshal(map[string]any{"model": defaultEmbeddingModel.Key, "vector": vector, "limit": 10})
	response, err = http.Post(server.URL+"/api/app/ai/search", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	result := responseJSON(t, response)
	if response.StatusCode != 200 || len(result["images"].([]any)) != 1 {
		t.Fatalf("semantic search failed: %#v", result)
	}
}

func TestFullReindexRequiresConfirmationAndClearsEmbeddings(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(app.libraryDir, "reference.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
	vector[0] = 1
	if err := app.updateEmbeddings(map[string]embeddingRecord{
		picture: {Mtime: embeddingMtime(picture), Vector: vector},
	}); err != nil {
		t.Fatal(err)
	}

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/app/ai/reindex", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusForbidden || value["ok"] != false {
		t.Fatalf("unconfirmed full reindex was accepted: status=%d %#v", response.StatusCode, value)
	}
	if missing := app.missingEmbeddings(); len(missing) != 0 {
		t.Fatalf("unconfirmed reindex changed embeddings: %#v", missing)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/app/ai/reindex", nil)
	request.Header.Set("X-Pictogrep-Action", "rebuild-search-index")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	if response.StatusCode != http.StatusOK || value["cleared"] != float64(1) || value["total"] != float64(1) {
		t.Fatalf("full reindex failed: status=%d %#v", response.StatusCode, value)
	}
	missing := app.missingEmbeddings()
	if len(missing) != 1 || missing[0]["path"] != picture {
		t.Fatalf("full reindex did not queue the complete library: %#v", missing)
	}
}

func TestLegacyEmbeddingTimestampIsUpgraded(t *testing.T) {
	app, server := testHTTPServer(t)
	picture := filepath.Join(app.libraryDir, "reference.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
	vector[0] = 1
	payload, _ := json.Marshal(map[string]any{"model": defaultEmbeddingModel.Key, "items": []any{map[string]any{
		"path": picture, "mtime": embeddingMtime(picture) / 1000, "vector": vector,
	}}})
	response, err := http.Post(server.URL+"/api/app/ai/embeddings", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusOK || value["saved"].(float64) != 1 {
		t.Fatalf("legacy timestamp was rejected: status=%d %#v", response.StatusCode, value)
	}
	if missing := app.missingEmbeddings(); len(missing) != 0 {
		t.Fatalf("legacy timestamp was not normalized: %#v", missing)
	}
}

func TestRelatedImagesUseCachedSemanticEmbeddings(t *testing.T) {
	app, server := testHTTPServer(t)
	paths := []string{
		filepath.Join(app.libraryDir, "reference.png"),
		filepath.Join(app.libraryDir, "closest.png"),
		filepath.Join(app.libraryDir, "different.png"),
	}
	records := map[string]embeddingRecord{}
	for index, path := range paths {
		writeTestPNG(t, path)
		app.addPath(path)
		vector := make([]float32, defaultEmbeddingModel.Dimensions)
		vector[0] = []float32{1, 0.9, 0.1}[index]
		vector[1] = []float32{0, 0.1, 0.9}[index]
		records[path] = embeddingRecord{Mtime: embeddingMtime(path), Vector: vector}
	}
	if err := app.updateEmbeddings(records); err != nil {
		t.Fatal(err)
	}

	response, err := http.Get(server.URL + "/api/app/related/" + stableImageID(paths[0]) + "?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	images := value["images"].([]any)
	if response.StatusCode != http.StatusOK || value["ready"] != true || len(images) != 2 {
		t.Fatalf("related images were not returned: status=%d %#v", response.StatusCode, value)
	}
	first := images[0].(map[string]any)
	if first["name"] != "closest.png" || first["id"] == stableImageID(paths[0]) {
		t.Fatalf("closest image was not ranked first or source was included: %#v", images)
	}
}

func TestStableImageIDDoesNotChangeWhenIndexOrderChanges(t *testing.T) {
	app, server := testHTTPServer(t)
	first := filepath.Join(app.libraryDir, "first.png")
	second := filepath.Join(app.libraryDir, "second.png")
	writeTestPNG(t, first)
	writeTestPNG(t, second)
	app.addPath(first)
	app.addPath(second)
	id := stableImageID(first)

	app.mu.Lock()
	app.paths[0], app.paths[1] = app.paths[1], app.paths[0]
	app.mu.Unlock()

	response, err := http.Get(server.URL + "/api/app/images/" + id)
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	image := value["image"].(map[string]any)
	if response.StatusCode != http.StatusOK || image["name"] != "first.png" || image["id"] != id {
		t.Fatalf("stable ID targeted the wrong image after reorder: status=%d %#v", response.StatusCode, value)
	}
}

func TestPortugueseLocaleHasEveryEnglishKey(t *testing.T) {
	locales := map[string]map[string]string{}
	for _, name := range []string{"en", "pt-BR"} {
		data, err := embeddedFiles.ReadFile("web/i18n/locales/" + name + ".json")
		locale := map[string]string{}
		if err != nil || json.Unmarshal(data, &locale) != nil {
			t.Fatalf("could not read %s locale: %v", name, err)
		}
		locales[name] = locale
	}
	for key := range locales["en"] {
		if locales["pt-BR"][key] == "" {
			t.Errorf("Portuguese locale is missing %q", key)
		}
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
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
	vector[4] = 1
	payload, _ := json.Marshal(map[string]any{"model": defaultEmbeddingModel.Key, "query": "  Red   Car ", "vector": vector})
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
	if response.StatusCode != http.StatusOK || value["cached"] != true || len(cached) != defaultEmbeddingModel.Dimensions || cached[4].(float64) != 1 {
		t.Fatalf("query cache lookup failed: status=%d %#v", response.StatusCode, value)
	}
}
