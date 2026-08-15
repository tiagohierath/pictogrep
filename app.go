package main

import (
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

var version = "0.7.4"

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
	embeddingModel     embeddingModel

	mu         sync.RWMutex
	canvasMu   sync.Mutex
	paths      []string
	sources    []string
	job        jobState
	embeddings map[string]embeddingRecord
	queries    map[string]queryEmbeddingRecord
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
		embeddingModel:     model,
		embeddings:         map[string]embeddingRecord{},
		queries:            map[string]queryEmbeddingRecord{},
		job:                jobState{State: "idle", Message: "Ready", UpdatedAt: time.Now().Unix()},
	}
	for _, directory := range []string{a.dataDir, a.embeddingsDir, a.queryCacheDir, a.canvasDir, a.thumbnailDir, a.libraryDir, a.tagsDir, a.boardsDir, a.referenceDir, filepath.Dir(a.configPath)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, err
		}
	}
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
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document["language"] = language
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
	// Pinterest import ships with release installations and is ready on first
	// launch, while remaining an ordinary plugin that users can turn off.
	defaultEnabled := name == "pinterest"
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
	if name != "wikimedia" && name != "calendar" && name != "sidebar" && name != "vim" && name != "commandPalette" && name != "pinterest" {
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
	a.paths = kept
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

func (a *application) loadEmbeddingStore() error {
	data, err := os.ReadFile(a.embeddingStorePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	offset := 0
	for offset < len(data) {
		start := offset
		if len(data)-offset < 16 || string(data[offset:offset+4]) != embeddingMagic {
			break
		}
		pathLength := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		mtime := int64(binary.LittleEndian.Uint64(data[offset+8 : offset+16]))
		recordLength := 16 + pathLength + a.embeddingModel.Dimensions*4 + 4
		if pathLength < 1 || pathLength > 1<<20 || recordLength > len(data)-offset {
			break
		}
		checksumOffset := offset + recordLength - 4
		if crc32.ChecksumIEEE(data[offset:checksumOffset]) != binary.LittleEndian.Uint32(data[checksumOffset:checksumOffset+4]) {
			break
		}
		path := expandPath(string(data[offset+16 : offset+16+pathLength]))
		vector := make([]float32, a.embeddingModel.Dimensions)
		vectorOffset := offset + 16 + pathLength
		for index := range vector {
			vector[index] = math.Float32frombits(binary.LittleEndian.Uint32(data[vectorOffset : vectorOffset+4]))
			vectorOffset += 4
		}
		a.embeddings[path] = embeddingRecord{Mtime: mtime, Vector: vector}
		offset = start + recordLength
	}
	if offset != len(data) {
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

func (a *application) missingEmbeddings() []map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	items := []map[string]any{}
	for _, path := range a.paths {
		mtime := embeddingMtime(path)
		record, found := a.embeddings[path]
		if !found || record.Mtime != mtime || len(record.Vector) != a.embeddingModel.Dimensions {
			id := stableImageID(path)
			items = append(items, map[string]any{
				"id": id, "name": filepath.Base(path), "url": "/image/" + id, "path": path, "mtime": mtime,
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

func (a *application) indexedEmbeddingCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	count := 0
	for _, path := range a.paths {
		record, found := a.embeddings[path]
		if found && record.Mtime == embeddingMtime(path) && len(record.Vector) == a.embeddingModel.Dimensions {
			count++
		}
	}
	return count
}

func (a *application) vectorSearch(vector []float32, limit int) []searchResult {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(vector) != a.embeddingModel.Dimensions {
		return nil
	}
	results := make([]searchResult, 0, len(a.embeddings))
	for _, path := range a.paths {
		record, found := a.embeddings[path]
		if !found || record.Mtime != embeddingMtime(path) || len(record.Vector) != len(vector) {
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
	a.paths = state.Images
	a.sources = existingUniqueDirectories(state.Sources)
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
	a.paths = append(a.paths, path)
	_ = a.saveLibraryLocked()
	return len(a.paths) - 1
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
	a.paths = kept
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
	a.paths = paths
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
