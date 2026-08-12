package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"math/rand"
	"mime"
	"net"
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
	mux.HandleFunc("GET /licenses", s.licenses)
	mux.HandleFunc("GET /assets/{name}", s.asset)
	mux.HandleFunc("GET /api/app/state", s.appState)
	mux.HandleFunc("GET /api/app/update", s.appUpdate)
	mux.HandleFunc("POST /api/app/update", s.installAppUpdate)
	mux.HandleFunc("GET /api/app/images", s.appImages)
	mux.HandleFunc("GET /api/app/folders", s.appFolders)
	mux.HandleFunc("GET /api/app/search", s.appSearch)
	mux.HandleFunc("GET /api/app/related/{id}", s.appRelated)
	mux.HandleFunc("GET /api/app/canvas", s.appCanvas)
	mux.HandleFunc("POST /api/app/canvas", s.saveAppCanvas)
	mux.HandleFunc("GET /api/app/boards", s.appBoards)
	mux.HandleFunc("POST /api/app/index", s.appIndex)
	mux.HandleFunc("POST /api/app/upload", s.appUpload)
	mux.HandleFunc("POST /api/app/tags", s.appTags)
	mux.HandleFunc("GET /api/app/ai", s.aiState)
	mux.HandleFunc("POST /api/app/ai/embeddings", s.aiEmbeddings)
	mux.HandleFunc("GET /api/app/ai/query", s.aiQuery)
	mux.HandleFunc("POST /api/app/ai/query", s.saveAIQuery)
	mux.HandleFunc("POST /api/app/ai/search", s.aiSearch)
	mux.HandleFunc("GET /image/{id}", s.image)
	mux.HandleFunc("GET /thumbnail/{id}", s.thumbnail)
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

func (s *server) appUpdate(w http.ResponseWriter, _ *http.Request) {
	state, err := checkForUpdate()
	if err != nil {
		sendError(w, http.StatusBadGateway, err)
		return
	}
	sendJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
		updateState
	}{OK: true, updateState: state})
}

func (s *server) installAppUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Pictogrep-Action") != "install-update" {
		sendError(w, http.StatusForbidden, fmt.Errorf("update confirmation is required"))
		return
	}
	state, err := checkForUpdate()
	if err != nil {
		sendError(w, http.StatusBadGateway, err)
		return
	}
	if !state.Available {
		sendJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": false, "currentVersion": version})
		return
	}
	if err := installUpdate(state); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": true, "version": state.LatestVersion, "restartRequired": true})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !isLoopbackAuthority(r.Host) {
			sendError(w, http.StatusForbidden, fmt.Errorf("Pictogrep only accepts local requests"))
			return
		}
		if requestChangesState(r.Method) && !hasTrustedOrigin(r) {
			sendError(w, http.StatusForbidden, fmt.Errorf("cross-origin requests are not allowed"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestChangesState(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func hasTrustedOrigin(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Non-browser clients such as the CLI do not send browser origin headers.
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host) && isLoopbackAuthority(parsed.Host)
}

func isLoopbackAuthority(authority string) bool {
	host := strings.TrimSpace(authority)
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

func (s *server) licenses(w http.ResponseWriter, r *http.Request) {
	files := []struct {
		name  string
		label string
	}{
		{name: "LICENSE", label: "Pictogrep — MIT"},
		{name: "third_party/transformers.js/LICENSE", label: "Transformers.js — Apache-2.0"},
		{name: "third_party/onnxruntime/LICENSE", label: "ONNX Runtime — MIT"},
		{name: "third_party/onnxruntime/ThirdPartyNotices.txt", label: "ONNX Runtime third-party notices"},
	}
	var output bytes.Buffer
	for _, file := range files {
		data, err := embeddedFiles.ReadFile(file.name)
		if err != nil {
			http.Error(w, "could not read embedded licenses", http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(&output, "%s\n%s\n\n", file.label, strings.Repeat("=", len(file.label)))
		output.Write(data)
		output.WriteString("\n\n")
	}
	serveBytes(w, r, "licenses.txt", "text/plain; charset=utf-8", output.Bytes())
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
	switch strings.ToLower(filepath.Ext(name)) {
	case ".js", ".mjs":
		contentType = "text/javascript; charset=utf-8"
	case ".wasm":
		contentType = "application/wasm"
	}
	serveBytes(w, r, name, contentType, data)
}

func serveBytes(w http.ResponseWriter, r *http.Request, name, contentType string, data []byte) {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
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
		"ok": true, "version": version, "model": s.app.embeddingModel.ModelID, "pretrained": s.app.embeddingModel.Revision,
		"semanticModel": s.app.embeddingModel.Key, "embeddingModel": s.app.embeddingModel,
		"updateMethod": updateMethod(),
		"index":        index, "indexJob": job, "sources": sources, "tags": tags,
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
		"indexed": len(paths) - len(missing), "total": len(paths), "missing": missing, "model": s.app.embeddingModel,
	})
}

func (s *server) aiEmbeddings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model string `json:"model"`
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
	if request.Model != s.app.embeddingModel.Key {
		sendError(w, 409, fmt.Errorf("embedding model changed; reload Pictogrep"))
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
		mtime := embeddingMtime(path)
		if item.Mtime != mtime && item.Mtime != mtime/1000 {
			sendError(w, 409, fmt.Errorf("image changed while it was being indexed"))
			return
		}
		records[path] = embeddingRecord{Mtime: mtime, Vector: item.Vector}
	}
	if err := s.app.updateEmbeddings(records); err != nil {
		sendError(w, 400, err)
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "saved": len(records)})
}

func (s *server) aiQuery(w http.ResponseWriter, r *http.Request) {
	query := normalizeSemanticQuery(r.URL.Query().Get("q"))
	if query == "" || len(query) > 500 {
		sendError(w, 400, fmt.Errorf("search must be between 1 and 500 characters"))
		return
	}
	vector, found := s.app.queryEmbedding(query)
	sendJSON(w, 200, map[string]any{"ok": true, "cached": found, "query": query, "vector": vector, "model": s.app.embeddingModel.Key})
}

func (s *server) saveAIQuery(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model  string    `json:"model"`
		Query  string    `json:"query"`
		Vector []float32 `json:"vector"`
	}
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	if request.Model != s.app.embeddingModel.Key {
		sendError(w, 409, fmt.Errorf("embedding model changed; reload Pictogrep"))
		return
	}
	if err := s.app.updateQueryEmbedding(request.Query, request.Vector); err != nil {
		sendError(w, 400, err)
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "cached": true, "query": normalizeSemanticQuery(request.Query)})
}

