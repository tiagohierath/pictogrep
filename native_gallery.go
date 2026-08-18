package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Downloading without gallery-dl.
//
// gallery-dl is a Python program, and Pictogrep shells out to it. That is fine
// on a desktop, where Python is either already there or a package away, and
// impossible inside an Android app: there is no interpreter in the APK and no
// way to install one. Bundling CPython would have answered it, at fifteen to
// twenty five megabytes per architecture, an interpreter to start on every
// import, and a second toolchain to keep alive forever.
//
// So the two addresses that matter are implemented here instead, against the
// same galleryDLRunner interface the rest of the import already speaks: a
// Pinterest board, and an ordinary web page with pictures on it. Everything
// downstream, the duplicate check, the folder manifest, the progress reporting,
// cannot tell which downloader ran.
//
// What this deliberately is not: gallery-dl's several hundred site extractors,
// its logins, or its cookie handling. Where the real thing is installed it is
// still preferred, and this is what answers when it is not.

const (
	// A page or an API answer. Not the pictures, which are streamed.
	nativeGalleryPageTimeout = 30 * time.Second
	// One picture. Generous, because a phone on mobile data is slow rather
	// than broken.
	nativeGalleryImageTimeout = 5 * time.Minute
	// How many pages of a board to walk before calling it enough. Pinterest
	// hands back 25 pins a page, so this is 8000 pins, well past the point
	// where maxPinterestImages stops the import anyway.
	nativeGalleryMaxPages = 320
	// What to send as a browser. Pinterest serves a different, useless page to
	// something that admits to being a script, and some hosts refuse an empty
	// user agent outright.
	nativeGalleryUserAgent = "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
)

// nativeGalleryImage is one picture worth downloading, before it is downloaded.
type nativeGalleryImage struct {
	// URL is where the bytes are.
	URL string
	// ID is what the archive remembers, so a board checked every week only
	// downloads what is new. Stable across runs and independent of the URL,
	// which Pinterest rewrites with new CDN hostnames.
	ID string
}

// runNativeGallery is a galleryDLRunner. It puts image files in
// download.Directory and lets the caller do the importing, which is the whole
// contract gallery-dl was held to.
func runNativeGallery(ctx context.Context, download galleryDLRequest) error {
	ctx, cancel := context.WithTimeout(ctx, pinterestDownloadTimeout)
	defer cancel()

	target, err := url.Parse(download.URL)
	if err != nil || !strings.HasPrefix(target.Scheme, "http") {
		return importError(http.StatusBadRequest, "that does not look like a web address")
	}

	limit := download.Limit
	if limit <= 0 || limit > maxPinterestImages {
		limit = maxPinterestImages
	}

	images, err := nativeGalleryImages(ctx, target, limit)
	if err != nil {
		return err
	}

	// Everything already downloaded on an earlier check, skipped before a
	// single request goes out for it.
	seen, archivePath := readNativeArchive(download.Archive)
	fresh := images[:0:0]
	for _, image := range images {
		if !seen[image.ID] {
			fresh = append(fresh, image)
		}
	}
	if len(fresh) > limit {
		fresh = fresh[:limit]
	}

	saved := 0
	var budget int64
	for index, image := range fresh {
		if ctx.Err() != nil {
			break
		}
		name := nativeGalleryFilename(image, index)
		written, err := downloadOneImage(ctx, image.URL, filepath.Join(download.Directory, name))
		if err != nil {
			// One picture that will not download is not a failed import. A
			// board always has a few dead pins on it, and the alternative is
			// losing the other nine hundred to one of them.
			continue
		}
		budget += written
		saved++
		appendNativeArchive(archivePath, image.ID)
		if budget >= maxPinterestDownloadBytes {
			break
		}
	}

	// Nothing new is a successful check of a board that has not changed, which
	// is the ordinary result of following something. Nothing at all, on a first
	// look, is the caller's error to raise: it knows whether an empty download
	// is allowed here and this does not.
	if saved > 0 {
		log.Printf("downloaded %d pictures from %s", saved, target.Hostname())
	}
	return nil
}

// nativeGalleryImages fetches the address once and decides what it is from what
// came back, rather than from a pattern matched against the address. Pinterest
// has a site per country, hands out pin.it short links that redirect, and puts
// the same state blob on a board, a profile and a search; all of them arrive
// here as "a page that carries Pinterest state", which is the thing actually
// worth branching on.
func nativeGalleryImages(ctx context.Context, target *url.URL, limit int) ([]nativeGalleryImage, error) {
	body, final, err := fetchTextFrom(ctx, target, nil)
	if err != nil {
		return nil, importError(http.StatusBadGateway, "could not open that page: %v", err)
	}
	if state := pinterestStateScript.FindStringSubmatch(body); state != nil {
		return pinterestBoardImages(ctx, final, state[1], limit)
	}
	if isPinterestHost(final.Hostname()) {
		return nil, importError(http.StatusBadGateway,
			"Pinterest did not return a board here. A board address looks like "+
				"pinterest.com/someone/a-board/, and a private board cannot be read.")
	}
	return webPageImages(final, body, limit)
}

