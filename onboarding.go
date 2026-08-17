package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Onboarding needs two things the rest of the application never had to offer:
// somewhere to remember that it already ran, and a way to pick a real folder on
// disk. The folder picker matters more than it looks. Every existing way of
// adding pictures copies them into the managed library, so "choose a folder"
// could not honestly promise that pictures stay where they are until there was
// a way to name a path and index it in place.

type onboardingSettings struct {
	Completed bool `json:"completed"`
}

func (a *application) onboardingSettings() onboardingSettings {
	settings := onboardingSettings{}
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return settings
	}
	var document struct {
		Onboarding onboardingSettings `json:"onboarding"`
	}
	if json.Unmarshal(data, &document) == nil {
		settings = document.Onboarding
	}
	return settings
}

func (a *application) saveOnboardingSettings(settings onboardingSettings) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document["onboarding"] = settings
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

func (s *server) saveOnboarding(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Completed bool `json:"completed"`
	}
	if err := decodeJSON(r, &request, 1<<16); err != nil {
		sendError(w, 400, err)
		return
	}
	if err := s.app.saveOnboardingSettings(onboardingSettings{Completed: request.Completed}); err != nil {
		sendError(w, 400, err)
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "completed": request.Completed})
}

// Counting every picture under a folder is what makes the picker worth using,
// but a home directory can hide an enormous tree. The walk stops early and says
// so rather than making the interface wait on a number nobody reads exactly.
const (
	browseCountLimit = 5000
	// A folder with very few pictures can still hide an enormous tree under it.
	// /nix/store is the local example, and walking it would leave the picker
	// sitting there for minutes. The count gives up and says it is approximate
	// rather than making anyone wait for a number they read as "lots".
	browseCountBudget  = 1200 * time.Millisecond
	browseVisitedLimit = 40000
)

func countImagesUnder(root string) (int, bool) {
	count := 0
	truncated := false
	visited := 0
	deadline := time.Now().Add(browseCountBudget)
	var walk func(string, int)
	walk = func(directory string, depth int) {
		if truncated || depth > 6 {
			return
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if truncated {
				return
			}
			visited++
			if visited >= browseVisitedLimit || (visited%512 == 0 && time.Now().After(deadline)) {
				truncated = true
				return
			}
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if entry.IsDir() {
				walk(filepath.Join(directory, entry.Name()), depth+1)
				continue
			}
			if imageExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
				count++
				if count >= browseCountLimit {
					truncated = true
					return
				}
			}
		}
	}
	walk(root, 0)
	return count, truncated
}

type browseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// appBrowse lists the folders inside a path so the interface can walk the disk
// without a native file dialog, which Pictogrep has no way to open from a
// browser window. Only directory names travel back, never file contents.
func (s *server) appBrowse(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		path = home
	}
	path = expandPath(path)
	if !filepath.IsAbs(path) {
		sendError(w, 400, fmt.Errorf("folder path must be absolute"))
		return
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		sendError(w, 400, fmt.Errorf("could not open %s", path))
		return
	}
	if !info.IsDir() {
		sendError(w, 400, fmt.Errorf("%s is not a folder", path))
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		sendError(w, 400, fmt.Errorf("could not read %s", path))
		return
	}
	folders := []browseEntry{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		folders = append(folders, browseEntry{Name: entry.Name(), Path: filepath.Join(path, entry.Name())})
		if len(folders) >= 500 {
			break
		}
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.ToLower(folders[i].Name) < strings.ToLower(folders[j].Name)
	})
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	count, truncated := countImagesUnder(path)
	sendJSON(w, 200, map[string]any{
		"ok": true, "path": path, "name": filepath.Base(path), "parent": parent, "home": home,
		"folders": folders, "images": count, "truncated": truncated,
	})
}

// forgetFolder stops Pictogrep reading a source folder. Adding one is easy now
// that there is a picker, so removing one has to be just as reachable, and a
// folder chosen by mistake was otherwise permanent.
func (s *server) forgetFolder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Folder string `json:"folder"`
	}
	if err := decodeJSON(r, &request, 1<<16); err != nil {
		sendError(w, 400, err)
		return
	}
	if strings.TrimSpace(request.Folder) == "" {
		sendError(w, 400, fmt.Errorf("name the folder to stop reading"))
		return
	}
	if err := s.app.forgetSourceFolder(request.Folder); err != nil {
		sendError(w, 400, err)
		return
	}
	_, sources, _ := s.app.snapshot()
	sendJSON(w, 200, map[string]any{"ok": true, "sources": sources})
}
