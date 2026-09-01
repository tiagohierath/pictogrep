package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type folderViewPreferences struct {
	CardSize  string            `json:"cardSize"`
	Sort      string            `json:"sort"`
	Order     []string          `json:"order"`
	Favorites map[string]bool   `json:"favorites"`
	Covers    map[string]string `json:"covers"`
}

func defaultFolderViewPreferences() folderViewPreferences {
	return folderViewPreferences{
		CardSize:  "medium",
		Sort:      "custom",
		Order:     []string{},
		Favorites: map[string]bool{},
		Covers:    map[string]string{},
	}
}

func folderPreferenceKey(kind, value string) string {
	digest := sha256.Sum256([]byte(value))
	return kind + "-" + hex.EncodeToString(digest[:8])
}

func (a *application) folderViewPreferencesPath() string {
	return filepath.Join(a.dataDir, "folder-view.json")
}

func (a *application) loadFolderViewPreferencesLocked() folderViewPreferences {
	preferences := defaultFolderViewPreferences()
	data, err := os.ReadFile(a.folderViewPreferencesPath())
	if err == nil {
		_ = json.Unmarshal(data, &preferences)
	}
	if preferences.CardSize != "tiny" && preferences.CardSize != "medium" && preferences.CardSize != "huge" {
		preferences.CardSize = "medium"
	}
	if preferences.Sort != "custom" && preferences.Sort != "name" && preferences.Sort != "recent" && preferences.Sort != "size" {
		preferences.Sort = "custom"
	}
	if preferences.Order == nil {
		preferences.Order = []string{}
	}
	if preferences.Favorites == nil {
		preferences.Favorites = map[string]bool{}
	}
	if preferences.Covers == nil {
		preferences.Covers = map[string]string{}
	}
	return preferences
}

func (a *application) loadFolderViewPreferences() folderViewPreferences {
	a.folderPreferencesMu.Lock()
	defer a.folderPreferencesMu.Unlock()
	return a.loadFolderViewPreferencesLocked()
}

