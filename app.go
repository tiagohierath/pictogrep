package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var version = "0.11.0"

const (
	maxCachedQueries = 512
	embeddingMagic   = "PGE1"
)

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
}

type libraryState struct {
	Sources   []string `json:"sources"`
	Images    []string `json:"images"`
	UpdatedAt int64    `json:"updatedAt"`
}

type storageSettings struct {
	// Deprecated. Pictogrep always imports original bytes and only optimizes its
	// disposable browsing previews.
	OptimizeImports bool `json:"optimizeImports,omitempty"`
}

type browserSettings struct {
	ThumbnailSize string `json:"thumbnailSize"`
	ShowFilenames bool   `json:"showFilenames"`
	HomeOrder     string `json:"homeOrder"`
}

type indexingSettings struct {
	Automatic bool `json:"automatic"`
}

type libraryRefresh struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Total   int `json:"total"`
}

type jobState struct {
	State     string `json:"state"`
	Current   int    `json:"current"`
	Total     int    `json:"total"`
	Message   string `json:"message"`
	UpdatedAt int64  `json:"updatedAt"`
}

type application struct {
	home               string
	dataDir            string
	libraryDir         string
	tagsDir            string
	boardsDir          string
	referenceDir       string
	statePath          string
	configPath         string
	embeddingsDir      string
	embeddingStorePath string
	queryCacheDir      string
	canvasDir          string
	thumbnailDir       string
	pluginsDir         string
	pluginDataDir      string
	embeddingModel     embeddingModel
	usage              *usageTracker

	mu                  sync.RWMutex
	canvasMu            sync.Mutex
	folderPreferencesMu sync.Mutex
	// Assign through setPaths, never directly. See the comment there.
	paths []string
	// Bumped by every replacement of paths, so a cache built from the library
	// can tell in one integer comparison whether it is out of date.
	edits            uint64
	sources          []string
	job              jobState
	embeddings       map[string]embeddingRecord
	queries          map[string]queryEmbeddingRecord
	installedPlugins map[string]pluginManifest
}

// mtimeOf reads a path's mtime from cache when the caller supplies one, or
// stats it directly otherwise. Deciding whether an embedding is still current
// costs one stat per picture, and a handler that asks two of these questions
// about the same library pays for the same stats twice: the related-pictures
// route counts what is indexed and then ranks it, which on a large library is
// two full passes over the disk for one request. mtimeSnapshot up front and
// this in the loop makes it one.
//
// A nil cache is not a degraded mode, it is the default: every caller that
// asks only one question passes nil and stats live, which is what keeps an
// edited picture out of the very next search rather than the one after it.
// Unknown paths fall through to a live stat for the same reason, so a cache
// taken before an import still sees what the import added.
func mtimeOf(path string, cache map[string]int64) int64 {
	if cache != nil {
		if mtime, ok := cache[path]; ok {
			return mtime
		}
	}
	return embeddingMtime(path)
}

// mtimeSnapshot stats every path in the library once. Pass the result to
// missingEmbeddings/indexedEmbeddingCount/vectorSearch when calling more than
// one of them for the same logical request.
func (a *application) mtimeSnapshot() map[string]int64 {
	a.mu.RLock()
	paths := append([]string(nil), a.paths...)
	a.mu.RUnlock()
	cache := make(map[string]int64, len(paths))
	for _, path := range paths {
		cache[path] = embeddingMtime(path)
	}
	return cache
}

type embeddingRecord struct {
	Mtime  int64     `json:"mtime"`
	Vector []float32 `json:"vector"`
}

type storedEmbedding struct {
	Path string `json:"path"`
	embeddingRecord
}

