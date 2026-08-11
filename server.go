package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxUploadBytes = 100 << 20

type server struct {
	app      *application
	practice []byte
}

type imageRecord struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Path   string   `json:"path,omitempty"`
	Mtime  int64    `json:"mtime"`
	URL    string   `json:"url"`
	Tags   []string `json:"tags,omitempty"`
	Width  int      `json:"width,omitempty"`
	Height int      `json:"height,omitempty"`
	Score  *float64 `json:"score,omitempty"`
}

func newServer(app *application) (*server, error) {
	practice, err := storyboardHTML()
	if err != nil {
		return nil, err
	}
	return &server{app: app, practice: practice}, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /practice", s.practicePage)
	mux.HandleFunc("GET /assets/{name}", s.asset)
	mux.HandleFunc("GET /api/app/state", s.appState)
	mux.HandleFunc("GET /api/app/images", s.appImages)
	mux.HandleFunc("GET /api/app/folders", s.appFolders)
	mux.HandleFunc("GET /api/app/search", s.appSearch)
	mux.HandleFunc("GET /api/app/boards", s.appBoards)
	mux.HandleFunc("POST /api/app/index", s.appIndex)
	mux.HandleFunc("POST /api/app/upload", s.appUpload)
	mux.HandleFunc("POST /api/app/tags", s.appTags)
	mux.HandleFunc("GET /api/app/ai", s.aiState)
	mux.HandleFunc("POST /api/app/ai/embeddings", s.aiEmbeddings)
	mux.HandleFunc("POST /api/app/ai/search", s.aiSearch)
	mux.HandleFunc("GET /image/{id}", s.image)
	mux.HandleFunc("GET /board/{name}", s.board)
	mux.HandleFunc("GET /api/images", s.practiceImages)
	mux.HandleFunc("GET /api/collections", s.practiceCollections)
	mux.HandleFunc("GET /api/search", s.practiceSearch)
	mux.HandleFunc("GET /api/references", s.references)
	mux.HandleFunc("POST /api/references", s.addReference)
	mux.HandleFunc("DELETE /api/references", s.deleteReference)
	mux.HandleFunc("GET /reference/{name}", s.reference)
	mux.HandleFunc("POST /api/save", s.saveBoard)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func sendError(w http.ResponseWriter, status int, err error) {
	sendJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
}

func (s *server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := embeddedFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	serveBytes(w, r, "index.html", "text/html; charset=utf-8", data)
}

func (s *server) practicePage(w http.ResponseWriter, r *http.Request) {
	serveBytes(w, r, "practice.html", "text/html; charset=utf-8", s.practice)
}

func (s *server) asset(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	path := "web/" + name
	if name == "pictogrep.png" {
		path = "assets/pictogrep.png"
	}
	data, err := embeddedFiles.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	serveBytes(w, r, name, contentType, data)
}

func serveBytes(w http.ResponseWriter, r *http.Request, name, contentType string, data []byte) {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(data)))
}

func (s *server) imageRecord(path string, score *float64) imageRecord {
	paths, _, _ := s.app.snapshot()
	id := -1
	for index, candidate := range paths {
		if candidate == path {
			id = index
			break
		}
	}
	if id < 0 {
		id = s.app.addPath(path)
	}
	info, _ := os.Stat(path)
	record := imageRecord{ID: id, Name: filepath.Base(path), Path: path, URL: "/image/" + strconv.Itoa(id), Tags: s.tagsForImage(path), Score: score}
	if info != nil {
		record.Mtime = info.ModTime().Unix()
	}
	if file, err := os.Open(path); err == nil {
		if config, _, err := image.DecodeConfig(file); err == nil {
			record.Width, record.Height = config.Width, config.Height
		}
		_ = file.Close()
	}
	return record
}

func (s *server) appState(w http.ResponseWriter, _ *http.Request) {
	paths, sources, job := s.app.snapshot()
	tags := []map[string]any{}
	for _, name := range s.collectionNames() {
		tags = append(tags, map[string]any{"name": name, "count": len(s.collectionImages(name))})
	}
	index := any(nil)
	if len(paths) > 0 {
		index = map[string]any{"count": len(paths), "sources": sources, "due": false, "maintenance_due": false, "duplicates": 0}
	}
	sendJSON(w, 200, map[string]any{
		"ok": true, "version": version, "model": "ViT-B-32", "pretrained": "laion2b_s34b_b79k",
		"index": index, "indexJob": job, "sources": sources, "tags": tags,
		"boards": len(s.boardRecords()), "aiAvailable": true,
		"paths":  map[string]string{"home": s.app.home, "library": s.app.libraryDir, "boards": s.app.boardsDir, "tags": s.app.tagsDir},
		"viewer": "Browser",
	})
}