func (a *application) saveFolderViewPreferencesLocked(preferences folderViewPreferences) error {
	data, err := json.MarshalIndent(preferences, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(a.folderViewPreferencesPath(), append(data, '\n'), 0o600)
}

func (a *application) moveFolderPreferences(kind, oldValue, newValue string) {
	a.folderPreferencesMu.Lock()
	defer a.folderPreferencesMu.Unlock()
	preferences := a.loadFolderViewPreferencesLocked()
	oldKey := folderPreferenceKey(kind, oldValue)
	newKey := folderPreferenceKey(kind, newValue)
	if preferences.Favorites[oldKey] {
		preferences.Favorites[newKey] = true
	}
	delete(preferences.Favorites, oldKey)
	if cover := preferences.Covers[oldKey]; cover != "" {
		preferences.Covers[newKey] = cover
	}
	delete(preferences.Covers, oldKey)
	for index, key := range preferences.Order {
		if key == oldKey {
			preferences.Order[index] = newKey
		}
	}
	_ = a.saveFolderViewPreferencesLocked(preferences)
}

func (s *server) folderPaths(kind, value string) ([]string, error) {
	switch kind {
	case "tag":
		name, err := collectionName(value)
		if err != nil || name != value {
			return nil, fmt.Errorf("unknown folder")
		}
		if info, err := os.Stat(filepath.Join(s.app.tagsDir, name)); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("unknown folder")
		}
		return s.collectionImages(name), nil
	case "source":
		directory := expandPath(value)
		paths, sources, _ := s.app.snapshot()
		allowed := false
		for _, source := range sources {
			if pathInside(directory, source) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("unknown folder")
		}
		result := make([]string, 0)
		for _, path := range paths {
			if pathInside(path, directory) {
				result = append(result, path)
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown folder")
	}
}

func (s *server) saveFolderView(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Kind     string   `json:"kind"`
		Value    string   `json:"value"`
		Favorite *bool    `json:"favorite"`
		CoverID  *string  `json:"coverId"`
		Order    []string `json:"order"`
		CardSize *string  `json:"cardSize"`
		Sort     *string  `json:"sort"`
	}
	if err := decodeJSON(r, &request, 2<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	s.app.folderPreferencesMu.Lock()
	defer s.app.folderPreferencesMu.Unlock()
	preferences := s.app.loadFolderViewPreferencesLocked()
	if request.CardSize != nil {
		if *request.CardSize != "tiny" && *request.CardSize != "medium" && *request.CardSize != "huge" {
			sendError(w, 400, fmt.Errorf("invalid card size"))
			return
		}
		preferences.CardSize = *request.CardSize
	}
	if request.Sort != nil {
		if *request.Sort != "custom" && *request.Sort != "name" && *request.Sort != "recent" && *request.Sort != "size" {
			sendError(w, 400, fmt.Errorf("invalid folder sort"))
			return
		}
		preferences.Sort = *request.Sort
	}
	if request.Order != nil {
		seen := map[string]bool{}
		preferences.Order = preferences.Order[:0]
		for _, key := range request.Order {
			if key != "" && len(key) <= 80 && !seen[key] {
				seen[key] = true
				preferences.Order = append(preferences.Order, key)
			}
		}
	}
	if request.Favorite != nil || request.CoverID != nil {
		paths, err := s.folderPaths(request.Kind, request.Value)
		if err != nil {
			sendError(w, 400, err)
			return
		}
		key := folderPreferenceKey(request.Kind, request.Value)
		if request.Favorite != nil {
			if *request.Favorite {
				preferences.Favorites[key] = true
			} else {
				delete(preferences.Favorites, key)
			}
		}
		if request.CoverID != nil {
			if *request.CoverID == "" {
				delete(preferences.Covers, key)
			} else {
				coverPath, found := s.imagePathByID(*request.CoverID)
				belongs := false
				for _, path := range paths {
					if path == coverPath {
						belongs = true
						break
					}
				}
				if !found || !belongs {
					sendError(w, 400, fmt.Errorf("cover must be in this folder"))
					return
				}
				preferences.Covers[key] = *request.CoverID
			}
		}
	}
	if err := s.app.saveFolderViewPreferencesLocked(preferences); err != nil {
		sendError(w, 500, fmt.Errorf("could not save folder view"))
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "view": preferences})
}

func (s *server) exportFolder(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	value := r.URL.Query().Get("value")
	paths, err := s.folderPaths(kind, value)
	if err != nil {
		sendError(w, 404, err)
		return
	}
	sort.Strings(paths)
	name := filepath.Base(filepath.FromSlash(value))
	if safe, safeErr := safeArchiveName(name); safeErr == nil {
		name = safe
	} else {
		name = "pictogrep-folder"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, name))
	w.Header().Set("Cache-Control", "no-store")
	archive := zip.NewWriter(w)
	used := map[string]bool{}
	for _, path := range paths {
		file, openErr := os.Open(path)
		if openErr != nil {
			continue
		}
		entryName, safeErr := safeImageName(filepath.Base(path))
		if safeErr != nil {
			_ = file.Close()
			continue
		}
		originalName := entryName
		for number := 2; used[entryName]; number++ {
			extension := filepath.Ext(originalName)
			entryName = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(originalName, extension), number, extension)
		}
		used[entryName] = true
		entry, createErr := archive.Create(entryName)
		if createErr == nil {
			_, _ = io.Copy(entry, file)
		}
		_ = file.Close()
	}
	_ = archive.Close()
}

func safeArchiveName(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == ' ' {
			return r
		}
		return '-'
	}, value)
	value = strings.Trim(strings.Join(strings.Fields(value), "-"), "-_")
	if value == "" {
		return "", fmt.Errorf("invalid archive name")
	}
	if len(value) > 80 {
		value = value[:80]
	}
	return value, nil
}