type queryEmbeddingRecord struct {
	Query     string    `json:"query"`
	Model     string    `json:"model"`
	UpdatedAt int64     `json:"updatedAt"`
	Vector    []float32 `json:"vector"`
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

func defaultConfigPath() string {
	if value := os.Getenv("PICTOGREP_CONFIG"); value != "" {
		return expandPath(value)
	}
	// Keep test and portable installations self-contained when their data home
	// is explicitly overridden.
	if value := os.Getenv("PICTOGREP_HOME"); value != "" {
		return filepath.Join(expandPath(value), "config.json")
	}
	if directory, err := os.UserConfigDir(); err == nil {
		return filepath.Join(directory, "pictogrep", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "pictogrep", "config.json")
}

func newApplication() (*application, error) {
	return newApplicationWithEmbeddingModel(defaultEmbeddingModel)
}

func newApplicationWithEmbeddingModel(model embeddingModel) (*application, error) {
	if err := model.validate(); err != nil {
		return nil, err
	}
	home := defaultHome()
	a := &application{
		home:               home,
		dataDir:            filepath.Join(home, "data"),
		libraryDir:         filepath.Join(home, "library"),
		tagsDir:            filepath.Join(home, "collections"),
		boardsDir:          filepath.Join(home, "storyboards"),
		referenceDir:       filepath.Join(home, "storyboards", "references"),
		statePath:          filepath.Join(home, "data", "library-state.json"),
		configPath:         defaultConfigPath(),
		embeddingsDir:      filepath.Join(home, "data", "embeddings"),
		embeddingStorePath: filepath.Join(home, "data", model.storeFile),
		queryCacheDir:      filepath.Join(home, "data", "queries"),
		canvasDir:          filepath.Join(home, "data", "canvases"),
		thumbnailDir:       filepath.Join(home, "data", "thumbnails"),
		pluginsDir:         filepath.Join(home, "plugins"),
		pluginDataDir:      filepath.Join(home, "data", "plugins"),
		embeddingModel:     model,
		embeddings:         map[string]embeddingRecord{},
		queries:            map[string]queryEmbeddingRecord{},
		installedPlugins:   map[string]pluginManifest{},
		job:                jobState{State: "idle", Message: "Ready", UpdatedAt: time.Now().Unix()},
	}
	for _, directory := range []string{a.dataDir, a.embeddingsDir, a.queryCacheDir, a.canvasDir, a.thumbnailDir, a.libraryDir, a.tagsDir, a.boardsDir, a.referenceDir, a.pluginsDir, a.pluginDataDir, filepath.Dir(a.configPath)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, err
		}
	}
	a.reloadPlugins()
	if err := a.loadLibrary(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(a.configPath); os.IsNotExist(err) {
		if err := a.saveStorageSettings(storageSettings{}); err != nil {
			return nil, err
		}
	}
	if removed := a.removeDuplicateImportedFiles(); removed > 0 {
		_ = a.saveLibraryLocked()
	}
	_ = a.loadEmbeddings()
	_ = a.loadQueryEmbeddings()
	if tracksDailyUsage {
		tracker, err := newUsageTracker(filepath.Join(a.dataDir, "usage.json"), version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pictogrep: daily usage tracking disabled: %v\n", err)
		} else {
			a.usage = tracker
		}
	}
	return a, nil
}

func (a *application) storageSettings() storageSettings {
	// Original replacement used to be configurable. It is deliberately ignored
	// now so even an old config file cannot enable destructive imports.
	return storageSettings{}
}

func (a *application) language() string {
	if language := a.configuredLanguage(); language != "" {
		return language
	}
	return "en"
}

func (a *application) configuredLanguage() string {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return ""
	}
	var document struct {
		Language string `json:"language"`
	}
	if json.Unmarshal(data, &document) != nil || (document.Language != "en" && document.Language != "pt-BR") {
		return ""
	}
	return document.Language
}

func (a *application) saveLanguage(language string) error {
	if language != "pt-BR" {
		language = "en"
	}
	return a.writeConfigValue("language", language)
}

// theme is the interface's colour scheme, and dark is both the default and the
// empty value: a config file written before themes existed reads as dark,
// which is also what a fresh install gets. Only "light" is stored, so an
// unrecognised value degrades to the default rather than to a broken page.
func (a *application) theme() string {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return "dark"
	}
	var document struct {
		Theme string `json:"theme"`
	}
	if json.Unmarshal(data, &document) != nil || document.Theme != "light" {
		return "dark"
	}
	return "light"
}

func (a *application) saveTheme(theme string) error {
	if theme != "light" {
		theme = "dark"
	}
	return a.writeConfigValue("theme", theme)
}

