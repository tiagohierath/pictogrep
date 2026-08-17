package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxPinterestImages        = 5000
	maxPinterestDownloadBytes = int64(2 << 30)
	maxGalleryDLLogBytes      = 64 << 10
	pinterestDownloadTimeout  = 30 * time.Minute
	pinterestUsageInterval    = 2 * time.Second
)

type galleryDLRunner func(context.Context, string, string) error

// A board takes minutes to download, so the import outlives the request that
// starts it. Closing the panel, the tab, or the whole browser leaves it running,
// and the window that comes back asks for the result.
type pinterestImport struct {
	mu        sync.Mutex
	state     string
	phase     string
	done      int
	total     int
	board     string
	result    map[string]any
	failed    string
	cancel    context.CancelFunc
	cancelled bool
	automatic bool
}

// Downloading reports how many files have arrived, because the board size is
// not known up front. Importing knows both halves.
func (job *pinterestImport) progress(phase string, done, total int) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.phase, job.done, job.total = phase, done, total
}

func (job *pinterestImport) start(board string, cancel context.CancelFunc) bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.state == "running" {
		return false
	}
	job.state, job.board = "running", board
	job.phase, job.done, job.total = "downloading", 0, 0
	job.result, job.failed, job.cancel, job.cancelled = nil, "", cancel, false
	job.automatic = false
	return true
}

// A weekly check nobody asked for should not look like an import somebody
// started. The flag rides along so the interface can stay quiet about it.
func (job *pinterestImport) startAutomatic(board string, cancel context.CancelFunc) bool {
	if !job.start(board, cancel) {
		return false
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	job.automatic = true
	return true
}

func (job *pinterestImport) finish(result map[string]any, err error) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.cancel = nil
	job.phase = ""
	switch {
	case job.cancelled:
		// Whatever finished downloading before the stop was still imported.
		job.state, job.result = "cancelled", result
	case err != nil:
		job.state, job.failed = "error", err.Error()
	default:
		job.state, job.result = "done", result
	}
}

func (job *pinterestImport) stop() bool {
	job.mu.Lock()
	cancel := job.cancel
	if cancel != nil {
		job.cancelled = true
	}
	job.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (job *pinterestImport) running() bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.state == "running"
}

func (job *pinterestImport) snapshot() map[string]any {
	job.mu.Lock()
	defer job.mu.Unlock()
	state := job.state
	if state == "" {
		state = "idle"
	}
	payload := map[string]any{
		"ok": true, "state": state, "board": job.board,
		"phase": job.phase, "done": job.done, "total": job.total,
		"automatic": job.automatic,
	}
	// Stopping still has to import whatever already arrived, so the job stays
	// "running" for a while after the user asks it to stop. Saying so keeps the
	// panel from insisting it is still downloading.
	if job.cancelled && state == "running" {
		payload["stopping"] = true
	}
	if job.result != nil {
		payload["result"] = job.result
	}
	if job.failed != "" {
		payload["error"] = job.failed
	}
	return payload
}

func (s *server) importPinterestBoard(w http.ResponseWriter, r *http.Request) {
	if !s.app.pluginEnabled("pinterest") {
		sendError(w, http.StatusNotFound, fmt.Errorf("Pinterest import plugin is disabled"))
		return
	}
	var request struct {
		URL          string `json:"url"`
		Mode         string `json:"mode"`
		SkipExisting bool   `json:"skipExisting"`
	}
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	boardURL, err := validatePinterestBoardURL(request.URL)
	if err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if request.Mode != "original" && request.Mode != "board" {
		sendError(w, http.StatusBadRequest, fmt.Errorf("choose how Pinterest images should be organized"))
		return
	}
	// A board name that cannot become a folder has to be caught here. Finding
	// out after the download would throw away everything it just spent half an
	// hour fetching.
	if request.Mode == "board" {
		if _, err := collectionName(pinterestBoardName(boardURL)); err != nil {
			sendError(w, http.StatusBadRequest, fmt.Errorf("this board's name cannot be used as a folder; import it straight into your library instead"))
			return
		}
	}

	// Deliberately not the request context: the download has to survive the
	// window that asked for it.
	ctx, cancel := context.WithCancel(context.Background())
	if !s.pinterest.start(pinterestBoardName(boardURL), cancel) {
		cancel()
		sendError(w, http.StatusConflict, fmt.Errorf("a Pinterest import is already running"))
		return
	}
	go func() {
		defer cancel()
		defer guard(func(err error) { s.pinterest.finish(nil, err) })
		result, err := s.runPinterestImport(ctx, boardURL, request.Mode, request.SkipExisting)
		// A board that imported at least once is worth following, so it gets
		// re-checked about weekly until auto-sync is turned off or the board is
		// forgotten. Only the import settles what "now" means, so the timestamp
		// is written here rather than when the request arrived.
		if err == nil {
			_ = s.app.trackBoard(trackedBoard{
				URL:          boardURL.String(),
				Mode:         request.Mode,
				SkipExisting: request.SkipExisting,
				LastSyncAt:   time.Now().Unix(),
			})
		}
		s.pinterest.finish(result, err)
	}()
	sendJSON(w, http.StatusAccepted, s.pinterest.snapshot())
}