func (s *server) aiState(w http.ResponseWriter, _ *http.Request) {
	paths, _, _ := s.app.snapshot()
	missing := s.app.missingEmbeddings()
	sendJSON(w, 200, map[string]any{
		"ok": true, "ready": len(paths) > 0 && len(missing) == 0,
		"indexed": len(paths) - len(missing), "total": len(paths), "missing": missing,
	})
}

func (s *server) aiEmbeddings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Items []struct {
			Path   string    `json:"path"`
			Mtime  int64     `json:"mtime"`
			Vector []float32 `json:"vector"`
		} `json:"items"`
	}
	if err := decodeJSON(r, &request, 16<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	paths, _, _ := s.app.snapshot()
	allowed := map[string]bool{}
	for _, path := range paths {
		allowed[path] = true
	}
	records := map[string]embeddingRecord{}
	for _, item := range request.Items {
		path := expandPath(item.Path)
		if !allowed[path] {
			sendError(w, 400, fmt.Errorf("image is not in the library"))
			return
		}
		records[path] = embeddingRecord{Mtime: item.Mtime, Vector: item.Vector}
	}
	if err := s.app.updateEmbeddings(records); err != nil {
		sendError(w, 400, err)
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "saved": len(records)})
}

func (s *server) aiSearch(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Vector []float32 `json:"vector"`
		Limit  int       `json:"limit"`
		Tag    string    `json:"tag"`
		Source string    `json:"source"`
	}
	if err := decodeJSON(r, &request, 4<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	if len(request.Vector) != 512 {
		sendError(w, 400, fmt.Errorf("invalid search vector"))
		return
	}
	if request.Limit < 1 || request.Limit > 200 {
		request.Limit = 120
	}
	allowed, err := s.filteredPaths(request.Tag, request.Source)
	if err != nil {
		sendError(w, 400, err)
		return
	}
	allow := map[string]bool{}
	for _, path := range allowed {
		allow[path] = true
	}
	results := s.app.vectorSearch(request.Vector, len(allowed))
	images := []imageRecord{}
	for _, result := range results {
		if !allow[result.Path] {
			continue
		}
		score := result.Score
		images = append(images, s.imageRecord(result.Path, &score))
		if len(images) == request.Limit {
			break
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "images": images})
}

func boundedInt(value string, fallback, minimum, maximum int) int {
	number, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if number < minimum {
		return minimum
	}
	if number > maximum {
		return maximum
	}
	return number
}

func pathInside(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *server) filteredPaths(tag, source string) ([]string, error) {
	paths, _, _ := s.app.snapshot()
	if tag != "" {
		if !validCollectionName(tag) {
			return nil, fmt.Errorf("unknown folder: %s", tag)
		}
		paths = s.collectionImages(tag)
	}
	if source != "" {
		source = expandPath(source)
		filtered := paths[:0]
		for _, path := range paths {
			if pathInside(path, source) {
				filtered = append(filtered, path)
			}
		}
		paths = filtered
	}
	return paths, nil
}

func (s *server) appImages(w http.ResponseWriter, r *http.Request) {
	paths, err := s.filteredPaths(r.URL.Query().Get("tag"), r.URL.Query().Get("source"))
	if err != nil {
		sendError(w, 400, err)
		return
	}
	if r.URL.Query().Get("mode") == "recent" {
		sort.SliceStable(paths, func(i, j int) bool { return fileMtime(paths[i]) > fileMtime(paths[j]) })
	}
	total := len(paths)
	count := boundedInt(r.URL.Query().Get("count"), 120, 1, 500)
	if len(paths) > count {
		paths = paths[:count]
	}
	records := make([]imageRecord, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			records = append(records, s.imageRecord(path, nil))
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "images": records, "total": total})
}

func fileMtime(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().UnixNano()
	}
	return 0
}