// Read the config, set one key, write it back through a temp file and a
// rename, so a crash mid-write cannot leave a half-written config behind.
// Keys this build knows nothing about survive the round trip.
func (a *application) writeConfigValue(key string, value any) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document[key] = value
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

func (a *application) browserSettings() browserSettings {
	settings := browserSettings{ThumbnailSize: "medium", HomeOrder: "random"}
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return settings
	}
	var document struct {
		Browser browserSettings `json:"browser"`
	}
	if json.Unmarshal(data, &document) == nil {
		settings = document.Browser
	}
	if settings.ThumbnailSize != "small" && settings.ThumbnailSize != "large" {
		settings.ThumbnailSize = "medium"
	}
	if settings.HomeOrder != "recent" {
		settings.HomeOrder = "random"
	}
	return settings
}

func (a *application) saveBrowserSettings(settings browserSettings) error {
	if settings.ThumbnailSize != "small" && settings.ThumbnailSize != "large" {
		settings.ThumbnailSize = "medium"
	}
	if settings.HomeOrder != "recent" {
		settings.HomeOrder = "random"
	}
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document["browser"] = settings
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

func (a *application) indexingSettings() indexingSettings {
	settings := indexingSettings{Automatic: true}
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return settings
	}
	var document struct {
		Indexing *struct {
			Automatic *bool `json:"automatic"`
		} `json:"indexing"`
	}
	if json.Unmarshal(data, &document) == nil && document.Indexing != nil && document.Indexing.Automatic != nil {
		settings.Automatic = *document.Indexing.Automatic
	}
	return settings
}

func (a *application) saveIndexingSettings(settings indexingSettings) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document["indexing"] = settings
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

func (a *application) pluginEnabled(name string) bool {
	// Not a default the user can change: a build without the board importer does
	// not have one to turn on. This is also what stops the weekly board sync,
	// which asks this question before it fetches anything.
	if name == "pinterest" && !offersPinterest {
		return false
	}
	// Locked is locked: not merely hidden in the panel, but off for the routes
	// it serves and the background work it schedules too. See premium.go.
	if a.premiumLocks(name) {
		return false
	}
	// The two importers ship with release installations and are ready on first
	// launch, while remaining ordinary plugins that users can turn off.
	defaultEnabled := name == "pinterest" || name == "web"
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return defaultEnabled
	}
	var document struct {
		Plugins map[string]bool `json:"plugins"`
	}
	if json.Unmarshal(data, &document) != nil {
		return defaultEnabled
	}
	enabled, found := document.Plugins[name]
	if !found {
		return defaultEnabled
	}
	return enabled
}

func (a *application) setPluginEnabled(name string, enabled bool) error {
	if name != "wikimedia" && name != "calendar" && name != "sidebar" && name != "vim" && name != "commandPalette" && name != "pinterest" && name != "web" && name != "canvas" {
		return fmt.Errorf("unknown plugin: %s", name)
	}
	// A build with no board importer has no setting for one either, so that
	// writing the config by hand cannot bring back a panel this build does not
	// serve.
	if name == "pinterest" && !offersPinterest {
		return fmt.Errorf("unknown plugin: %s", name)
	}
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	plugins, ok := document["plugins"].(map[string]any)
	if !ok {
		plugins = map[string]any{}
	}
	plugins[name] = enabled
	document["plugins"] = plugins
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

func (a *application) saveStorageSettings(_ storageSettings) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	delete(document, "storage")
	delete(document, "optimizeImports")
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

func fileDigest(path string) ([32]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return [32]byte{}, err
	}
	var digest [32]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func (a *application) removeDuplicateImportedFiles() int {
	seen := map[[32]byte]string{}
	kept := make([]string, 0, len(a.paths))
	removed := 0
	for _, path := range a.paths {
		relative, err := filepath.Rel(a.libraryDir, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			kept = append(kept, path)
			continue
		}
		digest, err := fileDigest(path)
		if err != nil {
			kept = append(kept, path)
			continue
		}
		if original, exists := seen[digest]; exists {
			if os.Remove(path) == nil {
				removed++
				continue
			}
			_ = original
		}
		seen[digest] = path
		kept = append(kept, path)
	}
	a.setPaths(kept)
	return removed
}

func (a *application) loadEmbeddings() error {
	if err := a.loadEmbeddingStore(); err != nil {
		return err
	}
	// The JSON format predates model identities and belongs to the original
	// default model. Other models must never claim those vectors.
	if a.embeddingModel.Key != defaultEmbeddingModel.Key {
		return nil
	}
	entries, err := os.ReadDir(a.embeddingsDir)
	if err != nil {
		return err
	}
	legacyFiles := []string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.embeddingsDir, entry.Name()))
		if err != nil {
			continue
		}
		var stored storedEmbedding
		if json.Unmarshal(data, &stored) == nil && stored.Path != "" && len(stored.Vector) == a.embeddingModel.Dimensions {
			path := expandPath(stored.Path)
			stored.Mtime = upgradedEmbeddingMtime(path, stored.Mtime)
			if existing, found := a.embeddings[path]; !found || stored.Mtime >= existing.Mtime {
				a.embeddings[path] = stored.embeddingRecord
			}
			legacyFiles = append(legacyFiles, filepath.Join(a.embeddingsDir, entry.Name()))
		}
	}
	if len(legacyFiles) > 0 {
		if err := a.writeEmbeddingStoreLocked(); err != nil {
			return err
		}
		for _, path := range legacyFiles {
			_ = os.Remove(path)
		}
	}
	return nil
}

