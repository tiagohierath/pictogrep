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
	"time"
)

func postWebImport(t *testing.T, handler http.Handler, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/app/plugins/web/import", bytes.NewReader(data))
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func webImportServer(t *testing.T) (*application, http.Handler, *galleryDLRequest) {
	t.Helper()
	app := testApplication(t)
	if err := app.setPluginEnabled("web", true); err != nil {
		t.Fatal(err)
	}
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	asked := &galleryDLRequest{}
	handler.galleryDL = func(_ context.Context, download galleryDLRequest) error {
		*asked = download
		return os.WriteFile(filepath.Join(download.Directory, "new-work.png"), testRemotePNG(t), 0o644)
	}
	return app, handler.routes(), asked
}

func TestWebSourceURLValidation(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com/art", "https://127.0.0.1/art", "http://localhost/art", "https://example.com:8080/art"} {
		if _, err := validateWebSourceURL(raw); err == nil {
			t.Fatalf("%q should not be accepted as a followable page", raw)
		}
	}
	parsed, err := validateWebSourceURL("  https://WWW.Deviantart.com/artist/gallery#top  ")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.String() != "https://www.deviantart.com/artist/gallery" {
		t.Fatalf("address was not cleaned up: %q", parsed.String())
	}
}

func TestWebSourceNameReadsTheLastMeaningfulPart(t *testing.T) {
	for raw, want := range map[string]string{
		"https://www.deviantart.com/some-artist/gallery": "some artist",
		"https://bsky.app/profile/someone.bsky.social":   "someone.bsky.social",
		"https://example.com/":                           "example",
		"https://example.com/tag/ink%20wash":             "ink wash",
	} {
		parsed, err := validateWebSourceURL(raw)
		if err != nil {
			t.Fatal(err)
		}
		if name := webSourceName(parsed); name != want {
			t.Fatalf("%s named itself %q, wanted %q", raw, name, want)
		}
	}
}