func (s *server) appSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := boundedInt(r.URL.Query().Get("limit"), 80, 1, 200)
	results, ai, err := s.app.search(query, limit)
	if err != nil {
		sendError(w, 400, err)
		return
	}
	allowed, err := s.filteredPaths(r.URL.Query().Get("tag"), r.URL.Query().Get("source"))
	if err != nil {
		sendError(w, 400, err)
		return
	}
	allow := map[string]bool{}
	for _, path := range allowed {
		allow[path] = true
	}
	images := []imageRecord{}
	for _, result := range results {
		path := expandPath(result.Path)
		if allow[path] {
			score := result.Score
			images = append(images, s.imageRecord(path, &score))
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "images": images, "query": query, "ai": ai})
}

func (s *server) appFolders(w http.ResponseWriter, _ *http.Request) {
	paths, sources, _ := s.app.snapshot()
	folders := []map[string]any{}
	for _, source := range sources {
		matched := []string{}
		for _, path := range paths {
			if pathInside(path, source) {
				matched = append(matched, path)
			}
		}
		folders = append(folders, s.folderRecord("source", filepath.Base(source), source, matched))
	}
	for _, name := range s.collectionNames() {
		folders = append(folders, s.folderRecord("tag", name, name, s.collectionImages(name)))
	}
	sendJSON(w, 200, map[string]any{"ok": true, "folders": folders})
}

func (s *server) folderRecord(kind, name, value string, paths []string) map[string]any {
	sort.SliceStable(paths, func(i, j int) bool { return fileMtime(paths[i]) > fileMtime(paths[j]) })
	previews := []imageRecord{}
	for _, path := range paths {
		if len(previews) == 4 {
			break
		}
		previews = append(previews, s.imageRecord(path, nil))
	}
	return map[string]any{"kind": kind, "name": name, "value": value, "count": len(paths), "images": previews}
}

func (s *server) appIndex(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Folder         string   `json:"folder"`
		Folders        []string `json:"folders"`
		IncludeLibrary bool     `json:"includeLibrary"`
	}
	if err := decodeJSON(r, &request, 2<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	folders := request.Folders
	if request.Folder != "" {
		folders = append(folders, request.Folder)
	}
	if request.IncludeLibrary {
		folders = append(folders, s.app.libraryDir)
	}
	_, _, job := s.app.snapshot()
	if job.State == "running" {
		sendError(w, 409, fmt.Errorf("an index job is already running"))
		return
	}
	s.app.setJob("running", 0, 0, "Scanning image folders…")
	go func() {
		if err := s.app.indexFolders(folders); err != nil {
			s.app.setJob("error", 0, 0, err.Error())
			return
		}
		paths, _, _ := s.app.snapshot()
		message := fmt.Sprintf("Library ready: %d images.", len(paths))
		s.app.setJob("complete", len(paths), len(paths), message)
	}()
	sendJSON(w, 202, map[string]any{"ok": true, "folders": folders})
}

func safeImageName(value string) (string, error) {
	name := filepath.Base(value)
	ext := strings.ToLower(filepath.Ext(name))
	if !imageExtensions[ext] {
		return "", fmt.Errorf("unsupported image type")
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	stem = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._- ", r) {
			return r
		}
		return '-'
	}, stem)
	stem = strings.Join(strings.Fields(stem), "-")
	stem = strings.Trim(stem, ".-")
	if stem == "" {
		stem = "image"
	}
	if len(stem) > 100 {
		stem = stem[:100]
	}
	return stem + ext, nil
}

func uniqueFile(directory, name string) string {
	target := filepath.Join(directory, name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for number := 2; ; number++ {
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			return target
		}
		target = filepath.Join(directory, fmt.Sprintf("%s-%d%s", stem, number, ext))
	}
}

