package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var version = "0.3.1"

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
}

type libraryState struct {
	Sources   []string `json:"sources"`
	Images    []string `json:"images"`
	UpdatedAt int64    `json:"updatedAt"`
}

type jobState struct {
	State     string `json:"state"`
	Current   int    `json:"current"`
	Total     int    `json:"total"`
	Message   string `json:"message"`
	UpdatedAt int64  `json:"updatedAt"`
}

type application struct {
	home          string
	dataDir       string
	libraryDir    string
	tagsDir       string
	boardsDir     string
	referenceDir  string
	statePath     string
	embeddingsDir string

	mu         sync.RWMutex
	paths      []string
	sources    []string
	job        jobState
	embeddings map[string]embeddingRecord
}

type embeddingRecord struct {
	Mtime  int64     `json:"mtime"`
	Vector []float32 `json:"vector"`
}

type storedEmbedding struct {
	Path string `json:"path"`
	embeddingRecord
}

func defaultHome() string {
	if value := os.Getenv("PICTOGREP_HOME"); value != "" {
		return expandPath(value)
	}
	if runtime.GOOS == "windows" {
		if value := os.Getenv("LOCALAPPDATA"); value != "" {
			return filepath.Join(value, "Pictogrep")
		}
	}
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		return filepath.Join(expandPath(value), "pictogrep")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "pictogrep")
}

func expandPath(value string) string {
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, _ := os.UserHomeDir()
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	abs, err := filepath.Abs(value)
	if err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(value)
}

func newApplication() (*application, error) {
	home := defaultHome()
	a := &application{
		home:          home,
		dataDir:       filepath.Join(home, "data"),
		libraryDir:    filepath.Join(home, "library"),
		tagsDir:       filepath.Join(home, "collections"),
		boardsDir:     filepath.Join(home, "storyboards"),
		referenceDir:  filepath.Join(home, "storyboards", "references"),
		statePath:     filepath.Join(home, "data", "library-state.json"),
		embeddingsDir: filepath.Join(home, "data", "embeddings"),
		embeddings:    map[string]embeddingRecord{},
		job:           jobState{State: "idle", Message: "Ready", UpdatedAt: time.Now().Unix()},
	}
	for _, directory := range []string{a.dataDir, a.embeddingsDir, a.libraryDir, a.tagsDir, a.boardsDir, a.referenceDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, err
		}
	}
	if err := a.loadLibrary(); err != nil {
		return nil, err
	}
	_ = a.loadEmbeddings()
	return a, nil
}

func (a *application) loadEmbeddings() error {
	entries, err := os.ReadDir(a.embeddingsDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.embeddingsDir, entry.Name()))
		if err != nil {
			continue
		}
		var stored storedEmbedding
		if json.Unmarshal(data, &stored) == nil && stored.Path != "" && len(stored.Vector) == 512 {
			a.embeddings[expandPath(stored.Path)] = stored.embeddingRecord
		}
	}
	return nil
}

func (a *application) updateEmbeddings(records map[string]embeddingRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for path, record := range records {
		if len(record.Vector) != 512 {
			return fmt.Errorf("invalid embedding size for %s", filepath.Base(path))
		}
		path = expandPath(path)
		stored := storedEmbedding{Path: path, embeddingRecord: record}
		data, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(path))
		target := filepath.Join(a.embeddingsDir, fmt.Sprintf("%x.json", digest[:16]))
		tmp := target + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, target); err != nil {
			return err
		}
		a.embeddings[path] = record
	}
	return nil
}

func (a *application) missingEmbeddings() []map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	items := []map[string]any{}
	for index, path := range a.paths {
		mtime := embeddingMtime(path)
		record, found := a.embeddings[path]
		if !found || record.Mtime != mtime || len(record.Vector) != 512 {
			items = append(items, map[string]any{
				"id": index, "name": filepath.Base(path), "url": "/image/" + strconv.Itoa(index), "path": path, "mtime": mtime,
			})
		}
	}
	return items
}

func embeddingMtime(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().Unix()
	}
	return 0
}

