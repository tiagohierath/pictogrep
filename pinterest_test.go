package main

import (
	"bytes"
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
		// The two a phone produces. Pinterest's own share sheet hands out
		// pin.it links and nothing else, and they usually resolve to a single
		// pin, so refusing either refused the only address most people have.
		"https://pin.it/AbCd123",
		"https://pinterest.com/pin/123456/",
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
		"https://pin.it/",
		"https://pin.it/too/many/parts",
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

func recorderJSON(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	result := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
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
