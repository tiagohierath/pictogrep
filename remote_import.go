package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type remoteImage struct {
	Data []byte
	Name string
}

type remoteImageFetcher func(context.Context, string) (remoteImage, error)

type importStatusError struct {
	Status int
	Err    error
}

func (e *importStatusError) Error() string { return e.Err.Error() }
func (e *importStatusError) Unwrap() error { return e.Err }

func importError(status int, message string, values ...any) error {
	return &importStatusError{Status: status, Err: fmt.Errorf(message, values...)}
}

func importErrorStatus(err error, fallback int) int {
	var statusError *importStatusError
	if errors.As(err, &statusError) {
		return statusError.Status
	}
	return fallback
}

func (s *server) importImageURL(w http.ResponseWriter, r *http.Request) {
	var request struct {
		URL    string `json:"url"`
		Folder string `json:"folder"`
		Source string `json:"source"`
	}
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := validateRemoteImageURL(request.URL); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if _, _, _, err := s.importDestination(request.Folder, request.Source); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	remote, err := s.remoteFetcher(r.Context(), request.URL)
	if err != nil {
		sendError(w, importErrorStatus(err, http.StatusBadGateway), err)
		return
	}
	result, status, err := s.saveImportedImage(bytes.NewReader(remote.Data), remote.Name, request.Folder, request.Source)
	if err != nil {
		sendError(w, status, err)
		return
	}
	result["downloadedFrom"] = request.URL
	sendJSON(w, http.StatusOK, result)
}

func (s *server) saveImportedImage(reader io.Reader, requestedName, folder, source string) (map[string]any, int, error) {
	return s.saveImportedImageWithOptions(reader, requestedName, folder, source, true, false, nil)
}

// saveImportedImageWithOptions lets batch importers choose whether an exact
// duplicate should be linked to the destination collection and whether a
// duplicate outside a collection is a successful no-op. Ordinary one-image
// imports retain their existing conflict behavior through saveImportedImage.
func (s *server) saveImportedImageWithOptions(reader io.Reader, requestedName, folder, source string, linkDuplicate, duplicateOK bool, digestIndex map[[32]byte]string) (map[string]any, int, error) {
	name, err := safeImageName(requestedName)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	directory, folder, source, err := s.importDestination(folder, source)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	target := uniqueFile(directory, name)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("could not create image in this folder")
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, maxUploadBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written < 1 || written > maxUploadBytes {
		_ = os.Remove(target)
		if written > maxUploadBytes {
			return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("image is larger than 100 MB")
		}
		return nil, http.StatusBadRequest, fmt.Errorf("could not save image")
	}
	if !validImageFile(target) {
		_ = os.Remove(target)
		return nil, http.StatusBadRequest, fmt.Errorf("dropped file is not a valid image")
	}
	existing, duplicate, digest, err := s.importDuplicate(target, digestIndex)
	if err != nil {
		_ = os.Remove(target)
		return nil, http.StatusBadRequest, err
	}
	if duplicate {
		_ = os.Remove(target)
		if folder != "" && linkDuplicate {
			linked, linkErr := s.linkTag(folder, existing)
			if linkErr != nil {
				return nil, http.StatusBadRequest, linkErr
			}
			return map[string]any{
				"ok": true, "name": filepath.Base(existing), "path": existing,
				"folder": folder, "source": "", "duplicate": true, "linked": linked,
			}, http.StatusOK, nil
		}
		if duplicateOK {
			return map[string]any{
				"ok": true, "name": filepath.Base(existing), "path": existing,
				"folder": folder, "source": "", "duplicate": true, "linked": false,
			}, http.StatusOK, nil
		}
		return nil, http.StatusConflict, fmt.Errorf("exact duplicate: Pictogrep kept the existing picture")
	}
	if folder != "" {
		if _, err := s.linkTag(folder, target); err != nil {
			_ = os.Remove(target)
			return nil, http.StatusBadRequest, err
		}
	}
	s.app.addPath(target)
	if digestIndex != nil {
		digestIndex[digest] = target
	}
	return map[string]any{
		"ok": true, "name": filepath.Base(target), "path": target,
		"folder": folder, "source": source,
	}, http.StatusOK, nil
}

// importDuplicate uses a batch digest index when one is available. A Pinterest
// board can contain thousands of images, so rereading every library file for
// every item would otherwise turn one import into quadratic disk I/O.
func (s *server) importDuplicate(candidate string, digestIndex map[[32]byte]string) (string, bool, [32]byte, error) {
	if digestIndex == nil {
		existing, duplicate, err := s.app.duplicatePath(candidate)
		return existing, duplicate, [32]byte{}, err
	}
	digest, err := fileDigest(candidate)
	if err != nil {
		return "", false, digest, err
	}
	existing, duplicate := digestIndex[digest]
	return existing, duplicate, digest, nil
}