func (a *application) vectorSearch(vector []float32, limit int) []searchResult {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(vector) != 512 {
		return nil
	}
	results := make([]searchResult, 0, len(a.embeddings))
	for _, path := range a.paths {
		record, found := a.embeddings[path]
		if !found || len(record.Vector) != len(vector) {
			continue
		}
		var score float64
		for index, value := range vector {
			score += float64(value * record.Vector[index])
		}
		results = append(results, searchResult{Path: path, Score: score})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func (a *application) loadLibrary() error {
	state := libraryState{}
	data, err := os.ReadFile(a.statePath)
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("read library state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(state.Images) == 0 {
		legacy := filepath.Join(a.dataDir, "metadata.json")
		if data, err := os.ReadFile(legacy); err == nil {
			_ = json.Unmarshal(data, &state.Images)
		}
	}
	state.Images = existingUniquePaths(state.Images)
	a.paths = state.Images
	a.sources = existingUniquePaths(state.Sources)
	if len(a.paths) == 0 {
		paths, _ := scanImages(a.libraryDir)
		a.paths = paths
	}
	return nil
}

func (a *application) saveLibraryLocked() error {
	state := libraryState{Sources: a.sources, Images: a.paths, UpdatedAt: time.Now().Unix()}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := a.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.statePath)
}

func existingUniquePaths(paths []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(paths))
	for _, value := range paths {
		path := expandPath(value)
		if seen[path] {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

func scanImages(root string) ([]string, error) {
	root = expandPath(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if imageExtensions[strings.ToLower(filepath.Ext(root))] {
			return []string{root}, nil
		}
		return nil, nil
	}
	var paths []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".venv", "venv", "node_modules", "data", "storyboards", "collections":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if imageExtensions[strings.ToLower(filepath.Ext(path))] {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func (a *application) snapshot() (paths, sources []string, job jobState) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]string(nil), a.paths...), append([]string(nil), a.sources...), a.job
}

func (a *application) addPath(path string) int {
	path = expandPath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	for index, existing := range a.paths {
		if existing == path {
			return index
		}
	}
	a.paths = append(a.paths, path)
	_ = a.saveLibraryLocked()
	return len(a.paths) - 1
}

func (a *application) replaceLibrary(sources, paths []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sources = uniqueStrings(sources)
	a.paths = existingUniquePaths(paths)
	return a.saveLibraryLocked()
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = expandPath(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func (a *application) setJob(state string, current, total int, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.job = jobState{State: state, Current: current, Total: total, Message: message, UpdatedAt: time.Now().Unix()}
}

func (a *application) indexFolders(requested []string) error {
	_, remembered, _ := a.snapshot()
	folders := uniqueStrings(append(remembered, requested...))
	if len(folders) == 0 {
		return errors.New("add an image folder first")
	}
	all := []string{}
	for _, folder := range folders {
		info, err := os.Stat(folder)
		if err != nil {
			return fmt.Errorf("folder does not exist: %s", folder)
		}
		if !info.IsDir() {
			return fmt.Errorf("not a folder: %s", folder)
		}
		paths, err := scanImages(folder)
		if err != nil {
			return err
		}
		all = append(all, paths...)
	}
	all = existingUniquePaths(all)
	if err := a.replaceLibrary(folders, all); err != nil {
		return err
	}
	return nil
}

type searchResult struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

func (a *application) search(query string, limit int) ([]searchResult, bool, error) {
	paths, _, _ := a.snapshot()
	words := strings.Fields(strings.ToLower(query))
	results := []searchResult{}
	for _, path := range paths {
		name := strings.ToLower(strings.ReplaceAll(filepath.Base(path), "_", " "))
		matches := 0
		for _, word := range words {
			if strings.Contains(name, word) {
				matches++
			}
		}
		if matches > 0 {
			results = append(results, searchResult{Path: path, Score: float64(matches) / float64(len(words))})
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > limit {
		results = results[:limit]
	}
	return results, false, nil
}

func shuffled(paths []string) []string {
	result := append([]string(nil), paths...)
	rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})
	return result
}
