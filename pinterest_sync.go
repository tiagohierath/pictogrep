package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A Pinterest board is a living thing: people keep pinning to it. Importing one
// used to be a single snapshot, so a board that grew afterwards quietly drifted
// out of date and the only fix was noticing and importing it again by hand.
// Every board that gets imported is now remembered and re-checked about once a
// week, which is roughly how often a board worth following changes.
//
// Re-checking is the same import that ran the first time, with duplicates
// skipped, so it only ever adds what is new. Nothing is deleted, and nothing
// already in the library is touched.

const (
	pinterestSyncEvery    = 7 * 24 * time.Hour
	pinterestSyncCheck    = 1 * time.Hour
	pinterestSyncFirstRun = 2 * time.Minute
)

type trackedBoard struct {
	URL          string `json:"url"`
	Mode         string `json:"mode"`
	SkipExisting bool   `json:"skipExisting"`
	LastSyncAt   int64  `json:"lastSyncAt"`
}

type pinterestSettings struct {
	AutoSync bool `json:"autoSync"`
}

func (a *application) pinterestSettings() pinterestSettings {
	settings := pinterestSettings{AutoSync: true}
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return settings
	}
	var document struct {
		Pinterest *struct {
			AutoSync *bool `json:"autoSync"`
		} `json:"pinterest"`
	}
	if json.Unmarshal(data, &document) == nil && document.Pinterest != nil && document.Pinterest.AutoSync != nil {
		settings.AutoSync = *document.Pinterest.AutoSync
	}
	return settings
}

func (a *application) savePinterestSettings(settings pinterestSettings) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document["pinterest"] = settings
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := a.configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.configPath)
}

func (a *application) trackedBoardsPath() string {
	return filepath.Join(a.dataDir, "pinterest-boards.json")
}

func (a *application) trackedBoards() []trackedBoard {
	data, err := os.ReadFile(a.trackedBoardsPath())
	if err != nil {
		return nil
	}
	var boards []trackedBoard
	if json.Unmarshal(data, &boards) != nil {
		return nil
	}
	kept := boards[:0]
	for _, board := range boards {
		if strings.TrimSpace(board.URL) != "" {
			kept = append(kept, board)
		}
	}
	return kept
}

func (a *application) saveTrackedBoards(boards []trackedBoard) error {
	if boards == nil {
		boards = []trackedBoard{}
	}
	data, err := json.MarshalIndent(boards, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	tmp := a.trackedBoardsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.trackedBoardsPath())
}

// Importing the same board twice updates the one entry instead of stacking up
// duplicates that would each re-download the same pins.
func (a *application) trackBoard(board trackedBoard) error {
	boards := a.trackedBoards()
	for index, existing := range boards {
		if existing.URL == board.URL {
			boards[index] = board
			return a.saveTrackedBoards(boards)
		}
	}
	return a.saveTrackedBoards(append(boards, board))
}

func (a *application) forgetBoard(url string) error {
	boards := a.trackedBoards()
	kept := make([]trackedBoard, 0, len(boards))
	for _, board := range boards {
		if board.URL != url {
			kept = append(kept, board)
		}
	}
	return a.saveTrackedBoards(kept)
}

func (s *server) appPinterestBoards(w http.ResponseWriter, _ *http.Request) {
	boards := s.app.trackedBoards()
	if boards == nil {
		boards = []trackedBoard{}
	}
	sendJSON(w, http.StatusOK, map[string]any{
		"ok": true, "boards": boards, "autoSync": s.app.pinterestSettings().AutoSync,
	})
}

func (s *server) savePinterestSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AutoSync *bool  `json:"autoSync"`
		Forget   string `json:"forget"`
	}
	if err := decodeJSON(r, &request, 1<<16); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if request.Forget != "" {
		if err := s.app.forgetBoard(request.Forget); err != nil {
			sendError(w, http.StatusBadRequest, err)
			return
		}
	}
	if request.AutoSync != nil {
		if err := s.app.savePinterestSettings(pinterestSettings{AutoSync: *request.AutoSync}); err != nil {
			sendError(w, http.StatusBadRequest, err)
			return
		}
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "autoSync": s.app.pinterestSettings().AutoSync})
}

func (board trackedBoard) due(now time.Time) bool {
	return now.Sub(time.Unix(board.LastSyncAt, 0)) >= pinterestSyncEvery
}

// syncDueBoards re-checks at most one board per pass. An import is heavy and
// only one can run at a time anyway, so taking them one per hour spreads the
// work out instead of queueing a pile of downloads behind each other.
func (s *server) syncDueBoards(ctx context.Context, now time.Time) bool {
	if !s.app.pluginEnabled("pinterest") || !s.app.pinterestSettings().AutoSync {
		return false
	}
	if s.pinterest.running() || s.app.indexing() {
		return false
	}
	for _, board := range s.app.trackedBoards() {
		if !board.due(now) {
			continue
		}
		boardURL, err := validatePinterestBoardURL(board.URL)
		if err != nil {
			// A board link that stopped being valid is dropped rather than
			// retried forever.
			_ = s.app.forgetBoard(board.URL)
			continue
		}
		// A real cancel, not a placeholder. Stopping an automatic check has to
		// stop the downloader with it, the same way stopping a manual import
		// does, or gallery-dl keeps running against a folder that is going away.
		runCtx, cancel := context.WithCancel(ctx)
		if !s.pinterest.startAutomatic(pinterestBoardName(boardURL), "pinterest", cancel) {
			cancel()
			return false
		}
		// Recorded before the run, not after, so a board that fails every time
		// waits its turn again instead of being retried on every pass.
		board.LastSyncAt = now.Unix()
		_ = s.app.trackBoard(board)
		result, runErr := s.runPinterestImport(runCtx, boardURL, board.Mode, true)
		cancel()
		s.pinterest.finish(result, runErr)
		if runErr != nil {
			log.Printf("pinterest-sync warning %s: %v", board.URL, runErr)
		}
		return true
	}
	return false
}

func (s *server) watchPinterestBoards(ctx context.Context) {
	timer := time.NewTimer(pinterestSyncFirstRun)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			func() {
				defer guard(func(err error) {
					log.Printf("pinterest-sync warning: %v", err)
				})
				// A board and a followed website share one downloader, so a pass
				// that already started a board leaves the websites for the next
				// one rather than queueing behind it.
				if !s.syncDueBoards(ctx, now) {
					s.syncDueWebSources(ctx, now)
				}
			}()
			timer.Reset(pinterestSyncCheck)
		}
	}
}
