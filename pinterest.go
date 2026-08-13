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
)

type galleryDLRunner func(context.Context, string, string) error

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

	downloadDir, err := os.MkdirTemp("", "pictogrep-pinterest-")
	if err != nil {
		sendError(w, http.StatusInternalServerError, fmt.Errorf("could not prepare Pinterest import"))
		return
	}
	defer os.RemoveAll(downloadDir)
	if err := s.galleryDL(r.Context(), boardURL.String(), downloadDir); err != nil {
		sendError(w, importErrorStatus(err, http.StatusBadGateway), err)
		return
	}
	images, err := pinterestDownloadedImages(downloadDir)
	if err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}

	boardName := pinterestBoardName(boardURL)
	folder := ""
	if request.Mode == "board" {
		folder, err = collectionName(boardName)
		if err != nil {
			sendError(w, http.StatusBadRequest, fmt.Errorf("Pinterest board name cannot be used as a folder"))
			return
		}
	}
	digestIndex := s.app.importDigestIndex()
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
	for _, imagePath := range images {
		file, openErr := os.Open(imagePath)
		if openErr != nil {
			failed++
			lastError = "could not read a downloaded Pinterest image"
			continue
		}
		result, _, saveErr := s.saveImportedImageWithOptions(
			file, filepath.Base(imagePath), "", "", false, true, digestIndex,
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
			if folder != "" && !request.SkipExisting && !collectionSet[path] {
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
	if folder != "" {
		directory := filepath.Join(s.app.tagsDir, folder)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			sendError(w, http.StatusBadRequest, fmt.Errorf("could not create Pinterest board folder: %v", err))
			return
		}
		if err := writeTagManifest(directory, collectionPaths); err != nil {
			sendError(w, http.StatusBadRequest, fmt.Errorf("could not organize Pinterest board folder: %v", err))
			return
		}
	}
	sendJSON(w, http.StatusOK, map[string]any{
		"ok": true, "board": boardName, "boardUrl": boardURL.String(), "folder": folder,
		"total": len(images), "imported": imported, "skipped": skipped,
		"linked": linked, "failed": failed, "lastError": lastError,
	})
}

func runGalleryDL(ctx context.Context, boardURL, directory string) error {
	binary, err := galleryDLBinary()
	if err != nil {
		return importError(http.StatusServiceUnavailable, "gallery-dl is required for Pinterest imports; install gallery-dl and restart Pictogrep")
	}
	downloadContext, cancel := context.WithTimeout(ctx, pinterestDownloadTimeout)
	defer cancel()
	command := exec.CommandContext(downloadContext, binary, galleryDLArguments(boardURL, directory)...)
	output := &boundedCommandOutput{maximum: maxGalleryDLLogBytes}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		return importError(http.StatusBadGateway, "gallery-dl could not start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(250 * time.Millisecond)
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
		"-D", directory, "-f", "/O", boardURL,
	}
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
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
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