func (s *server) pinterestImportStatus(w http.ResponseWriter, r *http.Request) {
	if !s.app.pluginEnabled("pinterest") {
		sendError(w, http.StatusNotFound, fmt.Errorf("Pinterest import plugin is disabled"))
		return
	}
	sendJSON(w, http.StatusOK, s.pinterest.snapshot())
}

func (s *server) cancelPinterestImport(w http.ResponseWriter, r *http.Request) {
	if !s.pinterest.stop() {
		sendError(w, http.StatusConflict, fmt.Errorf("no Pinterest import is running"))
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "state": "cancelled"})
}

func (s *server) runPinterestImport(ctx context.Context, boardURL *url.URL, mode string, skipExisting bool) (map[string]any, error) {
	downloadDir, err := os.MkdirTemp("", "pictogrep-pinterest-")
	if err != nil {
		return nil, fmt.Errorf("could not prepare Pinterest import")
	}
	defer os.RemoveAll(downloadDir)

	sampled := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		defer guard(nil)
		ticker := time.NewTicker(pinterestUsageInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sampled:
				return
			case <-ticker.C:
				if files, _, usageErr := pinterestDownloadUsage(downloadDir); usageErr == nil {
					s.pinterest.progress("downloading", files, 0)
				}
			}
		}
	}()
	downloadErr := s.galleryDL(ctx, boardURL.String(), downloadDir)
	// Waiting for the sampler keeps a late tick from reporting a download that
	// has already finished, or worse, one that belongs to the next import.
	close(sampled)
	<-stopped
	// Stopping an import keeps whatever already arrived, so half an hour of
	// downloading is never thrown away on the way out.
	if downloadErr != nil && ctx.Err() == nil {
		return nil, downloadErr
	}

	images, err := pinterestDownloadedImages(downloadDir)
	if err != nil {
		if ctx.Err() == nil {
			return nil, err
		}
		images = nil
	}

	boardName := pinterestBoardName(boardURL)
	folder := ""
	if mode == "board" {
		folder, err = collectionName(boardName)
		if err != nil {
			return nil, fmt.Errorf("Pinterest board name cannot be used as a folder")
		}
	}
	batch := s.app.newImportBatch()
	defer batch.commit(s.app)
	collectionPaths := []string{}
	collectionSet := map[string]bool{}
	if folder != "" {
		collectionPaths = s.collectionImages(folder)
		for _, path := range collectionPaths {
			collectionSet[path] = true
		}
	}
	imported, skipped, linked, failed := 0, 0, 0, 0
	lastError := ""
	for index, imagePath := range images {
		s.pinterest.progress("importing", index, len(images))
		file, openErr := os.Open(imagePath)
		if openErr != nil {
			failed++
			lastError = "could not read a downloaded Pinterest image"
			continue
		}
		result, _, saveErr := s.saveImportedImageWithOptions(
			file, filepath.Base(imagePath), "", "", false, true, batch,
		)
		_ = file.Close()
		_ = os.Remove(imagePath)
		if saveErr != nil {
			failed++
			lastError = saveErr.Error()
			continue
		}
		if duplicate, _ := result["duplicate"].(bool); duplicate {
			path, _ := result["path"].(string)
			if folder != "" && !skipExisting && !collectionSet[path] {
				collectionSet[path] = true
				collectionPaths = append(collectionPaths, path)
				linked++
			} else {
				skipped++
			}
			continue
		}
		if folder != "" {
			path, _ := result["path"].(string)
			if path != "" && !collectionSet[path] {
				collectionSet[path] = true
				collectionPaths = append(collectionPaths, path)
			}
		}
		imported++
	}
	s.pinterest.progress("importing", len(images), len(images))
	// The pictures are on disk either way, so they join the library before the
	// folder is written and before any of this can return an error.
	batch.commit(s.app)
	if folder != "" && len(images) > 0 {
		directory := filepath.Join(s.app.tagsDir, folder)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, fmt.Errorf("could not create Pinterest board folder: %v", err)
		}
		if err := writeTagManifest(directory, collectionPaths); err != nil {
			return nil, fmt.Errorf("could not organize Pinterest board folder: %v", err)
		}
	}
	return map[string]any{
		"ok": true, "board": boardName, "boardUrl": boardURL.String(), "folder": folder,
		"total": len(images), "imported": imported, "skipped": skipped,
		"linked": linked, "failed": failed, "lastError": lastError,
	}, nil
}

