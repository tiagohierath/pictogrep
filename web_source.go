package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pinterest was never the only place art lives. An artist posts on their own
// gallery, on Tumblr, on Bluesky, and following them by hand meant remembering
// to go look. A followed website works the way a feed reader does: the newest
// posts are checked for on a schedule and only what is new comes down.
//
// Two things people want are genuinely different, so they are two choices
// rather than one mode. Downloading everything already posted is how you take a
// whole gallery in one go. Downloading from now on is how you follow somebody
// without pulling ten years of back catalogue first. Asking for both is normal:
// take the archive today, keep up from here.

const (
	// A first check for a followed site takes the newest handful so the folder
	// is not empty while it waits for something new to appear.
	webFollowLimit = 20
	webSyncEvery   = 24 * time.Hour
)

type webSettings struct {
	AutoSync bool `json:"autoSync"`
}

// Following websites has its own switch. It used to ride along with the
// Pinterest one, so turning boards off quietly stopped every artist you follow
// as well, with nothing on screen saying so.
func (a *application) webSettings() webSettings {
	settings := webSettings{AutoSync: true}
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return settings
	}
	var document struct {
		Web *struct {
			AutoSync *bool `json:"autoSync"`
		} `json:"web"`
	}
	if json.Unmarshal(data, &document) == nil && document.Web != nil && document.Web.AutoSync != nil {
		settings.AutoSync = *document.Web.AutoSync
	}
	return settings
}

func (a *application) saveWebSettings(settings webSettings) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document["web"] = settings
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

func (s *server) saveWebSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AutoSync *bool `json:"autoSync"`
	}
	if err := decodeJSON(r, &request, 1<<16); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if request.AutoSync != nil {
		if err := s.app.saveWebSettings(webSettings{AutoSync: *request.AutoSync}); err != nil {
			sendError(w, http.StatusBadRequest, err)
			return
		}
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "autoSync": s.app.webSettings().AutoSync})
}

// Each followed site gets its own archive file, which is how gallery-dl knows
// what it has already fetched. The name is derived from the address so that
// unfollowing and following again picks the same one back up.
func (a *application) webArchivePath(source string) string {
	digest := sha256.Sum256([]byte(source))
	return filepath.Join(a.dataDir, "web-archives", hex.EncodeToString(digest[:16])+".sqlite3")
}

func (a *application) prepareWebArchive(source string) string {
	path := a.webArchivePath(source)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		// An archive is an optimization, not a requirement. Without it the
		// check still works, it just re-downloads what it already has.
		return ""
	}
	return path
}

func (a *application) removeWebArchive(source string) {
	_ = os.Remove(a.webArchivePath(source))
}

type webSource struct {
	URL        string `json:"url"`
	Name       string `json:"name"`
	Folder     string `json:"folder"`
	LastSyncAt int64  `json:"lastSyncAt"`
	AddedAt    int64  `json:"addedAt"`
}

func (a *application) webSourcesPath() string {
	return filepath.Join(a.dataDir, "web-sources.json")
}

func (a *application) webSources() []webSource {
	data, err := os.ReadFile(a.webSourcesPath())
	if err != nil {
		return nil
	}
	var sources []webSource
	if json.Unmarshal(data, &sources) != nil {
		return nil
	}
	kept := sources[:0]
	for _, source := range sources {
		if strings.TrimSpace(source.URL) != "" {
			kept = append(kept, source)
		}
	}
	return kept
}

func (a *application) saveWebSources(sources []webSource) error {
	if sources == nil {
		sources = []webSource{}
	}
	data, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	tmp := a.webSourcesPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.webSourcesPath())
}

// Following the same address twice updates the one entry, so a site cannot end
// up being checked two or three times over.
func (a *application) trackWebSource(source webSource) error {
	sources := a.webSources()
	for index, existing := range sources {
		if existing.URL == source.URL {
			if source.AddedAt == 0 {
				source.AddedAt = existing.AddedAt
			}
			sources[index] = source
			return a.saveWebSources(sources)
		}
	}
	return a.saveWebSources(append(sources, source))
}

func (a *application) forgetWebSource(raw string) error {
	sources := a.webSources()
	kept := make([]webSource, 0, len(sources))
	for _, source := range sources {
		if source.URL != raw {
			kept = append(kept, source)
		}
	}
	return a.saveWebSources(kept)
}

func (s *server) appWebSources(w http.ResponseWriter, _ *http.Request) {
	sources := s.app.webSources()
	if sources == nil {
		sources = []webSource{}
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "sources": sources})
}