func isPinterestHost(host string) bool {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	return host == "pinterest.com" || strings.HasSuffix(host, ".pinterest.com") ||
		host == "pin.it"
}

// ---------------------------------------------------------------------------
// Pinterest
// ---------------------------------------------------------------------------

// Pinterest renders its first screen on the server and ships the state that
// produced it in a script tag, so the first page of pins arrives with the HTML
// and costs nothing extra. Everything after that comes from the same JSON
// endpoint the site's own scroll handler calls.
var pinterestStateScript = regexp.MustCompile(
	`(?s)<script[^>]+id="__PWS_DATA__"[^>]*>(.*?)</script>`)

// pinterestBoardImages reads the state blob that came with the page and then
// pages through the rest of the board.
func pinterestBoardImages(ctx context.Context, board *url.URL, blob string, limit int) ([]nativeGalleryImage, error) {
	var state struct {
		Props struct {
			InitialReduxState struct {
				Pins   map[string]json.RawMessage `json:"pins"`
				Boards map[string]struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"boards"`
			} `json:"initialReduxState"`
		} `json:"props"`
	}
	if err := json.Unmarshal([]byte(blob), &state); err != nil {
		return nil, importError(http.StatusBadGateway, "Pinterest sent a board Pictogrep could not read")
	}

	images := []nativeGalleryImage{}
	seen := map[string]bool{}
	for _, raw := range state.Props.InitialReduxState.Pins {
		if image, ok := pinterestPinImage(raw, board); ok && !seen[image.ID] {
			seen[image.ID] = true
			images = append(images, image)
		}
	}
	// A map has no order and an import that changes its mind about which
	// pictures are "the newest few" every run would download the board twice.
	sort.Slice(images, func(a, b int) bool { return images[a].ID < images[b].ID })

	boardID := ""
	for _, entry := range state.Props.InitialReduxState.Boards {
		if entry.ID != "" {
			boardID = entry.ID
			break
		}
	}
	if boardID == "" {
		// A pin page, a search page, or a profile: whatever was on the first
		// screen is all there is to have.
		return images, nil
	}

	bookmark := ""
	for page := 0; page < nativeGalleryMaxPages && len(images) < limit; page++ {
		next, nextBookmark, err := pinterestBoardPage(ctx, board, boardID, bookmark)
		if err != nil {
			// Losing pagination is losing the rest of the board, not the part
			// already in hand.
			break
		}
		for _, image := range next {
			if !seen[image.ID] {
				seen[image.ID] = true
				images = append(images, image)
			}
		}
		if nextBookmark == "" || nextBookmark == "-end-" || nextBookmark == bookmark {
			break
		}
		bookmark = nextBookmark
	}
	if len(images) > limit {
		images = images[:limit]
	}
	return images, nil
}

// pinterestBoardPage asks for one page of a board the way the site's own
// scrolling does.
func pinterestBoardPage(ctx context.Context, board *url.URL, boardID, bookmark string) ([]nativeGalleryImage, string, error) {
	options := map[string]any{"board_id": boardID, "page_size": 25}
	if bookmark != "" {
		options["bookmarks"] = []string{bookmark}
	}
	payload, err := json.Marshal(map[string]any{"options": options, "context": map[string]any{}})
	if err != nil {
		return nil, "", err
	}

	// The same host the board came from. Pinterest runs a site per country and
	// a board opened on br.pinterest.com pages against br.pinterest.com; asking
	// the .com for it answers about a board that is not the one being read.
	endpoint := &url.URL{
		Scheme: board.Scheme, Host: board.Host, Path: "/resource/BoardFeedResource/get/",
		RawQuery: url.Values{
			"source_url": {board.EscapedPath()},
			"data":       {string(payload)},
		}.Encode(),
	}
	body, _, err := fetchText(ctx, endpoint.String(), map[string]string{
		"Accept":                  "application/json, text/javascript, */*, q=0.01",
		"X-Requested-With":        "XMLHttpRequest",
		"X-Pinterest-PWS-Handler": "www/[username]/[slug].js",
		"Referer":                 board.String(),
	})
	if err != nil {
		return nil, "", err
	}

	var answer struct {
		ResourceResponse struct {
			Data     []json.RawMessage `json:"data"`
			Bookmark string            `json:"bookmark"`
		} `json:"resource_response"`
		Resource struct {
			Options struct {
				Bookmarks []string `json:"bookmarks"`
			} `json:"options"`
		} `json:"resource"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		return nil, "", err
	}

	images := []nativeGalleryImage{}
	for _, raw := range answer.ResourceResponse.Data {
		if image, ok := pinterestPinImage(raw, board); ok {
			images = append(images, image)
		}
	}
	next := answer.ResourceResponse.Bookmark
	if next == "" && len(answer.Resource.Options.Bookmarks) > 0 {
		next = answer.Resource.Options.Bookmarks[0]
	}
	return images, next, nil
}

// pinterestPinImage reads one pin, in either of the two shapes Pinterest uses:
// the one in the page's own state and the one the feed endpoint returns. They
// agree on everything that matters here.
func pinterestPinImage(raw json.RawMessage, base *url.URL) (nativeGalleryImage, bool) {
	var pin struct {
		ID     string `json:"id"`
		Images map[string]struct {
			URL    string `json:"url"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"images"`
		// An Idea Pin is a video story. There is nothing here to import.
		StoryPinData json.RawMessage `json:"story_pin_data"`
	}
	if err := json.Unmarshal(raw, &pin); err != nil || pin.ID == "" {
		return nativeGalleryImage{}, false
	}

	// "orig" is the picture as it was pinned. The rest are thumbnails, and the
	// largest of them is the fallback when a pin has no original.
	best, bestPixels := "", 0
	for key, image := range pin.Images {
		if image.URL == "" {
			continue
		}
		if key == "orig" {
			best = image.URL
			break
		}
		if pixels := image.Width * image.Height; pixels > bestPixels {
			best, bestPixels = image.URL, pixels
		}
	}
	// Pinterest writes image addresses three ways depending on the endpoint:
	// absolute, protocol relative (//i.pinimg.com/...), and occasionally rooted
	// at the site. Resolving against the page they came from turns all three
	// into something that can be fetched.
	resolved, err := base.Parse(strings.TrimSpace(best))
	if err != nil || !strings.HasPrefix(resolved.Scheme, "http") || !looksLikeImageURL(resolved.String()) {
		return nativeGalleryImage{}, false
	}
	return nativeGalleryImage{URL: resolved.String(), ID: "pinterest:" + pin.ID}, true
}

// ---------------------------------------------------------------------------
// Any other page
// ---------------------------------------------------------------------------

var (
	htmlImageSource = regexp.MustCompile(`(?i)<img[^>]+src\s*=\s*["']([^"']+)["']`)
	htmlImageSet    = regexp.MustCompile(`(?i)srcset\s*=\s*["']([^"']+)["']`)
	htmlLink        = regexp.MustCompile(`(?i)<a[^>]+href\s*=\s*["']([^"']+)["']`)
	htmlMetaImage   = regexp.MustCompile(
		`(?i)<meta[^>]+(?:property|name)\s*=\s*["'](?:og:image|twitter:image)(?::src)?["'][^>]+content\s*=\s*["']([^"']+)["']`)
)

// webPageImages takes an ordinary page and finds the pictures on it: what it
// shows, what it links to, and what it says it is when someone shares it.
//
// Deliberately one page deep. Following links would turn "import this page"
// into a crawler, and a crawler pointed at the wrong address downloads a
// website.
func webPageImages(page *url.URL, body string, limit int) ([]nativeGalleryImage, error) {
	candidates := []string{}
	for _, match := range htmlMetaImage.FindAllStringSubmatch(body, -1) {
		candidates = append(candidates, match[1])
	}
	for _, match := range htmlLink.FindAllStringSubmatch(body, -1) {
		candidates = append(candidates, match[1])
	}
	// A srcset is a list of the same picture at several sizes. The last entry
	// is conventionally the largest, and taking the whole list would import the
	// same picture five times for the duplicate check to throw away.
	for _, match := range htmlImageSet.FindAllStringSubmatch(body, -1) {
		parts := strings.Split(match[1], ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		if fields := strings.Fields(last); len(fields) > 0 {
			candidates = append(candidates, fields[0])
		}
	}
	for _, match := range htmlImageSource.FindAllStringSubmatch(body, -1) {
		candidates = append(candidates, match[1])
	}

	images := []nativeGalleryImage{}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		absolute, err := page.Parse(strings.TrimSpace(html.UnescapeString(candidate)))
		if err != nil || !strings.HasPrefix(absolute.Scheme, "http") {
			continue
		}
		// Tracking pixels and sprite sheets arrive as data: URLs or as .svg,
		// neither of which the library can hold.
		if !looksLikeImageURL(absolute.String()) {
			continue
		}
		address := absolute.String()
		if seen[address] {
			continue
		}
		seen[address] = true
		images = append(images, nativeGalleryImage{URL: address, ID: "web:" + address})
		if len(images) >= limit {
			break
		}
	}
	if len(images) == 0 {
		return nil, importError(http.StatusBadRequest,
			"there are no pictures on that page Pictogrep can download")
	}
	return images, nil
}

// ---------------------------------------------------------------------------
// Fetching
// ---------------------------------------------------------------------------

// fetchTextFrom is fetchText for a caller that already parsed the address and
// wants the final one back in the same shape.
func fetchTextFrom(ctx context.Context, address *url.URL, headers map[string]string) (string, *url.URL, error) {
	return fetchText(ctx, address.String(), headers)
}

func nativeGalleryClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// fetchText returns the body and the address it ended up at, which is not
// always the one asked for: a pin.it link is a redirect into pinterest.com, and
// every relative link on a page is relative to where the page actually came
// from rather than to where it was requested.
func fetchText(ctx context.Context, address string, headers map[string]string) (string, *url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", nil, err
	}
	request.Header.Set("User-Agent", nativeGalleryUserAgent)
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := nativeGalleryClient(nativeGalleryPageTimeout).Do(request)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("the site answered %s", response.Status)
	}
	// A page is text. Anything enormous here is a site answering a page request
	// with a file, and reading it into memory on a phone is how the app dies.
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return "", nil, err
	}
	final := response.Request.URL
	if final == nil {
		final = request.URL
	}
	return string(body), final, nil
}

