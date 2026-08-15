package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGalleryDLArgumentsApplyDownloadLimits(t *testing.T) {
	arguments := galleryDLArguments("https://www.pinterest.com/artist/board/", t.TempDir())
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{
		"--config-ignore", "--no-input", "--range 1-5001",
		fmt.Sprintf("--filesize-max %d", maxUploadBytes),
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("gallery-dl arguments do not contain %q: %s", expected, joined)
		}
	}
}

func TestPinterestDownloadUsageCountsAllDownloadedBytes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "image.jpg"), make([]byte, 13), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "video.mp4"), make([]byte, 29), 0o644); err != nil {
		t.Fatal(err)
	}
	files, size, err := pinterestDownloadUsage(root)
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 || size != 42 {
		t.Fatalf("download usage files=%d bytes=%d", files, size)
	}
}

func TestPinterestDownloadUsageIgnoresFilesRenamedMidWalk(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 64; index++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("settled-%02d.jpg", index)), make([]byte, 8), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stop, renamed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(renamed)
		for index := 0; index < 500; index++ {
			select {
			case <-stop:
				return
			default:
			}
			part := filepath.Join(root, fmt.Sprintf("pin-%03d.jpg.part", index))
			if err := os.WriteFile(part, make([]byte, 8), 0o644); err != nil {
				return
			}
			if err := os.Rename(part, strings.TrimSuffix(part, ".part")); err != nil {
				return
			}
		}
	}()

	var failure error
	for monitoring := true; monitoring; {
		select {
		case <-renamed:
			monitoring = false
		default:
			if _, _, err := pinterestDownloadUsage(root); err != nil {
				failure, monitoring = err, false
			}
		}
	}
	close(stop)
	<-renamed
	if failure != nil {
		t.Fatalf("download monitor aborted the import while gallery-dl renamed a file: %v", failure)
	}
}

func TestGalleryDLArgumentsSkipVideoDownloads(t *testing.T) {
	arguments := galleryDLArguments("https://www.pinterest.com/artist/board/", t.TempDir())
	filter := ""
	for index, argument := range arguments {
		if argument == "--filter" && index+1 < len(arguments) {
			filter = arguments[index+1]
		}
	}
	if filter == "" {
		t.Fatal("gallery-dl is not restricted to importable images")
	}
	for extension := range imageExtensions {
		if !strings.Contains(filter, "'"+strings.TrimPrefix(extension, ".")+"'") {
			t.Errorf("gallery-dl filter drops supported extension %q: %s", extension, filter)
		}
	}
	if strings.Contains(filter, "mp4") {
		t.Errorf("gallery-dl filter still spends the download budget on videos: %s", filter)
	}
}

func TestGalleryDLDiagnosticOutputIsBounded(t *testing.T) {
	output := &boundedCommandOutput{maximum: 16}
	if _, err := output.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("abcdefghijkl")); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "6789abcdefghijkl" {
		t.Fatalf("bounded output retained %q", got)
	}
}

func TestPinterestBoardURLValidation(t *testing.T) {
	valid := []string{
		"https://www.pinterest.com/artist/film-reference/",
		"https://br.pinterest.com/artist/film-reference/?invite_code=secret#pins",
	}
	for _, raw := range valid {
		parsed, err := validatePinterestBoardURL(raw)
		if err != nil {
			t.Errorf("valid Pinterest URL %q was rejected: %v", raw, err)
			continue
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Scheme != "https" {
			t.Errorf("Pinterest URL was not canonicalized: %s", parsed)
		}
	}
	for _, raw := range []string{
		"https://example.com/artist/board/",
		"https://pinterest.com/artist/",
		"https://pinterest.com/pin/123456/",
		"https://pin.it/AbCd123",
		"file:///tmp/board",
		"https://user:password@pinterest.com/artist/board/",
		"https://pinterest.com:8443/artist/board/",
	} {
		if _, err := validatePinterestBoardURL(raw); err == nil {
			t.Errorf("invalid Pinterest board URL %q was accepted", raw)
		}
	}
}

func TestPinterestDownloadedImagesFindsSupportedFiles(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.jpg", "a.png", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(nested, name), []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	images, err := pinterestDownloadedImages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || filepath.Base(images[0]) != "a.png" || filepath.Base(images[1]) != "b.jpg" {
		t.Fatalf("unexpected gallery-dl image list: %#v", images)
	}
}

