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
	"time"
)

func TestGalleryDLArgumentsApplyDownloadLimits(t *testing.T) {
	arguments := galleryDLArguments(galleryDLRequest{URL: "https://www.pinterest.com/artist/board/", Directory: t.TempDir()})
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
	arguments := galleryDLArguments(galleryDLRequest{URL: "https://www.pinterest.com/artist/board/", Directory: t.TempDir()})
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
	handler.galleryDL = func(context.Context, galleryDLRequest) error {
		ran = true
		return nil
	}
	routes := handler.routes()
	response := postPinterestImport(t, routes, map[string]any{
		"url": "https://pinterest.com/artist/board/", "mode": "board", "skipExisting": true,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("import was not accepted for background download: status=%d body=%s", response.Code, response.Body)
	}
	if status := waitForPinterestImport(t, routes); status["state"] != "error" || !ran {
		t.Fatalf("default-enabled plugin did not run gallery-dl: ran=%v status=%#v", ran, status)
	}
	if err := app.setPluginEnabled("pinterest", false); err != nil {
		t.Fatal(err)
	}
	ran = false
	response = postPinterestImport(t, routes, map[string]any{
		"url": "https://pinterest.com/artist/board/", "mode": "board", "skipExisting": true,
	})
	if response.Code != http.StatusNotFound || ran {
		t.Fatalf("disabled plugin ran gallery-dl: status=%d ran=%v body=%s", response.Code, ran, response.Body)
	}
	if err := app.setPluginEnabled("pinterest", true); err != nil {
		t.Fatal(err)
	}
	response = postPinterestImport(t, routes, map[string]any{
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
	handler.galleryDL = func(_ context.Context, download galleryDLRequest) error {
		if download.URL != "https://www.pinterest.com/artist/film-references/" {
			return fmt.Errorf("gallery-dl received unexpected URL: %s", download.URL)
		}
		if err := os.WriteFile(filepath.Join(download.Directory, "one.png"), picture, 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(download.Directory, "two.png"), picture, 0o644)
	}
	routes := handler.routes()
	response := postPinterestImport(t, routes, map[string]any{
		"url":  "https://www.pinterest.com/artist/film-references/",
		"mode": "board", "skipExisting": true,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("Pinterest import was not accepted: status=%d body=%s", response.Code, response.Body)
	}
	result := pinterestResult(t, waitForPinterestImport(t, routes))
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
	handler.galleryDL = func(_ context.Context, download galleryDLRequest) error {
		return os.WriteFile(filepath.Join(download.Directory, "Original Camera Name.png"), testRemotePNG(t), 0o644)
	}
	routes := handler.routes()
	response := postPinterestImport(t, routes, map[string]any{
		"url":  "https://www.pinterest.com/artist/loose-references/",
		"mode": "original", "skipExisting": true,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("Pinterest original import failed: status=%d body=%s", response.Code, response.Body)
	}
	waitForPinterestImport(t, routes)
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
	handler.galleryDL = func(_ context.Context, download galleryDLRequest) error {
		data, readErr := os.ReadFile(existing)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(filepath.Join(download.Directory, "duplicate.png"), data, 0o644)
	}
	routes := handler.routes()
	response := postPinterestImport(t, routes, map[string]any{
		"url": "https://www.pinterest.com/artist/duplicates/", "mode": "board", "skipExisting": false,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("Pinterest import failed: status=%d body=%s", response.Code, response.Body)
	}
	result := pinterestResult(t, waitForPinterestImport(t, routes))
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

func TestPinterestImportKeepsDownloadingAfterTheWindowCloses(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	picture := testRemotePNG(t)
	release := make(chan struct{})
	handler.galleryDL = func(_ context.Context, download galleryDLRequest) error {
		<-release
		return os.WriteFile(filepath.Join(download.Directory, "late.png"), picture, 0o644)
	}
	routes := handler.routes()

	// A request context cancelled the instant the import starts stands in for a
	// window that was closed straight away.
	ctx, closeWindow := context.WithCancel(context.Background())
	body, err := json.Marshal(map[string]any{
		"url": "https://www.pinterest.com/artist/slow-board/", "mode": "board", "skipExisting": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/app/plugins/pinterest/import", bytes.NewReader(body)).WithContext(ctx)
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	closeWindow()
	if response.Code != http.StatusAccepted {
		t.Fatalf("import was not accepted: status=%d body=%s", response.Code, response.Body)
	}

	if !handler.pinterest.running() {
		t.Fatal("closing the window stopped the import")
	}
	close(release)
	result := pinterestResult(t, waitForPinterestImport(t, routes))
	if result["imported"] != float64(1) {
		t.Fatalf("import that outlived its window did not finish: %#v", result)
	}
}

func TestStoppingAPinterestImportKeepsWhatAlreadyDownloaded(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	picture := testRemotePNG(t)
	downloading := make(chan struct{})
	handler.galleryDL = func(ctx context.Context, download galleryDLRequest) error {
		if err := os.WriteFile(filepath.Join(download.Directory, "arrived.png"), picture, 0o644); err != nil {
			return err
		}
		close(downloading)
		<-ctx.Done()
		return ctx.Err()
	}
	routes := handler.routes()
	response := postPinterestImport(t, routes, map[string]any{
		"url": "https://www.pinterest.com/artist/stopped/", "mode": "board", "skipExisting": true,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("import was not accepted: status=%d body=%s", response.Code, response.Body)
	}
	<-downloading

	cancel := httptest.NewRequest(http.MethodDelete, "/api/app/plugins/pinterest/import", nil)
	cancel.Host = "127.0.0.1"
	cancelled := httptest.NewRecorder()
	routes.ServeHTTP(cancelled, cancel)
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel was refused: status=%d body=%s", cancelled.Code, cancelled.Body)
	}

	status := waitForPinterestImport(t, routes)
	if status["state"] != "cancelled" {
		t.Fatalf("stopped import did not report itself stopped: %#v", status)
	}
	result := pinterestResult(t, status)
	if result["imported"] != float64(1) {
		t.Fatalf("stopping threw away an image that had already downloaded: %#v", result)
	}
}

func TestPinterestImportReportsProgressWhileItRuns(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler.pinterest.start("film references", "pinterest", func() {})
	handler.pinterest.progress("downloading", 128, 0)
	status := handler.pinterest.snapshot()
	if status["state"] != "running" || status["phase"] != "downloading" || status["done"] != 128 {
		t.Fatalf("download progress is not reported: %#v", status)
	}
	handler.pinterest.progress("importing", 40, 128)
	status = handler.pinterest.snapshot()
	if status["phase"] != "importing" || status["done"] != 40 || status["total"] != 128 {
		t.Fatalf("import progress is not reported: %#v", status)
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
		"watchPinterestImport", "resumePinterestImport", "openImportedPinterestFolder", `t("pinterest.imported_notice"`,
	} {
		if !bytes.Contains(script, []byte(required)) {
			t.Errorf("Pinterest UI behavior is missing %q", required)
		}
	}
	if !bytes.Contains(style, []byte("#pinterestSection:not([hidden])")) {
		t.Fatal("Pinterest import page is hidden by drawer page styling")
	}
}

func scriptSection(t *testing.T, from, to string) []byte {
	t.Helper()
	script, err := embeddedFiles.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(script, []byte(from))
	if start < 0 {
		t.Fatalf("web/app.js no longer defines %q", from)
	}
	rest := script[start:]
	end := bytes.Index(rest, []byte(to))
	if end < 0 {
		t.Fatalf("web/app.js no longer defines %q", to)
	}
	return rest[:end]
}

func TestStartingAnImportHandsItOffAndReturnsToTheLibrary(t *testing.T) {
	handoff := scriptSection(t, "async function importPinterestBoard", "function showPinterestWorking")
	for _, required := range []string{"closeMenu()", "showMenuHome()", `t("pinterest.handed_off"`, "watchPinterestImport()"} {
		if !bytes.Contains(handoff, []byte(required)) {
			t.Errorf("starting an import does not run %s, so the user is left waiting on the panel", required)
		}
	}
	// showMenuHome ends by opening the drawer, so closing has to come after it.
	if bytes.Index(handoff, []byte("closeMenu()")) < bytes.Index(handoff, []byte("showMenuHome()")) {
		t.Error("the drawer is closed before showMenuHome reopens it, leaving the menu on screen")
	}
}

func TestNotificationsAreShownAndNotOnlyLogged(t *testing.T) {
	notify := scriptSection(t, "function showMessage(", "function closeMessage(")
	if bytes.Contains(notify, []byte("return;")) {
		t.Error("showMessage still drops everything that is not an error, so success notices never appear")
	}
	if !bytes.Contains(notify, []byte("seconds * 1000")) {
		t.Error("showMessage cannot hold a notice on screen for a given time")
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

func waitForPinterestImport(t *testing.T, routes http.Handler) map[string]any {
	t.Helper()
	for attempt := 0; attempt < 600; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "/api/app/plugins/pinterest/import", nil)
		request.Host = "127.0.0.1"
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		status := recorderJSON(t, response)
		if status["state"] != "running" {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the background Pinterest import never finished")
	return nil
}

func pinterestResult(t *testing.T, status map[string]any) map[string]any {
	t.Helper()
	result, ok := status["result"].(map[string]any)
	if !ok {
		t.Fatalf("import did not finish with a result: %#v", status)
	}
	return result
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

func TestUnusableBoardFolderNameIsRefusedBeforeDownloading(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	handler.galleryDL = func(context.Context, galleryDLRequest) error {
		ran = true
		return nil
	}
	routes := handler.routes()
	response := postPinterestImport(t, routes, map[string]any{
		"url": "https://www.pinterest.com/artist/!!!/", "mode": "board",
	})
	if response.Code != http.StatusBadRequest || ran {
		t.Fatalf("a board name that cannot be a folder was only caught after downloading: status=%d ran=%v body=%s", response.Code, ran, response.Body)
	}
	// The same board is fine when it never needs a folder name.
	response = postPinterestImport(t, routes, map[string]any{
		"url": "https://www.pinterest.com/artist/!!!/", "mode": "original",
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("a library import was refused over a folder name it does not use: status=%d body=%s", response.Code, response.Body)
	}
	waitForPinterestImport(t, routes)
}

func TestStoppingIsVisibleWhileTheImportFinishesUp(t *testing.T) {
	job := &pinterestImport{}
	if !job.start("film references", "pinterest", func() {}) {
		t.Fatal("a fresh import refused to start")
	}
	if _, stopping := job.snapshot()["stopping"]; stopping {
		t.Fatal("a running import already reports that it is stopping")
	}
	if !job.stop() {
		t.Fatal("a running import could not be stopped")
	}
	// Stopping still has to import what already arrived, so the job stays
	// running: the panel would otherwise keep claiming it is downloading.
	if status := job.snapshot(); status["state"] != "running" || status["stopping"] != true {
		t.Fatalf("a stopping import does not say so: %#v", status)
	}
	job.finish(map[string]any{"imported": float64(2)}, nil)
	if status := job.snapshot(); status["state"] != "cancelled" || status["stopping"] != nil {
		t.Fatalf("a finished import still reports that it is stopping: %#v", status)
	}
}

func TestBackgroundWorkReportsAPanicInsteadOfClosingPictogrep(t *testing.T) {
	// net/http recovers a panicking handler, but a background import has no
	// such net: an unguarded panic would take the whole process with it.
	var reported error
	func() {
		defer guard(func(err error) { reported = err })
		panic("gallery-dl returned something impossible")
	}()
	if reported == nil {
		t.Fatal("a panicking background job reported no error")
	}
	reported = nil
	func() {
		defer guard(func(err error) { reported = err })
	}()
	if reported != nil {
		t.Fatalf("ordinary background work was reported as a failure: %v", reported)
	}
}