// downloadOneImage streams one picture to disk and returns how much of it
// arrived. A file that turns out not to be a picture, or to be larger than the
// library accepts, is removed rather than left for the importer to trip over.
func downloadOneImage(ctx context.Context, address, destination string) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("User-Agent", nativeGalleryUserAgent)
	response, err := nativeGalleryClient(nativeGalleryImageTimeout).Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the site answered %s", response.Status)
	}
	if kind := response.Header.Get("Content-Type"); kind != "" &&
		!strings.HasPrefix(strings.ToLower(kind), "image/") {
		return 0, errors.New("that address is not a picture")
	}
	if response.ContentLength > maxUploadBytes {
		return 0, errors.New("that picture is too large for the library")
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return 0, err
	}
	file, err := os.Create(destination)
	if err != nil {
		return 0, err
	}
	// One more byte than the limit, so a file that is exactly at it is kept and
	// one over it is caught rather than silently truncated into a broken image.
	written, err := io.Copy(file, io.LimitReader(response.Body, maxUploadBytes+1))
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil || written == 0 || written > maxUploadBytes {
		_ = os.Remove(destination)
		if err == nil {
			err = errors.New("that picture is too large for the library")
		}
		return 0, err
	}
	return written, nil
}

// ---------------------------------------------------------------------------
// Remembering what has already been downloaded
// ---------------------------------------------------------------------------