func (s *server) aiSearch(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model  string    `json:"model"`
		Vector []float32 `json:"vector"`
		Limit  int       `json:"limit"`
		Tag    string    `json:"tag"`
		Source string    `json:"source"`
	}
	if err := decodeJSON(r, &request, 4<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	if request.Model != s.app.embeddingModel.Key {
		sendError(w, 409, fmt.Errorf("embedding model changed; reload Pictogrep"))
		return
	}
	if len(request.Vector) != s.app.embeddingModel.Dimensions {
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
	} else if r.URL.Query().Get("mode") == "random" {
		random := rand.New(rand.NewSource(time.Now().UnixNano()))
		random.Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
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

func (s *server) appRelated(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	paths, _, _ := s.app.snapshot()
	if err != nil || id < 0 || id >= len(paths) {
		sendError(w, 400, fmt.Errorf("unknown image"))
		return
	}
	limit := boundedInt(r.URL.Query().Get("limit"), 18, 1, 30)
	indexed := s.app.indexedEmbeddingCount()
	vector, ready := s.app.imageEmbedding(paths[id])
	images := []imageRecord{}
	if ready {
		for _, result := range s.app.vectorSearch(vector, limit+1) {
			if result.Path == paths[id] {
				continue
			}
			score := result.Score
			images = append(images, s.imageRecord(result.Path, &score))
			if len(images) == limit {
				break
			}
		}
	}
	sendJSON(w, 200, map[string]any{
		"ok": true, "ready": ready, "images": images,
		"indexed": indexed, "total": len(paths),
	})
}

func (s *server) canvasScope(tag, source string) (string, []string, error) {
	if tag != "" && source != "" {
		return "", nil, fmt.Errorf("choose one folder")
	}
	if tag != "" {
		paths, err := s.filteredPaths(tag, "")
		if err != nil {
			return "", nil, err
		}
		return "tag:" + tag, paths, nil
	}
	if source == "" {
		return "", nil, fmt.Errorf("open a folder first")
	}
	source = expandPath(source)
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return "", nil, fmt.Errorf("unknown folder")
	}
	_, sources, _ := s.app.snapshot()
	known := false
	for _, root := range sources {
		if source == root || pathInside(source, root) {
			known = true
			break
		}
	}
	if !known {
		return "", nil, fmt.Errorf("unknown folder")
	}
	paths, err := s.filteredPaths("", source)
	if err != nil {
		return "", nil, err
	}
	return "source:" + source, paths, nil
}

func (s *server) canvasImageRecords(paths []string) []imageRecord {
	allPaths, _, _ := s.app.snapshot()
	pathIDs := make(map[string]int, len(allPaths))
	for id, path := range allPaths {
		pathIDs[path] = id
	}
	tagsByPath := map[string][]string{}
	for _, name := range s.collectionNames() {
		for _, path := range s.collectionImages(name) {
			tagsByPath[path] = append(tagsByPath[path], name)
		}
	}
	images := make([]imageRecord, 0, len(paths))
	for _, path := range paths {
		id, found := pathIDs[path]
		if !found {
			continue
		}
		images = append(images, imageRecord{ID: id, Name: filepath.Base(path), Path: path, URL: "/image/" + strconv.Itoa(id), Tags: tagsByPath[path]})
	}
	return images
}

func (s *server) appCanvas(w http.ResponseWriter, r *http.Request) {
	scope, paths, err := s.canvasScope(r.URL.Query().Get("tag"), r.URL.Query().Get("source"))
	if err != nil {
		sendError(w, 400, err)
		return
	}
	stored, err := s.app.loadCanvasLayout(scope)
	if err != nil {
		sendError(w, 500, fmt.Errorf("could not open canvas"))
		return
	}
	images := s.canvasImageRecords(paths)
	positions := map[string]canvasPoint{}
	for _, image := range images {
		if point, found := stored[image.Path]; found {
			positions[strconv.Itoa(image.ID)] = point
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "images": images, "positions": positions})
}

func (s *server) saveAppCanvas(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Tag       string `json:"tag"`
		Source    string `json:"source"`
		Positions []struct {
			ID int     `json:"id"`
			X  float64 `json:"x"`
			Y  float64 `json:"y"`
		} `json:"positions"`
	}
	if err := decodeJSON(r, &request, 8<<20); err != nil {
		sendError(w, 400, err)
		return
	}
	scope, paths, err := s.canvasScope(request.Tag, request.Source)
	if err != nil {
		sendError(w, 400, err)
		return
	}
	allPaths, _, _ := s.app.snapshot()
	allowed := map[int]string{}
	pathSet := map[string]bool{}
	for _, path := range paths {
		pathSet[path] = true
	}
	for id, path := range allPaths {
		if pathSet[path] {
			allowed[id] = path
		}
	}
	positions := map[string]canvasPoint{}
	for _, item := range request.Positions {
		path, found := allowed[item.ID]
		if !found || math.IsNaN(item.X) || math.IsNaN(item.Y) || math.IsInf(item.X, 0) || math.IsInf(item.Y, 0) || math.Abs(item.X) > 1e7 || math.Abs(item.Y) > 1e7 {
			sendError(w, 400, fmt.Errorf("invalid canvas position"))
			return
		}
		positions[path] = canvasPoint{X: item.X, Y: item.Y}
	}
	if err := s.app.saveCanvasLayout(scope, positions); err != nil {
		sendError(w, 500, fmt.Errorf("could not save canvas"))
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "saved": len(positions)})
}