func runGalleryDL(ctx context.Context, boardURL, directory string) error {
	binary, err := galleryDLBinary()
	if err != nil {
		return importError(http.StatusServiceUnavailable, "gallery-dl is required for Pinterest imports; install gallery-dl and restart Pictogrep")
	}
	downloadContext, cancel := context.WithTimeout(ctx, pinterestDownloadTimeout)
	defer cancel()
	command := exec.CommandContext(downloadContext, binary, galleryDLArguments(boardURL, directory)...)
	command.Cancel = func() error {
		killProcessGroup(command)
		return nil
	}
	// Wait blocks until the output pipes close, which a surviving grandchild can
	// hold open forever. Without a delay, one stuck downloader would wedge the
	// import in "running" for the rest of the session: no other board could
	// start, and Pictogrep could never close itself.
	command.WaitDelay = 10 * time.Second
	groupProcess(command)
	output := &boundedCommandOutput{maximum: maxGalleryDLLogBytes}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		return importError(http.StatusBadGateway, "gallery-dl could not start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(pinterestUsageInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err == nil {
				return nil
			}
			if errors.Is(ctx.Err(), context.Canceled) {
				return importError(http.StatusRequestTimeout, "Pinterest import was cancelled")
			}
			if errors.Is(downloadContext.Err(), context.DeadlineExceeded) {
				return importError(http.StatusRequestTimeout, "Pinterest import exceeded the 30 minute limit")
			}
			detail := output.String()
			if detail == "" {
				detail = err.Error()
			}
			return importError(http.StatusBadGateway, "gallery-dl could not download this Pinterest board: %s", detail)
		case <-ticker.C:
			files, bytes, usageErr := pinterestDownloadUsage(directory)
			if usageErr != nil {
				cancel()
				<-done
				return importError(http.StatusBadGateway, "could not monitor Pinterest download: %v", usageErr)
			}
			if files > maxPinterestImages+1 || bytes > maxPinterestDownloadBytes {
				cancel()
				<-done
				return importError(http.StatusRequestEntityTooLarge, "Pinterest board exceeds the safe download limit")
			}
		case <-downloadContext.Done():
			<-done
			if errors.Is(ctx.Err(), context.Canceled) {
				return importError(http.StatusRequestTimeout, "Pinterest import was cancelled")
			}
			return importError(http.StatusRequestTimeout, "Pinterest import exceeded the 30 minute limit")
		}
	}
}

func galleryDLArguments(boardURL, directory string) []string {
	return []string{
		"--config-ignore", "--no-input",
		"--range", "1-" + strconv.Itoa(maxPinterestImages+1),
		"--filesize-max", strconv.FormatInt(maxUploadBytes, 10),
		"--filter", galleryDLImageFilter(),
		"-D", directory, "-f", "/O", boardURL,
	}
}