func (a *application) updateEmbeddings(records map[string]embeddingRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	pending := map[string]embeddingRecord{}
	for path, record := range records {
		if len(record.Vector) != a.embeddingModel.Dimensions {
			return fmt.Errorf("invalid embedding size for %s", filepath.Base(path))
		}
		path = expandPath(path)
		if existing, found := a.embeddings[path]; found && existing.Mtime == record.Mtime && len(existing.Vector) == a.embeddingModel.Dimensions {
			continue
		}
		pending[path] = embeddingRecord{Mtime: record.Mtime, Vector: append([]float32(nil), record.Vector...)}
	}
	if len(pending) == 0 {
		return nil
	}
	if err := a.appendEmbeddingRecordsLocked(pending); err != nil {
		return err
	}
	for path, record := range pending {
		a.embeddings[path] = record
	}
	a.compactEmbeddingStoreLocked()
	return nil
}

func upgradedEmbeddingMtime(path string, value int64) int64 {
	// v0.3.3 stored whole seconds. Upgrade matching legacy records to safe
	// millisecond precision without forcing every existing image to re-index.
	if value > 0 && value < 100_000_000_000 {
		if info, err := os.Stat(path); err == nil && info.ModTime().Unix() == value {
			return info.ModTime().UnixMilli()
		}
	}
	return value
}

func encodeEmbedding(model embeddingModel, path string, record embeddingRecord) ([]byte, error) {
	if path == "" || len(path) > 1<<20 || len(record.Vector) != model.Dimensions {
		return nil, fmt.Errorf("invalid image embedding")
	}
	pathBytes := []byte(path)
	data := make([]byte, 16+len(pathBytes)+model.Dimensions*4+4)
	copy(data[:4], embeddingMagic)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(pathBytes)))
	binary.LittleEndian.PutUint64(data[8:16], uint64(record.Mtime))
	copy(data[16:], pathBytes)
	offset := 16 + len(pathBytes)
	for _, value := range record.Vector {
		binary.LittleEndian.PutUint32(data[offset:offset+4], math.Float32bits(value))
		offset += 4
	}
	binary.LittleEndian.PutUint32(data[offset:offset+4], crc32.ChecksumIEEE(data[:offset]))
	return data, nil
}

// decodeEmbeddingRecord reads the record at the front of data: the magic, the
// path length, the mtime, the path, the vector, and a CRC32 over all of it.
func (a *application) decodeEmbeddingRecord(data []byte) (string, embeddingRecord, int, bool) {
	if len(data) < 16 || string(data[:4]) != embeddingMagic {
		return "", embeddingRecord{}, 0, false
	}
	pathLength := int(binary.LittleEndian.Uint32(data[4:8]))
	mtime := int64(binary.LittleEndian.Uint64(data[8:16]))
	length := 16 + pathLength + a.embeddingModel.Dimensions*4 + 4
	if pathLength < 1 || pathLength > 1<<20 || length > len(data) {
		return "", embeddingRecord{}, 0, false
	}
	checksumOffset := length - 4
	if crc32.ChecksumIEEE(data[:checksumOffset]) != binary.LittleEndian.Uint32(data[checksumOffset:length]) {
		return "", embeddingRecord{}, 0, false
	}
	vector := make([]float32, a.embeddingModel.Dimensions)
	offset := 16 + pathLength
	for index := range vector {
		vector[index] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
	}
	return expandPath(string(data[16 : 16+pathLength])), embeddingRecord{Mtime: mtime, Vector: vector}, length, true
}

