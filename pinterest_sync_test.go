package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPinterestAutoSyncDefaultsOnAndSaves(t *testing.T) {
	app := testApplication(t)
	if !app.pinterestSettings().AutoSync {
		t.Fatal("auto sync should be on until it is turned off")
	}
	if err := app.savePinterestSettings(pinterestSettings{AutoSync: false}); err != nil {
		t.Fatal(err)
	}
	if app.pinterestSettings().AutoSync {
		t.Fatal("auto sync stayed on after being turned off")
	}
	// The setting shares one config file with everything else, so it has to
	// leave the rest of that file alone.
	if err := app.saveBrowserSettings(browserSettings{ThumbnailSize: "large", HomeOrder: "recent"}); err != nil {
		t.Fatal(err)
	}
	if app.pinterestSettings().AutoSync {
		t.Fatal("saving browser settings brought auto sync back")
	}
	if settings := app.browserSettings(); settings.ThumbnailSize != "large" {
		t.Fatalf("browser settings were lost: %#v", settings)
	}
}

func TestTrackedBoardsUpsertInsteadOfStacking(t *testing.T) {
	app := testApplication(t)
	board := trackedBoard{URL: "https://www.pinterest.com/someone/board/", Mode: "board", LastSyncAt: 100}
	if err := app.trackBoard(board); err != nil {
		t.Fatal(err)
	}
	board.LastSyncAt = 200
	board.Mode = "original"
	if err := app.trackBoard(board); err != nil {
		t.Fatal(err)
	}
	boards := app.trackedBoards()
	if len(boards) != 1 {
		t.Fatalf("importing the same board twice should update one entry: %#v", boards)
	}
	if boards[0].LastSyncAt != 200 || boards[0].Mode != "original" {
		t.Fatalf("the entry was not updated: %#v", boards[0])
	}

	other := trackedBoard{URL: "https://www.pinterest.com/someone/other/", Mode: "board"}
	if err := app.trackBoard(other); err != nil {
		t.Fatal(err)
	}
	if boards := app.trackedBoards(); len(boards) != 2 {
		t.Fatalf("a different board should be tracked separately: %#v", boards)
	}
	if err := app.forgetBoard(other.URL); err != nil {
		t.Fatal(err)
	}
	boards = app.trackedBoards()
	if len(boards) != 1 || boards[0].URL != board.URL {
		t.Fatalf("forgetting removed the wrong board: %#v", boards)
	}
}