func (s *server) appFolders(w http.ResponseWriter, _ *http.Request) {
	paths, sources, _ := s.app.snapshot()
	folders := []map[string]any{}
	pathIDs := make(map[string]int, len(paths))
	for id, path := range paths {
		pathIDs[path] = id
	}
	for _, source := range sources {
		folders = append(folders, s.sourceFolderRecords(source, paths, pathIDs)...)
	}
	for _, name := range s.collectionNames() {
		record := s.folderRecord("tag", name, name, s.collectionImages(name), pathIDs)
		record["depth"] = 0
		folders = append(folders, record)
	}
	sendJSON(w, 200, map[string]any{"ok": true, "folders": folders})
}

func (s *server) sourceFolderRecords(source string, paths []string, pathIDs map[string]int) []map[string]any {
	byDirectory := map[string][]string{source: {}}
	for _, path := range paths {
		if !pathInside(path, source) {
			continue
		}
		byDirectory[source] = append(byDirectory[source], path)
		relative, err := filepath.Rel(source, filepath.Dir(path))
		if err != nil || relative == "." || relative == "" {
			continue
		}
		current := source
		for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
			if part == "" || part == "." || part == ".." {
				continue
			}
			current = filepath.Join(current, part)
			byDirectory[current] = append(byDirectory[current], path)
		}
	}

	directories := make([]string, 0, len(byDirectory)-1)
	for directory := range byDirectory {
		if directory != source {
			directories = append(directories, directory)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		left, _ := filepath.Rel(source, directories[i])
		right, _ := filepath.Rel(source, directories[j])
		return strings.ToLower(filepath.ToSlash(left)) < strings.ToLower(filepath.ToSlash(right))
	})
	directories = append([]string{source}, directories...)

	records := make([]map[string]any, 0, len(directories))
	for _, directory := range directories {
		relative, _ := filepath.Rel(source, directory)
		depth := 0
		name := filepath.Base(source)
		if relative != "." && relative != "" {
			depth = len(strings.Split(filepath.Clean(relative), string(filepath.Separator)))
			name = filepath.Base(directory)
		}
		record := s.folderRecord("source", name, directory, byDirectory[directory], pathIDs)
		record["depth"] = depth
		record["relative"] = filepath.ToSlash(relative)
		records = append(records, record)
	}
	return records
}