func TestPinterestImportIsOptionalAndValidatesBeforeGalleryDL(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	handler.galleryDL = func(context.Context, string, string) error {
		ran = true
		return nil
	}
	response := postPinterestImport(t, handler.routes(), map[string]any{
		"url": "https://pinterest.com/artist/board/", "mode": "board", "skipExisting": true,
	})
	if response.Code != http.StatusBadRequest || !ran {
		t.Fatalf("default-enabled plugin did not run gallery-dl: status=%d ran=%v body=%s", response.Code, ran, response.Body)
	}
	if err := app.setPluginEnabled("pinterest", false); err != nil {
		t.Fatal(err)
	}
	ran = false
	response = postPinterestImport(t, handler.routes(), map[string]any{
		"url": "https://pinterest.com/artist/board/", "mode": "board", "skipExisting": true,
	})
	if response.Code != http.StatusNotFound || ran {
		t.Fatalf("disabled plugin ran gallery-dl: status=%d ran=%v body=%s", response.Code, ran, response.Body)
	}
	if err := app.setPluginEnabled("pinterest", true); err != nil {
		t.Fatal(err)
	}
	response = postPinterestImport(t, handler.routes(), map[string]any{
		"url": "https://example.com/artist/board/", "mode": "board", "skipExisting": true,
	})
	if response.Code != http.StatusBadRequest || ran {
		t.Fatalf("invalid URL reached gallery-dl: status=%d ran=%v body=%s", response.Code, ran, response.Body)
	}
}