// The two boxes mean different things, and neither one ticked means nothing to
// do, so the request is refused rather than quietly doing one of them.
func TestWebImportNeedsAtLeastOneChoice(t *testing.T) {
	_, routes, _ := webImportServer(t)
	response := postWebImport(t, routes, map[string]any{"url": "https://www.deviantart.com/artist/gallery"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("an import with neither box ticked was accepted: status=%d body=%s", response.Code, response.Body)
	}
}

// Downloading the whole past is a one-time job. Following is what makes a site
// worth checking again, so only that is remembered.
func TestWebImportFollowsOnlyWhenAsked(t *testing.T) {
	app, routes, asked := webImportServer(t)
	response := postWebImport(t, routes, map[string]any{
		"url": "https://www.deviantart.com/artist/gallery", "backfill": true,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("backfill import failed: status=%d body=%s", response.Code, response.Body)
	}
	waitForPinterestImport(t, routes)
	if sources := app.webSources(); len(sources) != 0 {
		t.Fatalf("a one-time archive download was followed anyway: %#v", sources)
	}
	if asked.Limit != 0 {
		t.Fatalf("a backfill asked the downloader for only %d images", asked.Limit)
	}

	response = postWebImport(t, routes, map[string]any{
		"url": "https://www.deviantart.com/artist/gallery", "follow": true, "folder": "artist feed",
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("follow import failed: status=%d body=%s", response.Code, response.Body)
	}
	waitForPinterestImport(t, routes)
	sources := app.webSources()
	if len(sources) != 1 || sources[0].Folder != "artist-feed" {
		t.Fatalf("following did not record the site and its folder: %#v", sources)
	}
	// Following without a backfill takes the newest few, not the whole history.
	if asked.Limit != webFollowLimit {
		t.Fatalf("a follow asked the downloader for %d images, wanted %d", asked.Limit, webFollowLimit)
	}
	// The archive is what keeps a daily check from re-fetching the same newest
	// few over and over.
	if asked.Archive == "" {
		t.Fatal("a web import ran without an archive to remember what it fetched")
	}
}

func TestFollowedWebSourceIsRecheckedAndCanBeForgotten(t *testing.T) {
	app := testApplication(t)
	if err := app.setPluginEnabled("web", true); err != nil {
		t.Fatal(err)
	}
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler.galleryDL = func(_ context.Context, download galleryDLRequest) error {
		return os.WriteFile(filepath.Join(download.Directory, "new-work.png"), testRemotePNG(t), 0o644)
	}
	source := webSource{URL: "https://www.deviantart.com/artist/gallery", Name: "artist", Folder: "artist"}
	if err := app.trackWebSource(source); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !handler.syncDueWebSources(context.Background(), now) {
		t.Fatal("a site that has never been checked should be due")
	}
	sources := app.webSources()
	if len(sources) != 1 || sources[0].LastSyncAt != now.Unix() {
		t.Fatalf("the check was not recorded: %#v", sources)
	}
	if handler.syncDueWebSources(context.Background(), now) {
		t.Fatal("a site just checked was checked again on the next pass")
	}
	if err := app.forgetWebSource(source.URL); err != nil {
		t.Fatal(err)
	}
	if sources := app.webSources(); len(sources) != 0 {
		t.Fatalf("unfollowing left the site behind: %#v", sources)
	}
}

// Following the same page twice is one entry, not two downloads of the same
// gallery every day.
func TestTrackedWebSourcesUpsert(t *testing.T) {
	app := testApplication(t)
	source := webSource{URL: "https://example.com/artist", Name: "artist", AddedAt: 10, LastSyncAt: 10}
	if err := app.trackWebSource(source); err != nil {
		t.Fatal(err)
	}
	source.LastSyncAt = 500
	source.AddedAt = 0
	if err := app.trackWebSource(source); err != nil {
		t.Fatal(err)
	}
	sources := app.webSources()
	if len(sources) != 1 || sources[0].LastSyncAt != 500 || sources[0].AddedAt != 10 {
		t.Fatalf("following the same page twice did not update one entry: %#v", sources)
	}
}

// A Bluesky handle used to come out as someonebskysocial, because everything
// that is not a letter or a number is dropped on the way to a folder name.
func TestWebFolderNameKeepsHandlesReadable(t *testing.T) {
	folder, err := webFolderName("someone.bsky.social")
	if err != nil {
		t.Fatal(err)
	}
	if folder != "someone-bsky-social" {
		t.Fatalf("handle became the folder %q", folder)
	}
	if _, err := webFolderName(".."); err == nil {
		t.Fatal("a folder name of dots was accepted")
	}
}

// The two importers share a downloader but not a switch. Turning boards off
// used to stop every artist you follow as well, silently.
func TestWebImportSurvivesThePinterestPluginBeingOff(t *testing.T) {
	app := testApplication(t)
	if err := app.setPluginEnabled("web", true); err != nil {
		t.Fatal(err)
	}
	if err := app.setPluginEnabled("pinterest", false); err != nil {
		t.Fatal(err)
	}
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is downloaded here. Whether the request is allowed to start is
	// the whole question.
	handler.galleryDL = func(context.Context, galleryDLRequest) error { return nil }
	routes := handler.routes()
	response := postWebImport(t, routes, map[string]any{
		"url": "https://www.deviantart.com/artist/gallery", "backfill": true,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("web import stopped working with Pinterest off: status=%d body=%s", response.Code, response.Body)
	}
	waitForPinterestImport(t, routes)

	if err := app.setPluginEnabled("web", false); err != nil {
		t.Fatal(err)
	}
	response = postWebImport(t, routes, map[string]any{
		"url": "https://www.deviantart.com/artist/gallery", "backfill": true,
	})
	if response.Code != http.StatusNotFound {
		t.Fatalf("web import ran with its own plugin off: status=%d body=%s", response.Code, response.Body)
	}
}

func TestWebAutoSyncHasItsOwnSwitch(t *testing.T) {
	app := testApplication(t)
	if !app.webSettings().AutoSync {
		t.Fatal("following should be on until it is turned off")
	}
	if err := app.saveWebSettings(webSettings{AutoSync: false}); err != nil {
		t.Fatal(err)
	}
	if app.webSettings().AutoSync {
		t.Fatal("following stayed on after being turned off")
	}
	if !app.pinterestSettings().AutoSync {
		t.Fatal("turning followed websites off also stopped board sync")
	}
	if err := app.savePinterestSettings(pinterestSettings{AutoSync: false}); err != nil {
		t.Fatal(err)
	}
	if app.webSettings().AutoSync {
		t.Fatal("saving board settings brought website following back")
	}
	if err := app.saveWebSettings(webSettings{AutoSync: true}); err != nil {
		t.Fatal(err)
	}
	if !app.webSettings().AutoSync || app.pinterestSettings().AutoSync {
		t.Fatal("the two switches are not independent")
	}
}

// The folder mirrors the page it came from, so a picture already in the library
// is listed in it rather than quietly left out.
func TestWebImportListsExistingLibraryImageInTheFolder(t *testing.T) {
	app := testApplication(t)
	if err := app.setPluginEnabled("web", true); err != nil {
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
		return os.WriteFile(filepath.Join(download.Directory, "same.png"), data, 0o644)
	}
	routes := handler.routes()
	response := postWebImport(t, routes, map[string]any{
		"url": "https://www.deviantart.com/artist/gallery", "backfill": true, "folder": "artist",
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("web import failed: status=%d body=%s", response.Code, response.Body)
	}
	result := pinterestResult(t, waitForPinterestImport(t, routes))
	if result["linked"] != float64(1) {
		t.Fatalf("an image already in the library was not listed in the folder: %#v", result)
	}
	manifest, err := os.ReadFile(filepath.Join(app.tagsDir, "artist", "images.json"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := json.Unmarshal(manifest, &paths); err != nil || len(paths) != 1 || paths[0] != existing {
		t.Fatalf("folder does not mirror the page: err=%v paths=%#v", err, paths)
	}
}

// A followed artist who has posted nothing since yesterday is the ordinary
// case, not a failure, and must not look like one.
func TestReCheckWithNothingNewIsNotAFailure(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler.galleryDL = func(context.Context, galleryDLRequest) error { return nil }
	if err := app.trackWebSource(webSource{
		URL: "https://www.deviantart.com/artist/gallery", Name: "artist", Folder: "artist",
	}); err != nil {
		t.Fatal(err)
	}
	if !handler.syncDueWebSources(context.Background(), time.Now()) {
		t.Fatal("a due site was not checked")
	}
	if status := handler.pinterest.snapshot(); status["state"] != "done" {
		t.Fatalf("a quiet re-check was reported as %v: %#v", status["state"], status)
	}

	// A first import that finds nothing is a different story: the address is
	// wrong, or the downloader does not know the site, and saying so is the
	// only way anyone finds out.
	if err := app.setPluginEnabled("web", true); err != nil {
		t.Fatal(err)
	}
	empty := handler.routes()
	response := postWebImport(t, empty, map[string]any{
		"url": "https://example.com/nothing-here", "backfill": true,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("import was refused before it ran: status=%d body=%s", response.Code, response.Body)
	}
	status := waitForPinterestImport(t, empty)
	if status["state"] != "error" {
		t.Fatalf("a first import that found nothing was reported as %v", status["state"])
	}
}

// The stored folder is a plain file on disk between runs and it ends up in a
// path join, so it is cleaned again on the way in rather than trusted.
func TestSyncRefusesATamperedFolder(t *testing.T) {
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
	if err := app.trackWebSource(webSource{
		URL: "https://www.deviantart.com/artist/gallery", Name: "artist", Folder: "../../escape",
	}); err != nil {
		t.Fatal(err)
	}
	if handler.syncDueWebSources(context.Background(), time.Now()) {
		t.Fatal("a site with an unusable folder was checked anyway")
	}
	if ran {
		t.Fatal("the downloader ran for a site with an unusable folder")
	}
	if sources := app.webSources(); len(sources) != 0 {
		t.Fatalf("the unusable entry was kept: %#v", sources)
	}
}

// One downloader serves both panels, so a running job has to say which panel
// started it or the wrong one reports the result.
func TestRunningJobSaysWhichPanelStartedIt(t *testing.T) {
	_, routes, _ := webImportServer(t)
	response := postWebImport(t, routes, map[string]any{
		"url": "https://www.deviantart.com/artist/gallery", "backfill": true,
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("web import failed: status=%d body=%s", response.Code, response.Body)
	}
	started := recorderJSON(t, response)
	if started["kind"] != "web" {
		t.Fatalf("a web import called itself %v", started["kind"])
	}
	waitForPinterestImport(t, routes)
}
