package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// Pictogrep knew how to update itself and never once offered to. The check ran
// only when somebody opened About and pressed a button, so an installation sat
// on whatever version it was installed with until its owner went looking. This
// checks on its own and, where the installation is one Pictogrep may replace,
// installs the new version and waits for the next launch to use it.
//
// Nothing is ever installed over a package manager's copy. A Nix or system
// package is somebody else's to update, so those installations are only told
// that a newer version exists.

const (
	autoUpdateFirstCheck = 3 * time.Minute
	autoUpdateEvery      = 6 * time.Hour
)

type updateSettings struct {
	AutoUpdate bool `json:"autoUpdate"`
}

func (a *application) updateSettings() updateSettings {
	settings := updateSettings{AutoUpdate: true}
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return settings
	}
	var document struct {
		Update *struct {
			AutoUpdate *bool `json:"autoUpdate"`
		} `json:"update"`
	}
	if json.Unmarshal(data, &document) == nil && document.Update != nil && document.Update.AutoUpdate != nil {
		settings.AutoUpdate = *document.Update.AutoUpdate
	}
	return settings
}

func (a *application) saveUpdateSettings(settings updateSettings) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document["update"] = settings
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

// autoUpdater is what the last automatic check found, so the window can say so
// without checking again itself.
type autoUpdater struct {
	mu        sync.Mutex
	checkedAt time.Time
	latest    string
	available bool
	// installed is a version already written to disk and waiting for the next
	// launch. The running program keeps its own copy of the old executable, so
	// nothing changes underfoot.
	installed string
	failed    string
}

func (u *autoUpdater) snapshot() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	payload := map[string]any{"available": u.available, "latestVersion": u.latest}
	if !u.checkedAt.IsZero() {
		payload["checkedAt"] = u.checkedAt.Unix()
	}
	if u.installed != "" {
		payload["installedVersion"] = u.installed
		payload["restartRequired"] = true
	}
	if u.failed != "" {
		payload["error"] = u.failed
	}
	return payload
}

func (u *autoUpdater) record(state updateState, installed string, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checkedAt = time.Now()
	u.latest = state.LatestVersion
	u.available = state.Available
	if installed != "" {
		u.installed = installed
		// An installed update is no longer something to go and get.
		u.available = false
	}
	if err != nil {
		u.failed = err.Error()
		return
	}
	u.failed = ""
}

func (u *autoUpdater) pendingRestart() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.installed
}

// checkOnce runs one automatic check, and installs when the installation is one
// Pictogrep may replace. It returns true when it did any work, which is what
// the tests hold on to.
func (s *server) checkOnce(_ context.Context) bool {
	if !s.app.updateSettings().AutoUpdate {
		return false
	}
	// A version already downloaded is waiting for a restart. Checking again
	// would only download it a second time.
	if s.updates.pendingRestart() != "" {
		return false
	}
	state, err := s.checkUpdate()
	if err != nil {
		s.updates.record(updateState{}, "", err)
		return false
	}
	if !state.Available || state.Action != "replace" {
		// A package manager's copy, or nothing new. Either way this records
		// what it saw and installs nothing.
		s.updates.record(state, "", nil)
		return true
	}
	if err := s.applyUpdate(state); err != nil {
		s.updates.record(state, "", err)
		return true
	}
	s.updates.record(state, state.LatestVersion, nil)
	log.Printf("update installed: %s, starts with the next launch", state.LatestVersion)
	return true
}

func (s *server) watchForUpdates(ctx context.Context) {
	timer := time.NewTimer(autoUpdateFirstCheck)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			func() {
				defer guard(func(err error) { log.Printf("update warning: %v", err) })
				s.checkOnce(ctx)
			}()
			timer.Reset(autoUpdateEvery)
		}
	}
}

func (s *server) saveUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AutoUpdate *bool `json:"autoUpdate"`
	}
	if err := decodeJSON(r, &request, 1<<16); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if request.AutoUpdate != nil {
		if err := s.app.saveUpdateSettings(updateSettings{AutoUpdate: *request.AutoUpdate}); err != nil {
			sendError(w, http.StatusBadRequest, err)
			return
		}
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "autoUpdate": s.app.updateSettings().AutoUpdate})
}

// updateStatePayload is what the window reads on every refresh: the automatic
// check's last word, plus the switch that governs it.
func (s *server) updateStatePayload() map[string]any {
	payload := s.updates.snapshot()
	payload["autoUpdate"] = s.app.updateSettings().AutoUpdate
	payload["canSelfUpdate"] = currentInstallationChannel().Action == "replace"
	return payload
}