func (s *server) appUpload(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength <= 0 || r.ContentLength > maxUploadBytes {
		sendError(w, 400, fmt.Errorf("image must be between 1 byte and 100 MB"))
		return
	}
	name, err := safeImageName(r.URL.Query().Get("name"))
	if err != nil {
		sendError(w, 400, err)
		return
	}
	target := uniqueFile(s.app.libraryDir, name)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		sendError(w, 400, err)
		return
	}
	_, copyErr := io.Copy(file, http.MaxBytesReader(w, r.Body, maxUploadBytes))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(target)
		sendError(w, 400, fmt.Errorf("could not save image"))
		return
	}
	verify, err := os.Open(target)
	if err == nil {
		_, _, err = image.DecodeConfig(verify)
		_ = verify.Close()
	}
	// Go's standard library cannot decode WebP, but browsers can. Other formats
	// are verified before they enter the library.
	if err != nil && strings.ToLower(filepath.Ext(target)) != ".webp" {
		_ = os.Remove(target)
		sendError(w, 400, fmt.Errorf("uploaded file is not a valid image"))
		return
	}
	s.app.addPath(target)
	if folder := strings.TrimSpace(r.URL.Query().Get("folder")); folder != "" {
		if _, err := s.linkTag(folder, target); err != nil {
			sendError(w, 400, err)
			return
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": filepath.Base(target), "path": target, "folder": r.URL.Query().Get("folder")})
}

func collectionName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			builder.WriteRune(r)
		} else if r == ' ' {
			builder.WriteRune('-')
		}
	}
	name := strings.Trim(builder.String(), "-_")
	if name == "" || len(name) > 80 {
		return "", fmt.Errorf("folder name must contain letters or numbers")
	}
	return name, nil
}

func (s *server) appTags(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Action  string `json:"action"`
		Tag     string `json:"tag"`
		Prompt  string `json:"prompt"`
		Limit   int    `json:"limit"`
		ImageID int    `json:"imageId"`
	}
	if err := decodeJSON(r, &request, 2<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	name, err := collectionName(request.Tag)
	if err != nil {
		sendError(w, 400, err)
		return
	}
	directory := filepath.Join(s.app.tagsDir, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		sendError(w, 400, err)
		return
	}
	switch request.Action {
	case "", "add":
		paths, _, _ := s.app.snapshot()
		if request.ImageID < 0 || request.ImageID >= len(paths) {
			sendError(w, 400, fmt.Errorf("unknown image"))
			return
		}
		added, err := s.linkTag(name, paths[request.ImageID])
		if err != nil {
			sendError(w, 400, err)
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "tag": name, "added": added})
	case "create":
		sendJSON(w, 200, map[string]any{"ok": true, "tag": name})
	case "fill":
		sendError(w, 400, fmt.Errorf("automatic folder filling is not available yet"))
	case "remove":
		paths, _, _ := s.app.snapshot()
		if request.ImageID < 0 || request.ImageID >= len(paths) {
			sendError(w, 400, fmt.Errorf("unknown image"))
			return
		}
		removed := s.unlinkTag(name, paths[request.ImageID])
		sendJSON(w, 200, map[string]any{"ok": true, "tag": name, "removed": removed})
	default:
		sendError(w, 400, fmt.Errorf("unknown tag action"))
	}
}

func (s *server) collectionNames() []string {
	entries, _ := os.ReadDir(s.app.tagsDir)
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func validCollectionName(name string) bool {
	clean, err := collectionName(name)
	return err == nil && clean == name
}

func (s *server) collectionImages(name string) []string {
	if !validCollectionName(name) {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Join(s.app.tagsDir, name))
	paths := []string{}
	manifestPath := filepath.Join(s.app.tagsDir, name, "images.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest []string
		if json.Unmarshal(data, &manifest) == nil {
			paths = append(paths, manifest...)
		}
	}
	for _, entry := range entries {
		if entry.Name() == "images.json" {
			continue
		}
		path := filepath.Join(s.app.tagsDir, name, entry.Name())
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil && imageExtensions[strings.ToLower(filepath.Ext(resolved))] {
			paths = append(paths, resolved)
		}
	}
	return existingUniquePaths(paths)
}

func (s *server) tagsForImage(path string) []string {
	path = expandPath(path)
	result := []string{}
	for _, name := range s.collectionNames() {
		for _, tagged := range s.collectionImages(name) {
			if tagged == path {
				result = append(result, name)
				break
			}
		}
	}
	return result
}

func (s *server) linkTag(name, source string) (bool, error) {
	name, err := collectionName(name)
	if err != nil {
		return false, err
	}
	directory := filepath.Join(s.app.tagsDir, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return false, err
	}
	source = expandPath(source)
	for _, existing := range s.collectionImages(name) {
		if existing == source {
			return false, nil
		}
	}
	paths := s.collectionImages(name)
	paths = append(paths, source)
	return true, writeTagManifest(directory, paths)
}

func (s *server) unlinkTag(name, source string) bool {
	directory := filepath.Join(s.app.tagsDir, name)
	paths := s.collectionImages(name)
	kept := make([]string, 0, len(paths))
	removed := false
	for _, path := range paths {
		if expandPath(path) == expandPath(source) {
			removed = true
			continue
		}
		kept = append(kept, path)
	}
	if removed {
		_ = writeTagManifest(directory, kept)
	}
	// Remove a matching legacy symlink too, if one exists.
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil && expandPath(resolved) == expandPath(source) {
			_ = os.Remove(path)
			removed = true
		}
	}
	return removed
}

