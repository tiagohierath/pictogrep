package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"math"
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

var version = "0.3.4"

const (
	semanticModelKey   = "clip-vit-base-patch32-q8-v1"
	semanticVectorSize = 512
	maxCachedQueries   = 512
	embeddingMagic     = "PGE1"
)

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
	home               string
	dataDir            string
	libraryDir         string
	tagsDir            string
	boardsDir          string
	referenceDir       string
	statePath          string
	embeddingsDir      string
	embeddingStorePath string
	queryCacheDir      string

	mu         sync.RWMutex
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

func newApplication() (*application, error) {
	home := defaultHome()
	a := &application{
		home:               home,
		dataDir:            filepath.Join(home, "data"),
		libraryDir:         filepath.Join(home, "library"),
		tagsDir:            filepath.Join(home, "collections"),
		boardsDir:          filepath.Join(home, "storyboards"),
		referenceDir:       filepath.Join(home, "storyboards", "references"),
		statePath:          filepath.Join(home, "data", "library-state.json"),
		embeddingsDir:      filepath.Join(home, "data", "embeddings"),
		embeddingStorePath: filepath.Join(home, "data", "embeddings-v1.bin"),
		queryCacheDir:      filepath.Join(home, "data", "queries"),
		embeddings:         map[string]embeddingRecord{},
		queries:            map[string]queryEmbeddingRecord{},
		job:                jobState{State: "idle", Message: "Ready", UpdatedAt: time.Now().Unix()},
	}
	for _, directory := range []string{a.dataDir, a.embeddingsDir, a.queryCacheDir, a.libraryDir, a.tagsDir, a.boardsDir, a.referenceDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, err
		}
	}
	if err := a.loadLibrary(); err != nil {
		return nil, err
	}
	_ = a.loadEmbeddings()
	_ = a.loadQueryEmbeddings()
	return a, nil
}

func (a *application) loadEmbeddings() error {
	if err := a.loadEmbeddingStore(); err != nil {
		return err
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
		if json.Unmarshal(data, &stored) == nil && stored.Path != "" && len(stored.Vector) == semanticVectorSize {
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
		if len(record.Vector) != semanticVectorSize {
			return fmt.Errorf("invalid embedding size for %s", filepath.Base(path))
		}
		path = expandPath(path)
		if existing, found := a.embeddings[path]; found && existing.Mtime == record.Mtime && len(existing.Vector) == semanticVectorSize {
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

func encodeEmbedding(path string, record embeddingRecord) ([]byte, error) {
	if path == "" || len(path) > 1<<20 || len(record.Vector) != semanticVectorSize {
		return nil, fmt.Errorf("invalid image embedding")
	}
	pathBytes := []byte(path)
	data := make([]byte, 16+len(pathBytes)+semanticVectorSize*4+4)
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
		recordLength := 16 + pathLength + semanticVectorSize*4 + 4
		if pathLength < 1 || pathLength > 1<<20 || recordLength > len(data)-offset {
			break
		}
		checksumOffset := offset + recordLength - 4
		if crc32.ChecksumIEEE(data[offset:checksumOffset]) != binary.LittleEndian.Uint32(data[checksumOffset:checksumOffset+4]) {
			break
		}
		path := expandPath(string(data[offset+16 : offset+16+pathLength]))
		vector := make([]float32, semanticVectorSize)
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
		data, encodeErr := encodeEmbedding(path, records[path])
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
		data, encodeErr := encodeEmbedding(path, a.embeddings[path])
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
		liveSize += int64(16 + len(path) + semanticVectorSize*4 + 4)
	}
	if info.Size() > liveSize*2+(1<<20) {
		_ = a.writeEmbeddingStoreLocked()
	}
}

func normalizeSemanticQuery(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func (a *application) queryCachePath(query string) string {
	digest := sha256.Sum256([]byte(semanticModelKey + "\n" + query))
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
		if query != "" && query == record.Query && record.Model == semanticModelKey && len(record.Vector) == semanticVectorSize {
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
	if !found || record.Model != semanticModelKey || len(record.Vector) != semanticVectorSize {
		return nil, false
	}
	return append([]float32(nil), record.Vector...), true
}

func (a *application) updateQueryEmbedding(query string, vector []float32) error {
	query = normalizeSemanticQuery(query)
	if query == "" || len(query) > 500 {
		return fmt.Errorf("search must be between 1 and 500 characters")
	}
	if len(vector) != semanticVectorSize {
		return fmt.Errorf("invalid search vector")
	}
	record := queryEmbeddingRecord{
		Query: query, Model: semanticModelKey, UpdatedAt: time.Now().UnixNano(), Vector: append([]float32(nil), vector...),
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
	for index, path := range a.paths {
		mtime := embeddingMtime(path)
		record, found := a.embeddings[path]
		if !found || record.Mtime != mtime || len(record.Vector) != semanticVectorSize {
			items = append(items, map[string]any{
				"id": index, "name": filepath.Base(path), "url": "/image/" + strconv.Itoa(index), "path": path, "mtime": mtime,
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

func (a *application) vectorSearch(vector []float32, limit int) []searchResult {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(vector) != semanticVectorSize {
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