func (s *server) forgetWebSourceRequest(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Forget string `json:"forget"`
	}
	if err := decodeJSON(r, &request, 1<<16); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(request.Forget) == "" {
		sendError(w, http.StatusBadRequest, fmt.Errorf("say which website to stop following"))
		return
	}
	if err := s.app.forgetWebSource(request.Forget); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	s.app.removeWebArchive(request.Forget)
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) importWebSource(w http.ResponseWriter, r *http.Request) {
	if !s.app.pluginEnabled("web") {
		sendError(w, http.StatusNotFound, fmt.Errorf("web import plugin is disabled"))
		return
	}
	var request struct {
		URL      string `json:"url"`
		Folder   string `json:"folder"`
		Backfill bool   `json:"backfill"`
		Follow   bool   `json:"follow"`
	}
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	sourceURL, err := validateWebSourceURL(request.URL)
	if err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if !request.Backfill && !request.Follow {
		sendError(w, http.StatusBadRequest, fmt.Errorf("choose to download the past images, to follow from now on, or both"))
		return
	}
	displayName := webSourceName(sourceURL)
	// A name that cannot become a folder has to be caught now. Finding out after
	// the download would throw away everything it just fetched.
	folder, err := webFolderName(defaultString(request.Folder, displayName))
	if err != nil {
		sendError(w, http.StatusBadRequest, fmt.Errorf("that folder name cannot be used; pick another one"))
		return
	}

	// Following without a backfill still downloads the newest few, so the folder
	// has something in it and the next check has a baseline to compare against.
	limit := 0
	if !request.Backfill {
		limit = webFollowLimit
	}

	// Deliberately not the request context: the download has to survive the
	// window that asked for it.
	ctx, cancel := context.WithCancel(context.Background())
	if !s.pinterest.start(displayName, "web", cancel) {
		cancel()
		sendError(w, http.StatusConflict, fmt.Errorf("a download is already running"))
		return
	}
	go func() {
		defer cancel()
		defer guard(func(err error) { s.pinterest.finish(nil, err) })
		result, err := s.runGalleryImport(ctx, galleryImport{
			Source: sourceURL.String(), Name: displayName, Folder: folder,
			Archive: s.app.prepareWebArchive(sourceURL.String()),
			Limit:   limit, LinkDuplicates: true,
		})
		// Only a site that downloaded at least once is worth checking again. A
		// one-time archive grab is not followed, which is the whole difference
		// between the two boxes.
		if err == nil && request.Follow {
			_ = s.app.trackWebSource(webSource{
				URL: sourceURL.String(), Name: displayName, Folder: folder,
				LastSyncAt: time.Now().Unix(), AddedAt: time.Now().Unix(),
			})
		}
		s.pinterest.finish(result, err)
	}()
	sendJSON(w, http.StatusAccepted, s.pinterest.snapshot())
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (source webSource) due(now time.Time) bool {
	return now.Sub(time.Unix(source.LastSyncAt, 0)) >= webSyncEvery
}

// syncDueWebSources checks at most one site per pass, for the same reason the
// board sync does: a download is heavy and only one can run at a time.
func (s *server) syncDueWebSources(ctx context.Context, now time.Time) bool {
	if !s.app.pluginEnabled("web") || !s.app.webSettings().AutoSync {
		return false
	}
	if s.pinterest.running() || s.app.indexing() {
		return false
	}
	for _, source := range s.app.webSources() {
		if !source.due(now) {
			continue
		}
		sourceURL, err := validateWebSourceURL(source.URL)
		if err != nil {
			// An address that stopped being valid is dropped rather than retried
			// forever.
			_ = s.app.forgetWebSource(source.URL)
			continue
		}
		// The folder is cleaned again on the way in rather than trusted. It was
		// written by this program, but it is a plain file on disk between runs,
		// and it ends up in a path join.
		folder, err := webFolderName(source.Folder)
		if err != nil {
			_ = s.app.forgetWebSource(source.URL)
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		if !s.pinterest.startAutomatic(source.Name, "web", cancel) {
			cancel()
			return false
		}
		// Recorded before the run, so a site that fails every time waits its turn
		// again instead of being retried on every pass.
		source.LastSyncAt = now.Unix()
		_ = s.app.trackWebSource(source)
		result, runErr := s.runGalleryImport(runCtx, galleryImport{
			Source: sourceURL.String(), Name: source.Name, Folder: folder,
			Archive: s.app.prepareWebArchive(source.URL),
			Limit:   webFollowLimit, LinkDuplicates: true,
			// A followed site that has posted nothing new is the ordinary case.
			AllowEmpty: true,
		})
		cancel()
		s.pinterest.finish(result, runErr)
		if runErr != nil {
			log.Printf("web-sync warning %s: %v", source.URL, runErr)
		}
		return true
	}
	return false
}

func validateWebSourceURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 8192 {
		return nil, fmt.Errorf("paste the address of a gallery, profile, or tag page")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("paste a valid web address")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("web addresses must use HTTP or HTTPS")
	}
	if parsed.Port() != "" {
		return nil, fmt.Errorf("paste a plain web address, without a port")
	}
	// The address goes to a downloader that runs as a separate program, so one
	// pointing back at this machine is refused the same way a pasted image URL
	// is.
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !publicRemoteIP(ip) {
		return nil, fmt.Errorf("local and private addresses are not allowed")
	}
	if host := strings.ToLower(parsed.Hostname()); host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, fmt.Errorf("local and private addresses are not allowed")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	return parsed, nil
}

// A site's name is the last meaningful piece of its address, which is what the
// person following it would call the folder: an artist's handle, a tag, a
// gallery title. Sites that put everything at the root fall back to the domain.
func webSourceName(sourceURL *url.URL) string {
	parts := pinterestPathParts(sourceURL.Path)
	for index := len(parts) - 1; index >= 0; index-- {
		part := strings.TrimSpace(decodeURLPart(parts[index]))
		if part == "" || strings.EqualFold(part, "gallery") || strings.EqualFold(part, "media") {
			continue
		}
		return strings.ReplaceAll(strings.ReplaceAll(part, "-", " "), "_", " ")
	}
	host := strings.TrimPrefix(strings.ToLower(sourceURL.Hostname()), "www.")
	if name, _, found := strings.Cut(host, "."); found && name != "" {
		return name
	}
	return host
}

// A handle like someone.bsky.social loses its dots on the way to a folder name
// and comes out as someonebskysocial, so the dots become separators first.
func webFolderName(value string) (string, error) {
	return collectionName(strings.ReplaceAll(value, ".", " "))
}

func decodeURLPart(value string) string {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}