func writeTagManifest(directory string, paths []string) error {
	paths = existingUniquePaths(paths)
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	target := filepath.Join(directory, "images.json")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func (s *server) image(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	paths, _, _ := s.app.snapshot()
	if err != nil || id < 0 || id >= len(paths) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, paths[id])
}

func (s *server) board(w http.ResponseWriter, r *http.Request) {
	serveNamedFile(w, r, s.app.boardsDir, r.PathValue("name"))
}

func (s *server) reference(w http.ResponseWriter, r *http.Request) {
	serveNamedFile(w, r, s.app.referenceDir, r.PathValue("name"))
}

func serveNamedFile(w http.ResponseWriter, r *http.Request, directory, name string) {
	name = filepath.Base(name)
	if name == "." || name == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(directory, name))
}

func (s *server) boardRecords() []map[string]any {
	entries, _ := os.ReadDir(s.app.boardsDir)
	records := []map[string]any{}
	for _, entry := range entries {
		if entry.IsDir() || !imageExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			continue
		}
		path := filepath.Join(s.app.boardsDir, entry.Name())
		info, _ := entry.Info()
		record := map[string]any{"name": entry.Name(), "url": "/board/" + url.PathEscape(entry.Name()), "mtime": info.ModTime().Unix(), "source": "", "aspect": "", "query": "", "tags": []string{}}
		if data, err := os.ReadFile(strings.TrimSuffix(path, filepath.Ext(path)) + ".json"); err == nil {
			var metadata map[string]any
			if json.Unmarshal(data, &metadata) == nil {
				for _, key := range []string{"source", "aspect", "query", "tags"} {
					if value, ok := metadata[key]; ok {
						record[key] = value
					}
				}
			}
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i]["mtime"].(int64) > records[j]["mtime"].(int64) })
	return records
}

func (s *server) appBoards(w http.ResponseWriter, _ *http.Request) {
	sendJSON(w, 200, map[string]any{"ok": true, "boards": s.boardRecords()})
}

func (s *server) practiceImages(w http.ResponseWriter, r *http.Request) {
	paths, err := s.filteredPaths(r.URL.Query().Get("tag"), "")
	if err != nil {
		sendError(w, 400, err)
		return
	}
	if selected := r.URL.Query().Get("image"); selected != "" {
		id, parseErr := strconv.Atoi(selected)
		all, _, _ := s.app.snapshot()
		if parseErr != nil || id < 0 || id >= len(all) {
			sendError(w, 400, fmt.Errorf("unknown image"))
			return
		}
		paths = []string{all[id]}
	}
	total := len(paths)
	count := boundedInt(r.URL.Query().Get("count"), 30, 1, 10000)
	if r.URL.Query().Get("mode") == "all" {
		paths = shuffled(paths)
	} else {
		sort.SliceStable(paths, func(i, j int) bool { return fileMtime(paths[i]) > fileMtime(paths[j]) })
		if len(paths) > count {
			paths = paths[:count]
		}
	}
	records := []imageRecord{}
	for _, path := range paths {
		records = append(records, s.imageRecord(path, nil))
	}
	sendJSON(w, 200, map[string]any{"images": records, "selected": len(records), "total": total, "out": s.app.boardsDir})
}

func (s *server) practiceCollections(w http.ResponseWriter, _ *http.Request) {
	collections := []map[string]any{}
	for _, name := range s.collectionNames() {
		collections = append(collections, map[string]any{"name": name, "count": len(s.collectionImages(name))})
	}
	sendJSON(w, 200, map[string]any{"collections": collections})
}

func (s *server) practiceSearch(w http.ResponseWriter, r *http.Request) {
	// Match the app search payload to the storyboard's simpler schema.
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		s.practiceImages(w, r)
		return
	}
	results, _, err := s.app.search(query, 80)
	if err != nil {
		sendError(w, 400, err)
		return
	}
	tagged, _ := s.filteredPaths(r.URL.Query().Get("tag"), "")
	allow := map[string]bool{}
	for _, path := range tagged {
		allow[path] = true
	}
	images := []imageRecord{}
	for _, result := range results {
		path := expandPath(result.Path)
		if allow[path] {
			images = append(images, s.imageRecord(path, nil))
		}
	}
	paths, _, _ := s.app.snapshot()
	sendJSON(w, 200, map[string]any{"images": images, "selected": len(images), "total": len(paths), "query": query, "out": s.app.boardsDir})
}