// A tracking file that got damaged must not take Pictogrep down with it, the
// same way a damaged search index does not.
func TestTrackedBoardsSurviveADamagedFile(t *testing.T) {
	app := testApplication(t)
	if err := os.MkdirAll(app.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app.trackedBoardsPath(), []byte("{not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if boards := app.trackedBoards(); len(boards) != 0 {
		t.Fatalf("a damaged file should read as no boards: %#v", boards)
	}
	if err := app.trackBoard(trackedBoard{URL: "https://www.pinterest.com/someone/board/"}); err != nil {
		t.Fatal(err)
	}
	if boards := app.trackedBoards(); len(boards) != 1 {
		t.Fatalf("tracking should recover after damage: %#v", boards)
	}
}

func TestBoardIsDueAfterAWeek(t *testing.T) {
	now := time.Now()
	fresh := trackedBoard{LastSyncAt: now.Add(-24 * time.Hour).Unix()}
	stale := trackedBoard{LastSyncAt: now.Add(-8 * 24 * time.Hour).Unix()}
	if fresh.due(now) {
		t.Fatal("a board checked yesterday is not due")
	}
	if !stale.due(now) {
		t.Fatal("a board checked eight days ago is due")
	}
	// A board that has never synced carries a zero timestamp and should be
	// picked up rather than waiting a week from the epoch.
	if !(trackedBoard{}).due(now) {
		t.Fatal("an untracked timestamp should be due")
	}
}

func TestAutoSyncRespectsSettingsAndPluginToggle(t *testing.T) {
	app, httpServer := testHTTPServer(t)
	_ = httpServer
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.trackBoard(trackedBoard{URL: "https://www.pinterest.com/someone/board/", Mode: "board"}); err != nil {
		t.Fatal(err)
	}

	if err := app.savePinterestSettings(pinterestSettings{AutoSync: false}); err != nil {
		t.Fatal(err)
	}
	if handler.syncDueBoards(context.Background(), time.Now()) {
		t.Fatal("a due board synced while auto sync was off")
	}

	if err := app.savePinterestSettings(pinterestSettings{AutoSync: true}); err != nil {
		t.Fatal(err)
	}
	if err := app.setPluginEnabled("pinterest", false); err != nil {
		t.Fatal(err)
	}
	if handler.syncDueBoards(context.Background(), time.Now()) {
		t.Fatal("a due board synced while the plugin was disabled")
	}
}

// The board is stamped before the download runs, so one that fails every time
// waits its turn again instead of being retried on every pass.
func TestFailedAutoSyncDoesNotRetryImmediately(t *testing.T) {
	app, _ := testHTTPServer(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler.galleryDL = func(context.Context, galleryDLRequest) error {
		return context.DeadlineExceeded
	}
	board := trackedBoard{URL: "https://www.pinterest.com/someone/board/", Mode: "board"}
	if err := app.trackBoard(board); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !handler.syncDueBoards(context.Background(), now) {
		t.Fatal("a due board should have been picked up")
	}
	boards := app.trackedBoards()
	if len(boards) != 1 || boards[0].LastSyncAt != now.Unix() {
		t.Fatalf("a failed sync should still record the attempt: %#v", boards)
	}
	if handler.syncDueBoards(context.Background(), now) {
		t.Fatal("the failed board was retried on the very next pass")
	}
}

// Stopping an automatic check has to stop the downloader with it, the same way
// stopping a manual import does.
func TestAutomaticSyncCanBeStopped(t *testing.T) {
	app, _ := testHTTPServer(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	running := make(chan struct{})
	stopped := make(chan error, 1)
	handler.galleryDL = func(ctx context.Context, _ galleryDLRequest) error {
		close(running)
		<-ctx.Done()
		stopped <- ctx.Err()
		return ctx.Err()
	}
	if err := app.trackBoard(trackedBoard{URL: "https://www.pinterest.com/someone/board/", Mode: "board"}); err != nil {
		t.Fatal(err)
	}
	go handler.syncDueBoards(context.Background(), time.Now())
	select {
	case <-running:
	case <-time.After(5 * time.Second):
		t.Fatal("the automatic sync never started")
	}
	if !handler.pinterest.stop() {
		t.Fatal("an automatic sync reported nothing to stop")
	}
	select {
	case err := <-stopped:
		if err == nil {
			t.Fatal("the downloader was not cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopping an automatic sync did not reach the downloader")
	}
}

func TestPinterestSettingsEndpoint(t *testing.T) {
	app, server := testHTTPServer(t)
	if err := app.trackBoard(trackedBoard{URL: "https://www.pinterest.com/someone/board/", Mode: "board"}); err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(server.URL+"/api/app/settings/pinterest", "application/json", strings.NewReader(`{"autoSync":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if value := responseJSON(t, response); response.StatusCode != http.StatusOK || value["autoSync"] != false {
		t.Fatalf("unexpected save result: status=%d %#v", response.StatusCode, value)
	}

	listed, err := http.Get(server.URL + "/api/app/plugins/pinterest/boards")
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, listed)
	boards, _ := value["boards"].([]any)
	if len(boards) != 1 || value["autoSync"] != false {
		t.Fatalf("unexpected board listing: %#v", value)
	}

	forget, err := http.Post(server.URL+"/api/app/settings/pinterest", "application/json",
		strings.NewReader(`{"forget":"https://www.pinterest.com/someone/board/"}`))
	if err != nil {
		t.Fatal(err)
	}
	if forget.StatusCode != http.StatusOK {
		t.Fatalf("forgetting a board failed: %d", forget.StatusCode)
	}
	if boards := app.trackedBoards(); len(boards) != 0 {
		t.Fatalf("the board was not forgotten: %#v", boards)
	}
}