func (a *application) importDigestIndex() map[[32]byte]string {
	paths, _, _ := a.snapshot()
	index := make(map[[32]byte]string, len(paths))
	for _, path := range paths {
		if digest, err := fileDigest(path); err == nil {
			if _, exists := index[digest]; !exists {
				index[digest] = path
			}
		}
	}
	return index
}

func (s *server) importDestination(folder, source string) (directory, cleanFolder, cleanSource string, err error) {
	folder = strings.TrimSpace(folder)
	source = strings.TrimSpace(source)
	if folder != "" && source != "" {
		return "", "", "", fmt.Errorf("choose one destination folder")
	}
	if folder != "" {
		folder, err = collectionName(folder)
		if err != nil {
			return "", "", "", err
		}
		return s.app.libraryDir, folder, "", nil
	}
	if source == "" {
		return s.app.libraryDir, "", "", nil
	}
	source = expandPath(source)
	info, statErr := os.Stat(source)
	if statErr != nil || !info.IsDir() {
		return "", "", "", fmt.Errorf("unknown destination folder")
	}
	resolvedSource, resolveErr := filepath.EvalSymlinks(source)
	if resolveErr != nil {
		return "", "", "", fmt.Errorf("unknown destination folder")
	}
	_, sources, _ := s.app.snapshot()
	for _, root := range sources {
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		if rootErr == nil && pathInside(resolvedSource, resolvedRoot) {
			return source, "", source, nil
		}
	}
	return "", "", "", fmt.Errorf("destination is outside the indexed library")
}

func validateRemoteImageURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 8192 {
		return nil, fmt.Errorf("drop an HTTP or HTTPS image URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid image URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only HTTP and HTTPS image URLs are supported")
	}
	port := parsed.Port()
	if port != "" && !((parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443")) {
		return nil, fmt.Errorf("image URL must use the standard HTTP or HTTPS port")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !publicRemoteIP(ip) {
		return nil, fmt.Errorf("local and private image URLs are not allowed")
	}
	return parsed, nil
}

func publicRemoteIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	if address.Is4() {
		carrierNAT := netip.MustParsePrefix("100.64.0.0/10")
		if carrierNAT.Contains(address) {
			return false
		}
	}
	return true
}

func publicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, candidate := range addresses {
		if !publicRemoteIP(candidate.IP) {
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, "tcp", net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("image host does not resolve to a public address")
}

func downloadRemoteImage(ctx context.Context, raw string) (remoteImage, error) {
	parsed, err := validateRemoteImageURL(raw)
	if err != nil {
		return remoteImage{}, importError(http.StatusBadRequest, "%v", err)
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           publicDialContext,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: 12 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		IdleConnTimeout:       15 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   25 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many image redirects")
			}
			_, err := validateRemoteImageURL(request.URL.String())
			return err
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return remoteImage{}, importError(http.StatusBadRequest, "invalid image URL")
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,image/*;q=0.8")
	request.Header.Set("User-Agent", "Pictogrep/0.4")
	response, err := client.Do(request)
	if err != nil {
		return remoteImage{}, importError(http.StatusBadGateway, "could not download image: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return remoteImage{}, importError(http.StatusBadGateway, "image server returned %s", response.Status)
	}
	if response.ContentLength > maxUploadBytes {
		return remoteImage{}, importError(http.StatusRequestEntityTooLarge, "image is larger than 100 MB")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxUploadBytes+1))
	if err != nil {
		return remoteImage{}, importError(http.StatusBadGateway, "could not download image")
	}
	if len(data) == 0 {
		return remoteImage{}, importError(http.StatusBadRequest, "image URL returned an empty file")
	}
	if len(data) > maxUploadBytes {
		return remoteImage{}, importError(http.StatusRequestEntityTooLarge, "image is larger than 100 MB")
	}
	name, err := remoteImageName(response, parsed, data)
	if err != nil {
		return remoteImage{}, importError(http.StatusBadRequest, "%v", err)
	}
	return remoteImage{Data: data, Name: name}, nil
}

func remoteImageName(response *http.Response, parsed *url.URL, data []byte) (string, error) {
	extension := detectedImageExtension(data)
	if extension == "" {
		return "", fmt.Errorf("URL did not return a supported JPG, PNG, GIF, or WebP image")
	}
	name := ""
	if _, parameters, err := mime.ParseMediaType(response.Header.Get("Content-Disposition")); err == nil {
		name = parameters["filename"]
	}
	if name == "" {
		name = filepath.Base(parsed.Path)
	}
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	if stem == "" || stem == "." || stem == "/" {
		stem = "dropped-image"
	}
	return safeImageName(stem + extension)
}

func detectedImageExtension(data []byte) string {
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		switch format {
		case "jpeg":
			return ".jpg"
		case "png":
			return ".png"
		case "gif":
			return ".gif"
		}
	}
	if len(data) >= 20 && validWebPHeader(data[:20], int64(len(data))) {
		return ".webp"
	}
	return ""
}