// nextEmbeddingRecord finds where the next record could start. The magic can
// also occur inside a vector by chance, so a hit is only a candidate: the
// checksum is what decides whether a record really begins there.
func nextEmbeddingRecord(data []byte, from int) int {
	if from < 0 || from >= len(data) {
		return -1
	}
	index := bytes.Index(data[from:], []byte(embeddingMagic))
	if index < 0 {
		return -1
	}
	return from + index
}

func (a *application) loadEmbeddingStore() error {
	data, err := os.ReadFile(a.embeddingStorePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	offset, damaged := 0, false
	for offset < len(data) {
		path, record, length, ok := a.decodeEmbeddingRecord(data[offset:])
		if ok {
			a.embeddings[path] = record
			offset += length
			continue
		}
		// One damaged record used to cost every record written after it: the
		// loader stopped at the first bad byte and cut the rest of the file
		// away. Re-syncing on the next record keeps the loss to the pictures
		// actually damaged instead of an afternoon of re-indexing.
		next := nextEmbeddingRecord(data, offset+1)
		if next < 0 {
			break
		}
		damaged = true
		offset = next
	}
	if damaged {
		// The damage is still on disk, so write back only what survived.
		return a.writeEmbeddingStoreLocked()
	}
	if offset != len(data) {
		// A torn record at the end is an interrupted append, not corruption.
		return os.Truncate(a.embeddingStorePath, int64(offset))
	}
	return nil
}

func (a *application) appendEmbeddingRecordsLocked(records map[string]embeddingRecord) error {
	file, err := os.OpenFile(a.embeddingStorePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(records))
	for path := range records {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		data, encodeErr := encodeEmbedding(a.embeddingModel, path, records[path])
		if encodeErr != nil {
			_ = file.Close()
			return encodeErr
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (a *application) writeEmbeddingStoreLocked() error {
	tmp := a.embeddingStorePath + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(a.embeddings))
	for path := range a.embeddings {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		data, encodeErr := encodeEmbedding(a.embeddingModel, path, a.embeddings[path])
		if encodeErr != nil {
			_ = file.Close()
			_ = os.Remove(tmp)
			return encodeErr
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, a.embeddingStorePath)
}

func (a *application) compactEmbeddingStoreLocked() {
	info, err := os.Stat(a.embeddingStorePath)
	if err != nil {
		return
	}
	liveSize := int64(0)
	for path := range a.embeddings {
		liveSize += int64(16 + len(path) + a.embeddingModel.Dimensions*4 + 4)
	}
	if info.Size() > liveSize*2+(1<<20) {
		_ = a.writeEmbeddingStoreLocked()
	}
}

func normalizeSemanticQuery(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func (a *application) queryCachePath(query string) string {
	digest := sha256.Sum256([]byte(a.embeddingModel.Key + "\n" + query))
	return filepath.Join(a.queryCacheDir, fmt.Sprintf("%x.json", digest[:16]))
}

func (a *application) loadQueryEmbeddings() error {
	entries, err := os.ReadDir(a.queryCacheDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.queryCacheDir, entry.Name()))
		if err != nil {
			continue
		}
		var record queryEmbeddingRecord
		if json.Unmarshal(data, &record) != nil {
			continue
		}
		query := normalizeSemanticQuery(record.Query)
		if query != "" && query == record.Query && record.Model == a.embeddingModel.Key && len(record.Vector) == a.embeddingModel.Dimensions {
			a.queries[query] = record
		}
	}
	a.pruneQueryEmbeddingsLocked()
	return nil
}

func (a *application) queryEmbedding(query string) ([]float32, bool) {
	query = normalizeSemanticQuery(query)
	a.mu.RLock()
	record, found := a.queries[query]
	a.mu.RUnlock()
	if !found || record.Model != a.embeddingModel.Key || len(record.Vector) != a.embeddingModel.Dimensions {
		return nil, false
	}
	return append([]float32(nil), record.Vector...), true
}

func (a *application) updateQueryEmbedding(query string, vector []float32) error {
	query = normalizeSemanticQuery(query)
	if query == "" || len(query) > 500 {
		return fmt.Errorf("search must be between 1 and 500 characters")
	}
	if len(vector) != a.embeddingModel.Dimensions {
		return fmt.Errorf("invalid search vector")
	}
	record := queryEmbeddingRecord{
		Query: query, Model: a.embeddingModel.Key, UpdatedAt: time.Now().UnixNano(), Vector: append([]float32(nil), vector...),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	target := a.queryCachePath(query)
	tmp := target + ".tmp"
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	a.queries[query] = record
	a.pruneQueryEmbeddingsLocked()
	return nil
}

func (a *application) pruneQueryEmbeddingsLocked() {
	for len(a.queries) > maxCachedQueries {
		oldestQuery := ""
		oldestTime := int64(1<<63 - 1)
		for query, record := range a.queries {
			if record.UpdatedAt < oldestTime {
				oldestQuery = query
				oldestTime = record.UpdatedAt
			}
		}
		if oldestQuery == "" {
			return
		}
		delete(a.queries, oldestQuery)
		_ = os.Remove(a.queryCachePath(oldestQuery))
	}
}

// Preparing a picture reads a preview of it, not the original. The model works
// from a 224 pixel square, so handing it a 24 megapixel photograph spends the
// whole decode on pixels that are thrown away before anything is learned. 512
// keeps the short edge above 224 for anything up to a 2.3:1 frame, and a format
// the preview encoder cannot read, webp today, is still served whole by the
// same route.
//
// The original travels with it because a picture whose dimensions are unsafe to
// decode has no preview at all, and refusing to prepare it would quietly leave
// it out of every search.
const embeddingPreviewSize = "512"

func embeddingPreviewURL(id string) string {
	return "/thumbnail/" + id + "?size=" + embeddingPreviewSize
}

func (a *application) missingEmbeddings(mtimeCache map[string]int64) []map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	items := []map[string]any{}
	for _, path := range a.paths {
		mtime := mtimeOf(path, mtimeCache)
		record, found := a.embeddings[path]
		if !found || record.Mtime != mtime || len(record.Vector) != a.embeddingModel.Dimensions {
			id := stableImageID(path)
			items = append(items, map[string]any{
				"id": id, "name": filepath.Base(path), "url": embeddingPreviewURL(id),
				"originalUrl": "/image/" + id, "path": path, "mtime": mtime,
			})
		}
	}
	return items
}

func embeddingMtime(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().UnixMilli()
	}
	return 0
}

func (a *application) imageEmbedding(path string) ([]float32, bool) {
	path = expandPath(path)
	a.mu.RLock()
	record, found := a.embeddings[path]
	a.mu.RUnlock()
	if !found || record.Mtime != embeddingMtime(path) || len(record.Vector) != a.embeddingModel.Dimensions {
		return nil, false
	}
	return append([]float32(nil), record.Vector...), true
}

func (a *application) indexedEmbeddingCount(mtimeCache map[string]int64) int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	count := 0
	for _, path := range a.paths {
		record, found := a.embeddings[path]
		if found && record.Mtime == mtimeOf(path, mtimeCache) && len(record.Vector) == a.embeddingModel.Dimensions {
			count++
		}
	}
	return count
}

func (a *application) vectorSearch(vector []float32, limit int, mtimeCache map[string]int64) []searchResult {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(vector) != a.embeddingModel.Dimensions {
		return nil
	}
	results := make([]searchResult, 0, len(a.embeddings))
	for _, path := range a.paths {
		record, found := a.embeddings[path]
		if !found || record.Mtime != mtimeOf(path, mtimeCache) || len(record.Vector) != len(vector) {
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
	state.Images = safeImagePaths(existingUniquePaths(state.Images))
	a.setPaths(state.Images)
	a.sources = existingUniqueDirectories(state.Sources)
	if len(a.paths) == 0 {
		paths, _ := scanImages(a.libraryDir)
		a.setPaths(paths)
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

func safeImagePaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if validImageFile(path) {
			result = append(result, path)
		}
	}
	return result
}

func existingUniqueDirectories(paths []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(paths))
	for _, value := range paths {
		path := expandPath(value)
		if seen[path] {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
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
		if imageExtensions[strings.ToLower(filepath.Ext(root))] && validImageFile(root) {
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
		if imageExtensions[strings.ToLower(filepath.Ext(path))] && validImageFile(path) {
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

// setPaths replaces the library. Everything derived from the list of pictures
// is invalidated by the counter it bumps, so assign a.paths through here and
// nowhere else: a direct assignment leaves the id lookup in server.go pointing
// at pictures that have been removed, which reaches the user as a thumbnail
// for the wrong file. The caller holds a.mu.
func (a *application) setPaths(paths []string) {
	a.paths = paths
	a.edits++
}

// libraryVersion is how a cache asks "is what I built still true" without
// copying the whole library to find out. Cheap enough to call on every request
// for a thumbnail, which is what calls it.
func (a *application) libraryVersion() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.edits
}

func (a *application) indexing() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.job.State == "running"
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
	a.setPaths(append(a.paths, path))
	_ = a.saveLibraryLocked()
	return len(a.paths) - 1
}

// addPaths adds a whole import at once. Adding them one at a time rewrote the
// entire library state per picture, so a two thousand image board wrote that
// file two thousand times, each write longer than the one before it.
func (a *application) addPaths(paths []string) int {
	if len(paths) == 0 {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	known := make(map[string]bool, len(a.paths))
	for _, existing := range a.paths {
		known[existing] = true
	}
	added := 0
	for _, path := range paths {
		path = expandPath(path)
		if known[path] {
			continue
		}
		known[path] = true
		a.setPaths(append(a.paths, path))
		added++
	}
	if added > 0 {
		_ = a.saveLibraryLocked()
	}
	return added
}

func (a *application) removePath(path string) error {
	path = expandPath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := make([]string, 0, len(a.paths))
	for _, existing := range a.paths {
		if existing != path {
			kept = append(kept, existing)
		}
	}
	a.setPaths(kept)
	_, hadEmbedding := a.embeddings[path]
	delete(a.embeddings, path)
	if err := a.saveLibraryLocked(); err != nil {
		return err
	}
	if hadEmbedding {
		return a.writeEmbeddingStoreLocked()
	}
	return nil
}

func (a *application) replaceLibrary(sources, paths []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sources = uniqueStrings(sources)
	a.setPaths(existingUniquePaths(paths))
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

// refreshLibrary updates only file membership metadata. Existing image
// embeddings are left alone, so a later background pass processes only paths
// that are new or whose modification time changed.
func (a *application) refreshLibrary(root string) (libraryRefresh, error) {
	before, sources, _ := a.snapshot()
	roots := []string{a.libraryDir}
	if root != "" {
		root = expandPath(root)
		allowed := pathInside(root, a.libraryDir)
		for _, source := range sources {
			allowed = allowed || pathInside(root, source)
		}
		if !allowed {
			return libraryRefresh{}, fmt.Errorf("folder is outside the indexed library")
		}
		roots = []string{root}
	} else {
		roots = uniqueStrings(append(roots, sources...))
	}

	discovered := map[string]bool{}
	for _, scanRoot := range roots {
		paths, err := scanImages(scanRoot)
		if err != nil {
			return libraryRefresh{}, err
		}
		for _, path := range paths {
			discovered[expandPath(path)] = true
		}
	}
	beforeSet := make(map[string]bool, len(before))
	for _, path := range before {
		beforeSet[path] = true
	}
	covered := func(path string) bool {
		for _, scanRoot := range roots {
			if pathInside(path, scanRoot) {
				return true
			}
		}
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	paths := make([]string, 0, len(a.paths)+len(discovered))
	kept := map[string]bool{}
	removedEmbedding := false
	for _, path := range a.paths {
		// Preserve paths outside this refresh and imports that arrived while the
		// directory walk was in progress.
		keep := !covered(path) || discovered[path] || !beforeSet[path]
		if keep && !kept[path] {
			paths = append(paths, path)
			kept[path] = true
			continue
		}
		if _, found := a.embeddings[path]; found {
			delete(a.embeddings, path)
			removedEmbedding = true
		}
	}
	for path := range discovered {
		if !kept[path] {
			paths = append(paths, path)
			kept[path] = true
		}
	}
	sort.Strings(paths)
	a.setPaths(paths)
	if err := a.saveLibraryLocked(); err != nil {
		return libraryRefresh{}, err
	}
	if removedEmbedding {
		if err := a.writeEmbeddingStoreLocked(); err != nil {
			return libraryRefresh{}, err
		}
	}
	result := libraryRefresh{Total: len(paths)}
	for path := range kept {
		if !beforeSet[path] {
			result.Added++
		}
	}
	for _, path := range before {
		if !kept[path] {
			result.Removed++
		}
	}
	return result, nil
}

func (a *application) resetEmbeddings() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	removed := len(a.embeddings)
	a.embeddings = map[string]embeddingRecord{}
	if err := a.writeEmbeddingStoreLocked(); err != nil {
		return 0, err
	}
	return removed, nil
}

func (a *application) indexFolders(requested []string) error {
	_, remembered, _ := a.snapshot()
	folders := uniqueStrings(append(remembered, requested...))
	if len(folders) == 0 {
		return errors.New("add an image folder first")
	}
	return a.indexFolderSet(folders)
}

// forgetSourceFolder stops reading a folder. Indexing only ever unions the
// folders it knows with the ones it is handed, so without this the list could
// only grow, and a folder added by accident stayed for good.
func (a *application) forgetSourceFolder(folder string) error {
	folder = expandPath(folder)
	_, remembered, _ := a.snapshot()
	kept := make([]string, 0, len(remembered))
	found := false
	for _, source := range remembered {
		if source == folder {
			found = true
			continue
		}
		kept = append(kept, source)
	}
	if !found {
		return fmt.Errorf("Pictogrep is not reading that folder")
	}
	// The pictures themselves are untouched. Only the record of where to look
	// goes away, so nothing is deleted from anybody's disk.
	return a.indexFolderSet(kept)
}

// indexFolderSet rebuilds the library from exactly these folders. An empty set
// is allowed and leaves the managed library on its own, which is what removing
// the last folder should do rather than failing.
func (a *application) indexFolderSet(folders []string) error {
	folders = uniqueStrings(folders)
	// Managed imports are always part of the library, even when this operation
	// refreshes one or more external source folders. Otherwise replacing the
	// catalog below would silently orphan every image imported into libraryDir.
	scanFolders := append([]string{a.libraryDir}, folders...)
	scanFolders = uniqueStrings(scanFolders)
	all := []string{}
	for _, folder := range scanFolders {
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
	all = deduplicatePaths(existingUniquePaths(all))
	if err := a.replaceLibrary(folders, all); err != nil {
		return err
	}
	return nil
}

// deduplicatePaths keeps one indexed entry for each exact file content. It does
// not remove files outside Pictogrep's managed library directory.
func deduplicatePaths(paths []string) []string {
	seen := map[[32]byte]bool{}
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		digest, err := fileDigest(path)
		if err != nil {
			result = append(result, path)
			continue
		}
		if seen[digest] {
			continue
		}
		seen[digest] = true
		result = append(result, path)
	}
	return result
}

type searchResult struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

func (a *application) search(query string, limit int) ([]searchResult, bool, error) {
	paths, sources, _ := a.snapshot()
	words := strings.Fields(strings.ToLower(query))
	results := []searchResult{}
	for _, path := range paths {
		name := searchableImageText(path, sources)
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

func searchableImageText(path string, sources []string) string {
	text := filepath.Base(path)
	for _, source := range sources {
		relative, err := filepath.Rel(source, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			text = relative
			break
		}
	}
	text = strings.NewReplacer("_", " ", "-", " ", ".", " ", string(filepath.Separator), " ").Replace(text)
	return strings.ToLower(text)
}

func shuffled(paths []string) []string {
	result := append([]string(nil), paths...)
	rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})
	return result
}