func (s *server) folderRecord(kind, name, value string, paths []string, pathIDs map[string]int) map[string]any {
	sort.SliceStable(paths, func(i, j int) bool { return fileMtime(paths[i]) > fileMtime(paths[j]) })
	previews := []imageRecord{}
	for _, path := range paths {
		if len(previews) == 4 {
			break
		}
		id, found := pathIDs[path]
		if !found {
			continue
		}
		previews = append(previews, imageRecord{ID: id, Name: filepath.Base(path), Path: path, Mtime: fileMtime(path), URL: "/image/" + strconv.Itoa(id)})
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
	if r.ContentLength == 0 || r.ContentLength > maxUploadBytes {
		sendError(w, 400, fmt.Errorf("image must be between 1 byte and 100 MB"))
		return
	}
	name, err := safeImageName(r.URL.Query().Get("name"))
	if err != nil {
		sendError(w, 400, err)
		return
	}
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if folder != "" {
		folder, err = collectionName(folder)
		if err != nil {
			sendError(w, 400, err)
			return
		}
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
	if !validImageFile(target) {
		_ = os.Remove(target)
		sendError(w, 400, fmt.Errorf("uploaded file is not a valid image"))
		return
	}
	if folder != "" {
		if _, err := s.linkTag(folder, target); err != nil {
			_ = os.Remove(target)
			sendError(w, 400, err)
			return
		}
	}
	s.app.addPath(target)
	sendJSON(w, 200, map[string]any{"ok": true, "name": filepath.Base(target), "path": target, "folder": folder})
}

func validImageFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if _, format, err := image.DecodeConfig(file); err == nil {
		return imageFormatMatches(strings.ToLower(filepath.Ext(path)), format)
	}
	if strings.ToLower(filepath.Ext(path)) != ".webp" {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	header := make([]byte, 20)
	_, err = io.ReadFull(file, header)
	info, statErr := file.Stat()
	return err == nil && statErr == nil && validWebPHeader(header, info.Size())
}

func imageFormatMatches(extension, format string) bool {
	switch extension {
	case ".jpg", ".jpeg":
		return format == "jpeg"
	case ".png":
		return format == "png"
	case ".gif":
		return format == "gif"
	default:
		return false
	}
}

func validWebPHeader(header []byte, size int64) bool {
	if len(header) < 20 || size < 20 || string(header[:4]) != "RIFF" || string(header[8:12]) != "WEBP" {
		return false
	}
	declaredSize := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	chunkSize := int64(binary.LittleEndian.Uint32(header[16:20]))
	chunk := string(header[12:16])
	return declaredSize == size && chunkSize <= size-20 && (chunk == "VP8 " || chunk == "VP8L" || chunk == "VP8X")
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
		Action  string    `json:"action"`
		Model   string    `json:"model"`
		Tag     string    `json:"tag"`
		Prompt  string    `json:"prompt"`
		Limit   int       `json:"limit"`
		ImageID int       `json:"imageId"`
		Vector  []float32 `json:"vector"`
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
		directory := filepath.Join(s.app.tagsDir, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			sendError(w, 400, err)
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "tag": name})
	case "fill":
		prompt := normalizeSemanticQuery(request.Prompt)
		if prompt == "" || len(prompt) > 500 {
			sendError(w, 400, fmt.Errorf("folder search must be between 1 and 500 characters"))
			return
		}
		if request.Model != s.app.embeddingModel.Key {
			sendError(w, 409, fmt.Errorf("embedding model changed; reload Pictogrep"))
			return
		}
		if len(request.Vector) != s.app.embeddingModel.Dimensions {
			sendError(w, 400, fmt.Errorf("invalid folder search vector"))
			return
		}
		if request.Limit < 1 || request.Limit > 200 {
			request.Limit = 50
		}
		paths, _, _ := s.app.snapshot()
		results := s.app.vectorSearch(request.Vector, request.Limit)
		selected := make([]string, 0, len(results))
		for _, result := range results {
			selected = append(selected, result.Path)
		}
		directory := filepath.Join(s.app.tagsDir, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			sendError(w, 400, err)
			return
		}
		existing := s.collectionImages(name)
		combined := existingUniquePaths(append(existing, selected...))
		if err := writeTagManifest(directory, combined); err != nil {
			sendError(w, 400, err)
			return
		}
		sendJSON(w, 200, map[string]any{
			"ok": true, "tag": name, "prompt": prompt, "added": len(combined) - len(existing),
			"matched": len(selected), "indexed": s.app.indexedEmbeddingCount(), "total": len(paths),
		})
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

func (s *server) thumbnail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	paths, _, _ := s.app.snapshot()
	if err != nil || id < 0 || id >= len(paths) {
		http.NotFound(w, r)
		return
	}
	path := paths[id]
	key := sha256.Sum256([]byte(path + "\x00" + strconv.FormatInt(embeddingMtime(path), 10)))
	target := filepath.Join(s.app.thumbnailDir, hashHex(key[:])+".jpg")
	if file, err := os.Open(target); err == nil {
		defer file.Close()
		info, _ := file.Stat()
		w.Header().Set("Content-Type", "image/jpeg")
		http.ServeContent(w, r, filepath.Base(target), info.ModTime(), file)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	source, _, decodeErr := image.Decode(file)
	_ = file.Close()
	if decodeErr != nil {
		s.image(w, r)
		return
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	const maximum = 160
	if width > maximum || height > maximum {
		scale := math.Min(float64(maximum)/float64(width), float64(maximum)/float64(height))
		width = max(1, int(float64(width)*scale))
		height = max(1, int(float64(height)*scale))
	}
	thumbnail := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceY := bounds.Min.Y + y*bounds.Dy()/height
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/width
			thumbnail.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	output, err := os.CreateTemp(s.app.thumbnailDir, "thumbnail-*.tmp")
	if err != nil {
		s.image(w, r)
		return
	}
	temporary := output.Name()
	err = jpeg.Encode(output, thumbnail, &jpeg.Options{Quality: 72})
	closeErr := output.Close()
	if err != nil || closeErr != nil || os.Rename(temporary, target) != nil {
		_ = os.Remove(temporary)
		s.image(w, r)
		return
	}
	file, err = os.Open(target)
	if err != nil {
		s.image(w, r)
		return
	}
	defer file.Close()
	info, _ := file.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, filepath.Base(target), info.ModTime(), file)
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
		info, err := entry.Info()
		if err != nil {
			continue
		}
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
		info, err := entry.Info()
		if err != nil {
			continue
		}
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
	if len(parts) != 2 {
		sendError(w, 400, fmt.Errorf("reference must be an image"))
		return
	}
	extensionByHeader := map[string]string{
		"data:image/png;base64":  ".png",
		"data:image/jpeg;base64": ".jpg",
		"data:image/jpg;base64":  ".jpg",
		"data:image/gif;base64":  ".gif",
		"data:image/webp;base64": ".webp",
	}
	ext, ok := extensionByHeader[strings.ToLower(parts[0])]
	if !ok {
		sendError(w, 400, fmt.Errorf("reference must be an image"))
		return
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil || len(raw) > 15<<20 || !validImageData(raw, ext) {
		sendError(w, 400, fmt.Errorf("invalid or oversized reference image"))
		return
	}
	target := uniqueFile(s.app.referenceDir, cleanStem(request.Name)+ext)
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		sendError(w, 400, err)
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": filepath.Base(target)})
}

func validImageData(raw []byte, extension string) bool {
	if _, format, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
		return imageFormatMatches(extension, format)
	}
	return extension == ".webp" && validWebPHeader(raw, int64(len(raw)))
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
	if len(parts) != 2 || parts[0] != "data:image/png;base64" {
		sendError(w, 400, fmt.Errorf("invalid drawing"))
		return
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	paths, _, _ := s.app.snapshot()
	if err != nil || !validImageData(raw, ".png") || request.ImageID < 0 || request.ImageID >= len(paths) || request.Index < 1 || request.Index > 9999 {
		sendError(w, 400, fmt.Errorf("invalid drawing"))
		return
	}
	validAspects := map[string]bool{"16:9": true, "4:3": true, "2:1": true, "3:4": true}
	if !validAspects[request.Aspect] {
		sendError(w, 400, fmt.Errorf("invalid drawing aspect"))
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
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("invalid request: request is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("invalid request: trailing data")
	}
	return nil
}