// The archive is one id per line. gallery-dl keeps a SQLite database at the
// path it is given, so this writes alongside it under a different name rather
// than into it: two downloaders sharing one file would corrupt it, and a
// library that was following sites before this existed keeps its history with
// whichever downloader wrote it.
func nativeArchivePath(archive string) string {
	if archive == "" {
		return ""
	}
	return archive + ".native"
}

func readNativeArchive(archive string) (map[string]bool, string) {
	path := nativeArchivePath(archive)
	seen := map[string]bool{}
	if path == "" {
		return seen, ""
	}
	file, err := os.Open(path)
	if err != nil {
		return seen, path
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			seen[line] = true
		}
	}
	return seen, path
}

func appendNativeArchive(path, id string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(id + "\n")
}

// ---------------------------------------------------------------------------
// Names
// ---------------------------------------------------------------------------

// nativeGalleryFilename is what the picture is called on the way in. The
// importer renames everything it keeps, so this only has to be unique within
// one download and keep the extension the importer reads.
func nativeGalleryFilename(image nativeGalleryImage, index int) string {
	extension := strings.ToLower(filepath.Ext(imagePathOf(image.URL)))
	if !imageExtensions[extension] {
		extension = ".jpg"
	}
	return fmt.Sprintf("%05d%s", index, extension)
}

// imagePathOf is the path part of an address, with the query string dropped:
// CDNs routinely end an address with ?w=640, which is not part of the filename.
func imagePathOf(address string) string {
	if parsed, err := url.Parse(address); err == nil {
		return parsed.Path
	}
	if cut := strings.IndexAny(address, "?#"); cut >= 0 {
		return address[:cut]
	}
	return address
}

func looksLikeImageURL(address string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(imagePathOf(address)))]
}