func (s *server) references(w http.ResponseWriter, _ *http.Request) {
	entries, _ := os.ReadDir(s.app.referenceDir)
	type reference struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Time int64  `json:"-"`
	}
	items := []reference{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, _ := entry.Info()
		items = append(items, reference{Name: entry.Name(), URL: "/reference/" + url.PathEscape(entry.Name()), Time: info.ModTime().UnixNano()})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Time < items[j].Time })
	if len(items) > 5 {
		items = items[:5]
	}
	sendJSON(w, 200, map[string]any{"references": items, "limit": 5})
}

func cleanStem(value string) string {
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(value), filepath.Ext(value)))
	var builder strings.Builder
	separator := false
	for _, r := range stem {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			separator = false
		} else if !separator && builder.Len() > 0 {
			builder.WriteByte('-')
			separator = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "image"
	}
	if len(result) > 60 {
		return result[:60]
	}
	return result
}

func (s *server) addReference(w http.ResponseWriter, r *http.Request) {
	entries, _ := os.ReadDir(s.app.referenceDir)
	if len(entries) >= 5 {
		sendError(w, 400, fmt.Errorf("five-reference limit reached"))
		return
	}
	var request struct {
		DataURL string `json:"dataUrl"`
		Name    string `json:"name"`
	}
	if err := decodeJSON(r, &request, 20<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	parts := strings.SplitN(request.DataURL, ",", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "data:image/") {
		sendError(w, 400, fmt.Errorf("reference must be an image"))
		return
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil || len(raw) > 15<<20 {
		sendError(w, 400, fmt.Errorf("invalid or oversized reference image"))
		return
	}
	ext := strings.ToLower(filepath.Ext(request.Name))
	if !imageExtensions[ext] {
		ext = ".png"
	}
	target := uniqueFile(s.app.referenceDir, cleanStem(request.Name)+ext)
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		sendError(w, 400, err)
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": filepath.Base(target)})
}

func (s *server) deleteReference(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Query().Get("name"))
	path := filepath.Join(s.app.referenceDir, name)
	if name == "." || name == "" {
		sendError(w, 404, fmt.Errorf("reference not found"))
		return
	}
	if err := os.Remove(path); err != nil {
		sendError(w, 404, fmt.Errorf("reference not found"))
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func (s *server) saveBoard(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DataURL    string `json:"dataUrl"`
		HasDrawing bool   `json:"hasDrawing"`
		Aspect     string `json:"aspect"`
		Index      int    `json:"index"`
		ImageName  string `json:"imageName"`
		ImageID    int    `json:"imageId"`
		Query      string `json:"query"`
	}
	if err := decodeJSON(r, &request, 25<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	if !request.HasDrawing {
		sendError(w, 400, fmt.Errorf("empty drawing"))
		return
	}
	parts := strings.SplitN(request.DataURL, ",", 2)
	if len(parts) != 2 {
		sendError(w, 400, fmt.Errorf("invalid drawing"))
		return
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	paths, _, _ := s.app.snapshot()
	if err != nil || request.ImageID < 0 || request.ImageID >= len(paths) {
		sendError(w, 400, fmt.Errorf("invalid drawing"))
		return
	}
	aspect := strings.ReplaceAll(request.Aspect, ":", "x")
	filename := fmt.Sprintf("%04d_%s_%s.png", request.Index, aspect, cleanStem(request.ImageName))
	target := filepath.Join(s.app.boardsDir, filename)
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		sendError(w, 400, err)
		return
	}
	metadata := map[string]any{"file": filename, "source": paths[request.ImageID], "aspect": request.Aspect, "tags": s.tagsForImage(paths[request.ImageID]), "query": strings.TrimSpace(request.Query)}
	data, _ := json.MarshalIndent(metadata, "", "  ")
	_ = os.WriteFile(strings.TrimSuffix(target, ".png")+".json", append(data, '\n'), 0o644)
	sendJSON(w, 200, map[string]any{"ok": true, "file": filename})
}

func decodeJSON(r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}