func TestPinterestImportUsesGalleryDLAndSkipsExistingBytes(t *testing.T) {
	app := testApplication(t)
	if err := app.setPluginEnabled("pinterest", true); err != nil {
		t.Fatal(err)
	}
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	picture := testRemotePNG(t)
	handler.galleryDL = func(_ context.Context, raw, directory string) error {
		if raw != "https://www.pinterest.com/artist/film-references/" {
			t.Fatalf("gallery-dl received unexpected URL: %s", raw)
		}
		if err := os.WriteFile(filepath.Join(directory, "one.png"), picture, 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(directory, "two.png"), picture, 0o644)
	}
	response := postPinterestImport(t, handler.routes(), map[string]any{
		"url":  "https://www.pinterest.com/artist/film-references/",
		"mode": "board", "skipExisting": true,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("Pinterest import failed: status=%d body=%s", response.Code, response.Body)
	}
	result := recorderJSON(t, response)
	if result["board"] != "film references" || result["folder"] != "film-references" ||
		result["total"] != float64(2) || result["imported"] != float64(1) ||
		result["skipped"] != float64(1) || result["failed"] != float64(0) {
		t.Fatalf("unexpected Pinterest import result: %#v", result)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 1 || filepath.Base(paths[0]) != "one.png" {
		t.Fatalf("Pinterest duplicate changed the library: %#v", paths)
	}
	manifest, err := os.ReadFile(filepath.Join(app.tagsDir, "film-references", "images.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tagged []string
	if err := json.Unmarshal(manifest, &tagged); err != nil || len(tagged) != 1 || tagged[0] != paths[0] {
		t.Fatalf("Pinterest board folder is wrong: err=%v paths=%#v", err, tagged)
	}
}

func TestPinterestOriginalModeKeepsGalleryDLFilename(t *testing.T) {
	app := testApplication(t)
	if err := app.setPluginEnabled("pinterest", true); err != nil {
		t.Fatal(err)
	}
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler.galleryDL = func(_ context.Context, _ string, directory string) error {
		return os.WriteFile(filepath.Join(directory, "Original Camera Name.png"), testRemotePNG(t), 0o644)
	}
	response := postPinterestImport(t, handler.routes(), map[string]any{
		"url":  "https://www.pinterest.com/artist/loose-references/",
		"mode": "original", "skipExisting": true,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("Pinterest original import failed: status=%d body=%s", response.Code, response.Body)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 1 || filepath.Base(paths[0]) != "Original-Camera-Name.png" {
		t.Fatalf("gallery-dl filename was not kept: %#v", paths)
	}
	if _, err := os.Stat(filepath.Join(app.tagsDir, "loose-references")); !os.IsNotExist(err) {
		t.Fatalf("original mode unexpectedly created a board folder: %v", err)
	}
}

func TestPinterestBoardLinksExistingDuplicateOnce(t *testing.T) {
	app := testApplication(t)
	if err := app.setPluginEnabled("pinterest", true); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(app.libraryDir, "existing.png")
	writeTestPNG(t, existing)
	app.addPath(existing)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler.galleryDL = func(_ context.Context, _ string, directory string) error {
		data, readErr := os.ReadFile(existing)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(filepath.Join(directory, "duplicate.png"), data, 0o644)
	}
	response := postPinterestImport(t, handler.routes(), map[string]any{
		"url": "https://www.pinterest.com/artist/duplicates/", "mode": "board", "skipExisting": false,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("Pinterest import failed: status=%d body=%s", response.Code, response.Body)
	}
	result := recorderJSON(t, response)
	if result["linked"] != float64(1) || result["imported"] != float64(0) {
		t.Fatalf("unexpected duplicate result: %#v", result)
	}
	manifest, err := os.ReadFile(filepath.Join(app.tagsDir, "duplicates", "images.json"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := json.Unmarshal(manifest, &paths); err != nil || len(paths) != 1 || paths[0] != existing {
		t.Fatalf("duplicate was not linked once: err=%v paths=%#v", err, paths)
	}
}

func TestPinterestPluginStateIsPublishedAndPersists(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.appState(response, httptest.NewRequest(http.MethodGet, "/api/app/state", nil))
	plugins := recorderJSON(t, response)["plugins"].(map[string]any)
	if plugins["pinterest"].(map[string]any)["enabled"] != true {
		t.Fatalf("Pinterest plugin should start enabled: %#v", plugins["pinterest"])
	}
	if err := app.setPluginEnabled("pinterest", false); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.pluginEnabled("pinterest") {
		t.Fatal("disabled Pinterest plugin did not survive restart")
	}
}

func TestPinterestUIContainsRequestedOptionalImportForm(t *testing.T) {
	index, err := embeddedFiles.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := embeddedFiles.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	style, err := embeddedFiles.ReadFile("web/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`id="pinterestPluginToggle"`, `id="pinterestSection"`,
		`Import from Pinterest`, `Board link`, `value="original"`, `value="board" checked`,
		`id="pinterestSkipExisting" type="checkbox" checked`, `id="pinterestImportButton" data-i18n="pinterest.download_all">Download all`,
		`Use it again anytime from Menu → Import from Pinterest`,
	} {
		if !bytes.Contains(index, []byte(required)) {
			t.Errorf("Pinterest UI is missing %q", required)
		}
	}
	for _, required := range []string{
		"togglePinterestPlugin", "importPinterestBoard", "/api/app/plugins/pinterest/import",
		"pinterestImportController", "openImportedPinterestFolder", `t("pinterest.imported_notice"`,
	} {
		if !bytes.Contains(script, []byte(required)) {
			t.Errorf("Pinterest UI behavior is missing %q", required)
		}
	}
	if !bytes.Contains(style, []byte("#pinterestSection:not([hidden])")) {
		t.Fatal("Pinterest import page is hidden by drawer page styling")
	}
}

func TestPinterestBoardNameComesFromURL(t *testing.T) {
	boardURL, _ := url.Parse("https://www.pinterest.com/artist/film-references/")
	if name := pinterestBoardName(boardURL); name != "film references" {
		t.Fatalf("unexpected Pinterest board name: %q", name)
	}
}

func TestWordmarkHoverEasterEggIsPlainText(t *testing.T) {
	index, err := embeddedFiles.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := embeddedFiles.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(index, []byte(`<span class="wordmark" id="wordmark">pictogrep</span>`)) {
		t.Fatal("wordmark is not plain text")
	}
	for _, expected := range []string{`$("#wordmark").onmouseenter`, `"navylily.tv"`, `$("#wordmark").onmouseleave`} {
		if !bytes.Contains(script, []byte(expected)) {
			t.Fatalf("wordmark easter egg is missing %q", expected)
		}
	}
}

func postPinterestImport(t *testing.T, handler http.Handler, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/app/plugins/pinterest/import", bytes.NewReader(data))
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func recorderJSON(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	result := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