// Boards mix pins with Idea Pin videos that Pictogrep can never import. Asking
// gallery-dl to skip them keeps the download budget on images we can use.
func galleryDLImageFilter() string {
	extensions := make([]string, 0, len(imageExtensions))
	for extension := range imageExtensions {
		extensions = append(extensions, "'"+strings.TrimPrefix(extension, ".")+"'")
	}
	sort.Strings(extensions)
	return "extension in (" + strings.Join(extensions, ",") + ")"
}

type boundedCommandOutput struct {
	mu      sync.Mutex
	data    []byte
	maximum int
}

func (output *boundedCommandOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	written := len(data)
	if len(data) >= output.maximum {
		output.data = append(output.data[:0], data[len(data)-output.maximum:]...)
		return written, nil
	}
	if excess := len(output.data) + len(data) - output.maximum; excess > 0 {
		copy(output.data, output.data[excess:])
		output.data = output.data[:len(output.data)-excess]
	}
	output.data = append(output.data, data...)
	return written, nil
}

func (output *boundedCommandOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	detail := strings.TrimSpace(string(output.data))
	if len(detail) > 500 {
		detail = detail[len(detail)-500:]
	}
	return detail
}

func pinterestDownloadUsage(root string) (files int, bytes int64, err error) {
	// gallery-dl downloads into ".part" files and renames them when each one
	// finishes, so entries routinely disappear between reading the directory and
	// measuring them. A file that vanished mid-walk is normal progress.
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			if errors.Is(infoErr, os.ErrNotExist) {
				return nil
			}
			return infoErr
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func galleryDLBinary() (string, error) {
	names := []string{"pictogrep-gallery-dl", "gallery-dl"}
	if runtime.GOOS == "windows" {
		for index := range names {
			names[index] += ".exe"
		}
	}
	if executable, err := os.Executable(); err == nil {
		for _, name := range names {
			candidate := filepath.Join(filepath.Dir(executable), name)
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return exec.LookPath(names[len(names)-1])
}

func validatePinterestBoardURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 8192 {
		return nil, fmt.Errorf("paste a Pinterest board URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("paste a valid Pinterest board URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("Pinterest board URLs must use HTTP or HTTPS")
	}
	if parsed.Port() != "" || !pinterestHost(parsed.Hostname()) {
		return nil, fmt.Errorf("paste a URL from Pinterest")
	}
	parts := pinterestPathParts(parsed.Path)
	if len(parts) < 2 || parts[0] == "pin" || parts[0] == "ideas" || parts[0] == "search" {
		return nil, fmt.Errorf("paste a Pinterest board URL, not a Pin or profile URL")
	}
	parsed.Scheme = "https"
	parsed.Host = strings.ToLower(parsed.Hostname())
	parsed.Path = "/" + strings.Join(parts[:2], "/") + "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func pinterestHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	labels := strings.Split(host, ".")
	for index, label := range labels {
		if label != "pinterest" || index+1 >= len(labels) {
			continue
		}
		tail := labels[index+1:]
		if len(tail) == 1 && (tail[0] == "com" || len(tail[0]) == 2) {
			return true
		}
		if len(tail) == 2 && tail[0] == "co" && len(tail[1]) == 2 {
			return true
		}
	}
	return false
}

func pinterestPathParts(value string) []string {
	parts := []string{}
	for _, part := range strings.Split(strings.Trim(value, "/"), "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func pinterestBoardName(boardURL *url.URL) string {
	parts := pinterestPathParts(boardURL.Path)
	if len(parts) < 2 {
		return "Pinterest board"
	}
	return strings.ReplaceAll(strings.ReplaceAll(parts[1], "-", " "), "_", " ")
}

func pinterestDownloadedImages(root string) ([]string, error) {
	images := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() || !imageExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			return nil
		}
		images = append(images, path)
		if len(images) > maxPinterestImages {
			return fmt.Errorf("Pinterest board is too large to import safely (limit %d images)", maxPinterestImages)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("could not read gallery-dl downloads: %v", err)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("gallery-dl did not find any supported images on this public board")
	}
	sort.Strings(images)
	return images, nil
}
