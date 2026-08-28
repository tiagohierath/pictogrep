package main

// storage.kv: the one piece of persistent state a plugin gets. One JSON file
// per plugin under pluginDataDir, written the same atomic way as every other
// piece of app state (see writeFileAtomically in server.go). A plugin never
// gets a path into the library's own data files; this is the whole point of
// routing storage through the core instead of letting a plugin pick its own
// file layout.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

func (a *application) pluginStorePath(id string) string {
	return filepath.Join(a.pluginDataDir, id+".json")
}

func (a *application) pluginStorageGet(id string) map[string]any {
	data, err := os.ReadFile(a.pluginStorePath(id))
	if err != nil {
		return map[string]any{}
	}
	var store map[string]any
	if json.Unmarshal(data, &store) != nil {
		return map[string]any{}
	}
	return store
}

func (a *application) pluginStorageSet(id, key string, value any) error {
	store := a.pluginStorageGet(id)
	store[key] = value
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(a.pluginStorePath(id), append(data, '\n'), 0o644)
}

// handlePluginStorage answers the storage.kv capability, called through the
// broker on behalf of whichever plugin the request names. The broker is what
// checks the calling plugin actually declared this permission; this handler
// trusts that check the same way appTags trusts the router having matched a
// route, because both run only inside this app's own process.
func (s *server) handlePluginStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, found := s.app.plugins()[id]; !found {
		sendError(w, http.StatusNotFound, fmt.Errorf("no plugin named %s is installed", id))
		return
	}
	switch r.Method {
	case http.MethodGet:
		sendJSON(w, http.StatusOK, map[string]any{"ok": true, "value": s.app.pluginStorageGet(id)})
	case http.MethodPost:
		var request struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := decodeJSON(r, &request, 1<<20); err != nil {
			sendError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.app.pluginStorageSet(id, request.Key, request.Value); err != nil {
			sendError(w, http.StatusInternalServerError, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		sendError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
	}
}
