package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"log"
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
	"sync"
	"time"
)

const (
	maxUploadBytes           = 100 << 20
	maxDecodedImagePixels    = 25_000_000
	maxDecodedImageDimension = 16_384
)

type server struct {
	app           *application
	practice      []byte
	remoteFetcher remoteImageFetcher
	galleryDL     galleryDLRunner
	pinterest     pinterestImport
	// The automatic check reaches GitHub and can rewrite the running binary, so
	// both halves are swappable and the tests never touch either.
	checkUpdate func() (updateState, error)
	applyUpdate func(updateState) error
	updates     autoUpdater
	// Rebuilt from the library whenever it changes. See imageIndex.
	index imageIndex
	// nil until attachSync runs, which loads a device identity from disk. A
	// server under test, or one whose data directory is unwritable, works fine
	// without it: sync is additive to a working library, never a dependency of
	// one.
	sync *syncServer
}

// attachSync loads this device's identity and paired peers, but does not open
// the sync port: nothing listens until the user has opened "Connect phone" or
// this device has at least one paired peer to talk to. A machine that has
// never used sync should not be found by one that is scanning the network for
// open ports.
func (s *server) attachSync(deviceName string) error {
	sync, err := newSyncServer(s, deviceName)
	if err != nil {
		return err
	}
	s.sync = sync
	// Sending does not need a port of its own: a phone pushes outward and never
	// listens, so the outbox runs whether or not this device answers the
	// network. It costs nothing where nothing is paired.
	sync.outbox.start()
	if len(sync.peers.all()) > 0 {
		return sync.start()
	}
	return nil
}

type imageRecord struct {
	ID     string   `json:"id"`
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
	return &server{
		app: app, practice: practice, remoteFetcher: downloadRemoteImage,
		galleryDL: downloadGallery, checkUpdate: checkForUpdate, applyUpdate: installUpdate,
	}, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /practice", s.practicePage)
	mux.HandleFunc("GET /licenses", s.licenses)
	mux.HandleFunc("GET /assets/{name}", s.asset)
	mux.HandleFunc("GET /i18n/i18n.js", s.i18nScript)
	mux.HandleFunc("GET /i18n/locales/{name}", s.locale)
	mux.HandleFunc("GET /api/app/state", s.appState)
	mux.HandleFunc("GET /api/app/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /api/app/activity", s.appActivity)
	mux.HandleFunc("POST /api/app/log", s.appLog)
	mux.HandleFunc("POST /api/app/plugins", s.savePlugin)
	mux.HandleFunc("POST /api/app/premium", s.setPremium)
	mux.HandleFunc("GET /api/app/plugins/wikimedia/search", s.wikimediaSearch)
	mux.HandleFunc("GET /api/app/plugins/calendar", s.calendarView)
	// Not registered where the build has no board importer, so the app answers
	// 404 rather than "the downloader is missing". The panel is gone from the
	// page there (see rewriteForPhone), and an endpoint with no caller is an
	// endpoint that can still be called.
	if offersPinterest {
		mux.HandleFunc("POST /api/app/plugins/pinterest/import", s.importPinterestBoard)
		mux.HandleFunc("GET /api/app/plugins/pinterest/import", s.pinterestImportStatus)
		mux.HandleFunc("DELETE /api/app/plugins/pinterest/import", s.cancelPinterestImport)
	}
	mux.HandleFunc("POST /api/app/settings/storage", s.saveStorageSettings)
	mux.HandleFunc("POST /api/app/settings/language", s.saveStorageSettings)
	mux.HandleFunc("POST /api/app/settings/theme", s.saveThemeSettings)
	mux.HandleFunc("POST /api/app/settings/browser", s.saveBrowserSettings)
	mux.HandleFunc("POST /api/app/settings/indexing", s.saveIndexingSettings)
	mux.HandleFunc("GET /api/app/update", s.appUpdate)
	mux.HandleFunc("POST /api/app/update", s.installAppUpdate)
	mux.HandleFunc("GET /api/app/images", s.appImages)
	mux.HandleFunc("GET /api/app/images/{id}", s.appImage)
	mux.HandleFunc("DELETE /api/app/images/{id}", s.deleteImage)
	mux.HandleFunc("GET /api/app/folders", s.appFolders)
	mux.HandleFunc("POST /api/app/folders/view", s.saveFolderView)
	mux.HandleFunc("GET /api/app/folders/export", s.exportFolder)
	mux.HandleFunc("GET /api/app/search", s.appSearch)
	mux.HandleFunc("GET /api/app/related/{id}", s.appRelated)
	mux.HandleFunc("GET /api/app/canvas", s.appCanvas)
	mux.HandleFunc("POST /api/app/canvas", s.saveAppCanvas)
	mux.HandleFunc("GET /api/app/boards", s.appBoards)
	mux.HandleFunc("POST /api/app/index", s.appIndex)
	mux.HandleFunc("GET /api/app/browse", s.appBrowse)
	mux.HandleFunc("POST /api/app/folders/forget", s.forgetFolder)
	if offersPinterest {
		mux.HandleFunc("GET /api/app/plugins/pinterest/boards", s.appPinterestBoards)
		mux.HandleFunc("POST /api/app/settings/pinterest", s.savePinterestSettings)
	}
	mux.HandleFunc("POST /api/app/plugins/web/import", s.importWebSource)
	mux.HandleFunc("POST /api/app/settings/web", s.saveWebSettings)
	mux.HandleFunc("POST /api/app/settings/update", s.saveUpdateSettings)
	mux.HandleFunc("GET /api/app/plugins/web/sources", s.appWebSources)
	mux.HandleFunc("POST /api/app/plugins/web/sources", s.forgetWebSourceRequest)
	mux.HandleFunc("POST /api/app/onboarding", s.saveOnboarding)
	mux.HandleFunc("GET /api/app/plugins/installed", s.pluginsInstalled)
	mux.HandleFunc("GET /plugin/{id}/{path...}", s.servePlugin)
	mux.HandleFunc("GET /api/plugins/{id}/storage", s.handlePluginStorage)
	mux.HandleFunc("POST /api/plugins/{id}/storage", s.handlePluginStorage)
	// LAN sync with a paired phone or desktop. See sync_controls.go for what
	// each one does and why it exists as a button rather than only happening on
	// its own.
	mux.HandleFunc("GET /api/app/sync", s.syncState)
	mux.HandleFunc("POST /api/app/sync/listen", s.startSyncListening)
	mux.HandleFunc("POST /api/app/sync/pause", s.pauseSync)
	mux.HandleFunc("POST /api/app/sync/pairing", s.beginSyncPairing)
	mux.HandleFunc("POST /api/app/sync/now", s.syncNow)
	mux.HandleFunc("DELETE /api/app/sync/peers/{id}", s.forgetSyncPeer)
	mux.HandleFunc("POST /api/app/sync/pair-with", s.pairWithScanned)
	mux.HandleFunc("POST /api/app/sync/rediscovered", s.peerRediscovered)
	mux.HandleFunc("POST /api/app/sync/auto-send", s.setSyncAutoSend)
	mux.HandleFunc("POST /api/app/upload", s.appUpload)
	mux.HandleFunc("POST /api/app/import-url", s.importImageURL)
	mux.HandleFunc("POST /api/app/tags", s.appTags)
	mux.HandleFunc("GET /api/app/ai", s.aiState)
	mux.HandleFunc("POST /api/app/ai/embeddings", s.aiEmbeddings)
	mux.HandleFunc("POST /api/app/ai/reindex", s.rebuildAIIndex)
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

func (s *server) appActivity(w http.ResponseWriter, _ *http.Request) {
	if s.app.usage != nil {
		s.app.usage.markActive()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) appLog(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Level   string `json:"level"`
		Event   string `json:"event"`
		Message string `json:"message"`
		Path    string `json:"path"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	clean := func(value string, maximum int) string {
		value = strings.Join(strings.Fields(value), " ")
		if len(value) > maximum {
			value = value[:maximum]
		}
		return value
	}
	level := clean(request.Level, 16)
	if level == "" {
		level = "info"
	}
	event := clean(request.Event, 80)
	message := clean(request.Message, 1000)
	path := clean(request.Path, 4096)
	log.Printf("web %s %s: %s path=%q", level, event, message, path)
	w.WriteHeader(http.StatusNoContent)
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
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// Cross-origin isolation is the only thing that turns on SharedArrayBuffer,
		// and without it the bundled ONNX Runtime pins itself to a single wasm
		// thread no matter how many cores the machine has. Preparing pictures was
		// therefore using one core on every Pictogrep ever shipped.
		//
		// "credentialless" rather than "require-corp" because the Commons plugin
		// loads its thumbnails straight from upload.wikimedia.org: require-corp
		// would block every one of them, while credentialless only drops the
		// credentials those public images never needed. A browser that does not
		// know the value ignores it, stays unisolated, and behaves as it always has.
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "credentialless")
		// Nothing here is meant to be pulled into a page from somewhere else. Any
		// website can point an <img> at 127.0.0.1 and watch which ones load, which
		// is how a remote page learns what is in a local library.
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		if !isLoopbackAuthority(r.Host) {
			sendError(w, http.StatusForbidden, fmt.Errorf("Pictogrep only accepts local requests"))
			return
		}
		if !hasAccessToken(r) {
			rejectUntrustedCaller(w)
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

// The page, built once rather than read and rewritten per request.
var homePage = sync.OnceValues(func() ([]byte, error) {
	data, err := embeddedFiles.ReadFile("web/index.html")
	if err != nil {
		return nil, err
	}
	return phoneShell(data), nil
})

// The same page with the light theme stamped on <html>, also built once. The
// theme has to be in the markup the browser first parses: a page that arrives
// dark and is corrected by script paints the wrong colour for a frame, and
// avoiding that flash is the whole reason this is an attribute and not a class
// app.js adds. Dark needs no variant because it is what the stylesheets are.
var lightHomePage = sync.OnceValues(func() ([]byte, error) {
	page, err := homePage()
	if err != nil {
		return nil, err
	}
	return stampLightTheme(page), nil
})

// Matches the opening tag by prefix, so it works on the desktop page and on
// the phone one, where phoneShell has already added class="is-phone".
func stampLightTheme(page []byte) []byte {
	return bytes.Replace(page, []byte(`<html lang="en"`), []byte(`<html lang="en" data-theme="light"`), 1)
}

// phoneShell turns the shared page into the Android one.
//
// On a phone the interface is Material 3, which is a different stylesheet and
// a little furniture the desktop has no use for. Which one to serve is decided
// here, in the markup, rather than by JavaScript adding a class once the first
// state request comes back: by then the desktop layout has already been
// painted, and every launch of the app starts with a visible lurch out of it.
//
// runsOnPhone is a build constant, so the desktop binary compiles this away to
// a page that is byte for byte the file on disk.
func phoneShell(page []byte) []byte {
	if !runsOnPhone {
		return page
	}
	return rewriteForPhone(page)
}

// Split out from phoneShell so a test can run it on any build. On the desktop
// one, runsOnPhone is false and phoneShell alone would never reach this.
func rewriteForPhone(page []byte) []byte {
	for _, swap := range []struct{ from, to string }{
		{`<html lang="en">`, `<html lang="en" class="is-phone">`},
		{
			`<link rel="stylesheet" href="/assets/app.css">`,
			`<link rel="stylesheet" href="/assets/app.css">` + "\n" +
				`  <link rel="stylesheet" href="/assets/m3.css">`,
		},
		{
			`<script src="/assets/app.js" defer></script>`,
			`<script src="/assets/app.js" defer></script>` + "\n" +
				`  <script src="/assets/phone.js" defer></script>`,
		},
	} {
		if !bytes.Contains(page, []byte(swap.from)) {
			// The page is embedded at build time, so a miss here is a change to
			// index.html that silently drops the phone interface. Loud, and in
			// front of a test, rather than a launch that looks like the desktop.
			panic("phoneShell: index.html no longer contains " + swap.from)
		}
		page = bytes.Replace(page, []byte(swap.from), []byte(swap.to), 1)
	}
	return withoutPinterest(page)
}

// Which parts of the page belong to the board importer, and what kind of
// element each one is.
var pinterestParts = []struct{ marker, tag string }{
	{`id="pinterestSection"`, "section"},    // the panel itself
	{`id="showPinterest"`, "button"},        // the way into it, in the menu
	{`id="emptyPinterest"`, "p"},            // the offer on an empty library
	{`id="emptyPinterestPhone"`, "button"},  // the same offer, phone shaped
	{`id="pinterestPluginToggle"`, "label"}, // both settings rows
	{`id="pinterestAutoSyncToggle"`, "label"},
	{`id="pinterestBoardList"`, "div"}, // the boards a desktop is following
}

// withoutPinterest takes the board importer out of the page an app build
// serves. See platform_mobile.go for why this build does not have one.
//
// Emptied rather than deleted, because app.js is the same file on both
// platforms and writes to these elements as it renders, hiding the panel and
// setting the toggles. An element that is not there at all is a TypeError on
// the first state update, which takes the whole interface down with it. What is
// left is a skeleton of ids with nothing inside: no heading, no board field,
// nothing to press, and no mention of the service anywhere a user can read.
//
// Deleting the markup properly is the better end state, and it is a change to
// app.js rather than to this function: every one of those writes has to stop
// assuming its element exists first.
func withoutPinterest(page []byte) []byte {
	for _, part := range pinterestParts {
		page = hollowElement(page, part.marker, part.tag)
	}
	// A new piece of importer UI would arrive in index.html, which this function
	// has never seen, and reach the app as a working button. So the question is
	// asked of the finished page rather than of the list above: is the name of
	// the service readable anywhere on it. Loud and in front of a test, like the
	// rewrites above, rather than in a submission.
	if text := visibleText(page); bytes.Contains(bytes.ToLower(text), []byte("pinterest")) {
		panic("withoutPinterest: the page still says Pinterest somewhere a user can read it")
	}
	return page
}

// hollowElement empties the element carrying marker, keeping its own tag and
// every id inside it as an empty hidden span. Elements this build has never
// had are not an error: the page is free to lose one.
func hollowElement(page []byte, marker, tag string) []byte {
	at := bytes.Index(page, []byte(marker))
	if at < 0 {
		return page
	}
	open := bytes.LastIndex(page[:at], []byte("<"+tag))
	if open < 0 {
		panic("hollowElement: " + marker + " is not inside a <" + tag + ">")
	}
	// Where that element ends, counting the ones of the same kind nested inside
	// it: the importer panel holds a result section, and stopping at the first
	// </section> would cut the page in half.
	depth, end := 0, -1
	for i := open; i < len(page); i++ {
		switch {
		case bytes.HasPrefix(page[i:], []byte("<"+tag)):
			depth++
		case bytes.HasPrefix(page[i:], []byte("</"+tag+">")):
			depth--
			if depth == 0 {
				end = i + len("</"+tag+">")
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		panic("hollowElement: no closing </" + tag + "> for " + marker)
	}

	// The element's own id is the one in its opening tag. Everything after that
	// belongs to what was inside it, and the two are kept apart: writing a
	// child's id onto the parent would put the same id on the page twice.
	body := page[open:end]
	opening := bytes.IndexByte(body, '>')
	rebuilt := []byte("<" + tag)
	if own := idOf(body[:opening]); own != nil {
		rebuilt = append(rebuilt, ' ')
		rebuilt = append(rebuilt, own...)
	}
	rebuilt = append(rebuilt, []byte(" hidden>")...)
	// The ids of everything that was inside, kept so that code written for the
	// desktop page can go on writing to them harmlessly.
	for _, id := range idsIn(body[opening:]) {
		rebuilt = append(rebuilt, []byte(`<span `+string(id)+` hidden></span>`)...)
	}
	rebuilt = append(rebuilt, []byte("</"+tag+">")...)

	out := make([]byte, 0, len(page)-len(body)+len(rebuilt))
	out = append(out, page[:open]...)
	out = append(out, rebuilt...)
	return append(out, page[end:]...)
}

// idOf returns the first id attribute in fragment, as written.
func idOf(fragment []byte) []byte {
	ids := idsIn(fragment)
	if len(ids) == 0 {
		return nil
	}
	return ids[0]
}

// idsIn returns every id attribute in fragment, as written.
func idsIn(fragment []byte) [][]byte {
	var found [][]byte
	for rest := fragment; ; {
		at := bytes.Index(rest, []byte(`id="`))
		if at < 0 {
			return found
		}
		end := bytes.IndexByte(rest[at+len(`id="`):], '"')
		if end < 0 {
			return found
		}
		found = append(found, rest[at:at+len(`id="`)+end+1])
		rest = rest[at+len(`id="`)+end+1:]
	}
}

// visibleText returns the page with every tag removed, which is roughly what a
// user reads. Roughly is enough: it is asked one question, about one word.
func visibleText(page []byte) []byte {
	var text []byte
	for i := 0; i < len(page); i++ {
		if page[i] != '<' {
			text = append(text, page[i])
			continue
		}
		if close := bytes.IndexByte(page[i:], '>'); close >= 0 {
			i += close
		}
	}
	return text
}

func (s *server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	build := homePage
	if s.app.theme() == "light" {
		build = lightHomePage
	}
	data, err := build()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	serveBytes(w, r, "index.html", "text/html; charset=utf-8", data)
}

func (s *server) practicePage(w http.ResponseWriter, r *http.Request) {
	page := s.practice
	if s.app.theme() == "light" {
		page = stampLightTheme(page)
	}
	serveBytes(w, r, "practice.html", "text/html; charset=utf-8", page)
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
		{name: "third_party/gallery-dl/NOTICE.md", label: "gallery-dl notice"},
		{name: "third_party/gallery-dl/LICENSE", label: "gallery-dl — GPL-2.0-only"},
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

func (s *server) locale(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	if !strings.HasSuffix(name, ".json") || strings.TrimSuffix(name, ".json") == "" {
		http.NotFound(w, r)
		return
	}
	data, err := embeddedFiles.ReadFile("web/i18n/locales/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveBytes(w, r, name, "application/json; charset=utf-8", data)
}

func (s *server) i18nScript(w http.ResponseWriter, r *http.Request) {
	data, err := embeddedFiles.ReadFile("web/i18n/i18n.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveBytes(w, r, "i18n.js", "text/javascript; charset=utf-8", data)
}

func serveBytes(w http.ResponseWriter, r *http.Request, name, contentType string, data []byte) {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (s *server) imageRecord(path string, score *float64) imageRecord {
	id := stableImageID(path)
	info, _ := os.Stat(path)
	record := imageRecord{ID: id, Name: filepath.Base(path), Path: path, URL: "/image/" + id, Tags: s.tagsForImage(path), Score: score}
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

// Image IDs are derived from canonical file paths instead of their position in
// the index. Reordering or rebuilding the index therefore cannot redirect an
// action to a different image.
func stableImageID(path string) string {
	sum := sha256.Sum256([]byte(expandPath(path)))
	return hashHex(sum[:])
}

// The id to path index, and the library version it was built from.
//
// Turning an id back into a path used to mean hashing every path in the
// library, on every request. A screen of sixty thumbnails over a twenty
// thousand picture library is 1.2 million SHA-256 sums and 1.2 million copies
// of the path list, for sixty answers. On a desktop that is a warm fan; on a
// phone it is the reason scrolling stutters.
//
// Built once per change to the library instead, and thrown away by a counter
// rather than a timer, so it cannot serve a picture that has been removed.
type imageIndex struct {
	mu      sync.Mutex
	version uint64
	built   bool
	byID    map[string]string
}

func (s *server) imagePathByID(id string) (string, bool) {
	if len(id) != sha256.Size*2 {
		return "", false
	}

	version := s.app.libraryVersion()

	s.index.mu.Lock()
	defer s.index.mu.Unlock()
	if !s.index.built || s.index.version != version {
		// snapshot copies the library, which is exactly why this is here and
		// not on the path every request takes.
		paths, _, _ := s.app.snapshot()
		byID := make(map[string]string, len(paths))
		for _, path := range paths {
			byID[stableImageID(path)] = path
		}
		s.index.byID, s.index.version, s.index.built = byID, version, true
	}
	path, found := s.index.byID[id]
	return path, found
}

func (s *server) appState(w http.ResponseWriter, _ *http.Request) {
	paths, sources, job := s.app.snapshot()
	missing := s.app.missingEmbeddings(nil)
	indexing := s.app.indexingSettings()
	tags := []map[string]any{}
	for _, name := range s.collectionNames() {
		tags = append(tags, map[string]any{"name": name, "count": len(s.collectionImages(name))})
	}
	index := any(nil)
	if len(paths) > 0 {
		index = map[string]any{"count": len(paths), "sources": sources, "due": false, "maintenance_due": false, "duplicates": 0}
	}
	// Whether downloading works at all, which used to mean "is gallery-dl
	// installed". It is now always true: where gallery-dl is missing, which is
	// every phone, the built-in downloader answers for Pinterest boards and
	// ordinary picture pages instead. The name is reported so the interface can
	// say which one is running rather than implying the whole catalogue.
	_, galleryDLError := galleryDLBinary()
	downloader := "built-in"
	if galleryDLError == nil {
		downloader = "gallery-dl"
	}
	sendJSON(w, 200, map[string]any{
		"ok": true, "version": version, "model": s.app.embeddingModel.ModelID, "pretrained": s.app.embeddingModel.Revision,
		"semanticModel": s.app.embeddingModel.Key, "embeddingModel": s.app.embeddingModel,
		"updateMethod": updateMethod(),
		"update":       s.updateStatePayload(),
		"onboarding":   s.app.onboardingSettings(),
		"premium":      map[string]any{"unlocked": s.app.premiumUnlocked(), "sold": runsOnPhone},
		"pinterest":    s.app.pinterestSettings(),
		"web":          s.app.webSettings(),
		"index":        index, "indexJob": job, "sources": sources, "tags": tags,
		"searchIndex": map[string]any{
			"automatic": indexing.Automatic, "indexed": len(paths) - len(missing),
			"pending": len(missing), "total": len(paths),
		},
		"boards": len(s.boardRecords()), "aiAvailable": true,
		"mobile": runsOnPhone,
		"sync":   map[string]any{"available": s.sync != nil},
		"paths":  map[string]string{"home": s.app.home, "library": s.app.libraryDir, "boards": s.app.boardsDir, "tags": s.app.tagsDir},
		"viewer": "Browser",
		"storage": map[string]any{
			"previews":      true,
			"originalsKept": true,
		},
		"language": s.app.configuredLanguage(),
		"theme":    s.app.theme(),
		"browser":  s.app.browserSettings(),
		"plugins": map[string]any{
			"wikimedia": map[string]any{"enabled": s.app.pluginEnabled("wikimedia"), "name": "Wikimedia Commons", "description": "Browse random Commons images or search for something specific."},
			"calendar":  map[string]any{"enabled": s.app.pluginEnabled("calendar"), "name": "Calendar view", "description": "Browse local images grouped by when they were added."},
			"sidebar":   map[string]any{"enabled": s.app.pluginEnabled("sidebar"), "name": "Sidebar", "description": "Drag images into collections from a quick side panel."},
			"vim":       map[string]any{"enabled": s.app.pluginEnabled("vim"), "name": "Vim Mode", "description": "Use Vim-style keyboard navigation in the library and storyboard."},
			"canvas":    map[string]any{"enabled": s.app.pluginEnabled("canvas"), "name": "Folder canvas", "description": "Arrange a folder's pictures freely on a flat workspace."},
			"pinterest": map[string]any{
				"enabled": s.app.pluginEnabled("pinterest"), "available": true, "downloader": downloader,
				"name": "Import from Pinterest", "description": "Import every image from a public Pinterest board.",
			},
			"web": map[string]any{
				"enabled": s.app.pluginEnabled("web"), "available": true, "downloader": downloader,
				"name": "Import from web", "description": "Download a gallery from any supported site, and follow artists for new work.",
			},
			"commandPalette": map[string]any{
				"enabled": s.app.pluginEnabled("commandPalette"), "name": "Command palette",
				"description": "Open navigation and image search with Ctrl+K or Command+K.",
			},
		},
	})
}

func (s *server) saveBrowserSettings(w http.ResponseWriter, r *http.Request) {
	var request browserSettings
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.saveBrowserSettings(request); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "browser": s.app.browserSettings()})
}

func (s *server) saveIndexingSettings(w http.ResponseWriter, r *http.Request) {
	var request indexingSettings
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.saveIndexingSettings(request); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "indexing": s.app.indexingSettings()})
}

func (s *server) savePlugin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.setPluginEnabled(request.Name, request.Enabled); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "name": request.Name, "enabled": request.Enabled})
}

func (s *server) wikimediaSearch(w http.ResponseWriter, r *http.Request) {
	if !s.app.pluginEnabled("wikimedia") {
		sendError(w, http.StatusNotFound, fmt.Errorf("Wikimedia Commons plugin is disabled"))
		return
	}
	result, err := searchWikimedia(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("continue"), boundedInt(r.URL.Query().Get("limit"), 30, 1, 50))
	if err != nil {
		sendError(w, http.StatusBadGateway, err)
		return
	}
	sendJSON(w, http.StatusOK, result)
}

type calendarGroup struct {
	Label  string        `json:"label"`
	Count  int           `json:"count"`
	Images []imageRecord `json:"images"`
}

func (s *server) calendarView(w http.ResponseWriter, r *http.Request) {
	if !s.app.pluginEnabled("calendar") {
		sendError(w, http.StatusNotFound, fmt.Errorf("Calendar view plugin is disabled"))
		return
	}
	month := strings.TrimSpace(r.URL.Query().Get("month"))
	var monthTime time.Time
	if month != "" {
		parsed, err := time.Parse("2006-01", month)
		if err != nil {
			sendError(w, http.StatusBadRequest, fmt.Errorf("month must use YYYY-MM format"))
			return
		}
		monthTime = parsed
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)
	paths, _, _ := s.app.snapshot()
	groups := []calendarGroup{}
	byLabel := map[string]int{}
	for _, path := range paths {
		modified := fileMtime(path)
		when := time.Unix(modified, 0).In(now.Location())
		if !monthTime.IsZero() && (when.Year() != monthTime.Year() || when.Month() != monthTime.Month()) {
			continue
		}
		day := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, now.Location())
		label := when.Format("January 2006")
		if day.Equal(today) {
			label = "Today"
		} else if day.Equal(yesterday) {
			label = "Yesterday"
		}
		index, found := byLabel[label]
		if !found {
			index = len(groups)
			byLabel[label] = index
			groups = append(groups, calendarGroup{Label: label})
		}
		groups[index].Images = append(groups[index].Images, s.imageRecord(path, nil))
		groups[index].Count++
	}
	// Keep Today and Yesterday first, then month sections newest-first.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Label == "Today" {
			return true
		}
		if groups[j].Label == "Today" {
			return false
		}
		if groups[i].Label == "Yesterday" {
			return true
		}
		if groups[j].Label == "Yesterday" {
			return false
		}
		return groups[i].Label > groups[j].Label
	})
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "groups": groups, "month": month})
}

func (s *server) saveStorageSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Language string `json:"language"`
	}
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	// Also strips the retired original-replacement option from older config
	// files. Imported originals can no longer be converted through this API.
	if err := s.app.saveStorageSettings(storageSettings{}); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	if request.Language != "" {
		if err := s.app.saveLanguage(request.Language); err != nil {
			sendError(w, http.StatusInternalServerError, err)
			return
		}
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "originalsKept": true, "language": s.app.language()})
}

func (s *server) saveThemeSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Theme string `json:"theme"`
	}
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.saveTheme(request.Theme); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "theme": s.app.theme()})
}

func (s *server) aiState(w http.ResponseWriter, _ *http.Request) {
	paths, _, _ := s.app.snapshot()
	missing := s.app.missingEmbeddings(nil)
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

func (s *server) rebuildAIIndex(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Pictogrep-Action") != "rebuild-search-index" {
		sendError(w, http.StatusForbidden, fmt.Errorf("full reindex requires confirmation"))
		return
	}
	refresh, err := s.app.refreshLibrary("")
	if err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	removed, err := s.app.resetEmbeddings()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{
		"ok": true, "cleared": removed, "added": refresh.Added,
		"removed": refresh.Removed, "total": refresh.Total,
	})
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
	results := s.app.vectorSearch(request.Vector, len(allowed), nil)
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
	query := r.URL.Query()
	paths, err := s.filteredPaths(query.Get("tag"), query.Get("source"))
	if err != nil {
		sendError(w, 400, err)
		return
	}
	mode := query.Get("mode")
	if mode == "unsorted" {
		tagged := s.taggedPaths()
		kept := paths[:0]
		for _, path := range paths {
			if !tagged[expandPath(path)] {
				kept = append(kept, path)
			}
		}
		paths = kept
	}
	// The shuffle is seeded so a caller paging through the library keeps one
	// order across requests instead of seeing pictures repeat or go missing.
	// Kept well under 2^53 so the seed survives the round trip through JSON
	// numbers and every page really does shuffle the same way.
	seed, err := strconv.ParseInt(query.Get("seed"), 10, 64)
	if err != nil || seed <= 0 || seed >= 1<<48 {
		seed = rand.New(rand.NewSource(time.Now().UnixNano())).Int63n(1<<48-1) + 1
	}
	if mode == "recent" {
		sort.SliceStable(paths, func(i, j int) bool { return fileMtime(paths[i]) > fileMtime(paths[j]) })
	} else if mode == "random" {
		random := rand.New(rand.NewSource(seed))
		random.Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
	}
	total := len(paths)
	offset := boundedInt(query.Get("offset"), 0, 0, total)
	paths = paths[offset:]
	count := boundedInt(query.Get("count"), 120, 1, 500)
	if len(paths) > count {
		paths = paths[:count]
	}
	records := make([]imageRecord, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			records = append(records, s.imageRecord(path, nil))
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "images": records, "total": total, "offset": offset, "seed": seed})
}

func (s *server) appImage(w http.ResponseWriter, r *http.Request) {
	path, found := s.imagePathByID(r.PathValue("id"))
	if !found {
		sendError(w, http.StatusNotFound, fmt.Errorf("unknown image"))
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "image": s.imageRecord(path, nil)})
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
	path, found := s.imagePathByID(r.PathValue("id"))
	paths, _, _ := s.app.snapshot()
	if !found {
		sendError(w, 400, fmt.Errorf("unknown image"))
		return
	}
	limit := boundedInt(r.URL.Query().Get("limit"), 18, 1, 60)
	// Paging walks further down the same ranking, so the viewer can keep
	// showing less and less similar pictures as you scroll.
	offset := boundedInt(r.URL.Query().Get("offset"), 0, 0, 5000)
	mtimes := s.app.mtimeSnapshot()
	indexed := s.app.indexedEmbeddingCount(mtimes)
	vector, ready := s.app.imageEmbedding(path)
	images := []imageRecord{}
	if ready {
		skipped := 0
		for _, result := range s.app.vectorSearch(vector, limit+offset+1, mtimes) {
			if result.Path == path {
				continue
			}
			if skipped < offset {
				skipped++
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
		"ok": true, "ready": ready, "images": images, "offset": offset,
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
	indexed := make(map[string]bool, len(allPaths))
	for _, path := range allPaths {
		indexed[path] = true
	}
	tagsByPath := map[string][]string{}
	for _, name := range s.collectionNames() {
		for _, path := range s.collectionImages(name) {
			tagsByPath[path] = append(tagsByPath[path], name)
		}
	}
	images := make([]imageRecord, 0, len(paths))
	for _, path := range paths {
		if !indexed[path] {
			continue
		}
		images = append(images, imageRecord{ID: stableImageID(path), Name: filepath.Base(path), Path: path, URL: "/image/" + stableImageID(path), Tags: tagsByPath[path]})
	}
	return images
}

func (s *server) appCanvas(w http.ResponseWriter, r *http.Request) {
	if !s.app.pluginEnabled("canvas") {
		sendError(w, http.StatusNotFound, fmt.Errorf("Folder canvas plugin is disabled"))
		return
	}
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
			positions[image.ID] = point
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true, "images": images, "positions": positions})
}

func (s *server) saveAppCanvas(w http.ResponseWriter, r *http.Request) {
	if !s.app.pluginEnabled("canvas") {
		sendError(w, http.StatusNotFound, fmt.Errorf("Folder canvas plugin is disabled"))
		return
	}
	var request struct {
		Tag       string `json:"tag"`
		Source    string `json:"source"`
		Positions []struct {
			ID string  `json:"id"`
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
	allowed := map[string]string{}
	for _, path := range paths {
		allowed[stableImageID(path)] = path
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
	preferences := s.app.loadFolderViewPreferences()
	folders := []map[string]any{}
	indexed := make(map[string]bool, len(paths))
	for _, path := range paths {
		indexed[path] = true
	}
	for _, source := range sources {
		folders = append(folders, s.sourceFolderRecords(source, paths, indexed, preferences)...)
	}
	// The library is where imported pictures land, and it is not a source, so
	// walking sources alone leaves the folder holding most of the collection
	// with no card at all. It gets one here, and only when it has something in
	// it, so a library nobody has imported into does not show an empty folder.
	library := []string{}
	for _, path := range paths {
		if pathInside(path, s.app.libraryDir) {
			library = append(library, path)
		}
	}
	if len(library) > 0 {
		folders = append(folders, s.folderRecord("source", "Library", s.app.libraryDir, library, indexed, preferences))
	}
	for _, name := range s.collectionNames() {
		recordName := filepath.Base(filepath.FromSlash(name))
		record := s.folderRecord("tag", recordName, name, s.collectionImages(name), indexed, preferences)
		record["depth"] = strings.Count(filepath.ToSlash(name), "/")
		folders = append(folders, record)
	}
	sendJSON(w, 200, map[string]any{"ok": true, "folders": folders, "view": preferences})
}

func (s *server) sourceFolderRecords(source string, paths []string, indexed map[string]bool, preferences folderViewPreferences) []map[string]any {
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
		record := s.folderRecord("source", name, directory, byDirectory[directory], indexed, preferences)
		record["depth"] = depth
		record["relative"] = filepath.ToSlash(relative)
		records = append(records, record)
	}
	return records
}

func (s *server) folderRecord(kind, name, value string, paths []string, indexed map[string]bool, preferences folderViewPreferences) map[string]any {
	paths = append([]string(nil), paths...)
	sort.SliceStable(paths, func(i, j int) bool { return fileMtime(paths[i]) > fileMtime(paths[j]) })
	previews := []imageRecord{}
	key := folderPreferenceKey(kind, value)
	coverID := preferences.Covers[key]
	if coverID != "" {
		for _, path := range paths {
			if stableImageID(path) == coverID && indexed[path] {
				previews = append(previews, imageRecord{ID: coverID, Name: filepath.Base(path), Path: path, Mtime: fileMtime(path), URL: "/image/" + coverID})
				break
			}
		}
	}
	for _, path := range paths {
		if len(previews) == 4 {
			break
		}
		if !indexed[path] {
			continue
		}
		id := stableImageID(path)
		if id == coverID {
			continue
		}
		previews = append(previews, imageRecord{ID: id, Name: filepath.Base(path), Path: path, Mtime: fileMtime(path), URL: "/image/" + id})
	}
	// Unix seconds, not fileMtime's nanoseconds: the frontend's folderDate()
	// does `new Date(lastAdded * 1000)`, which on a raw UnixNano value lands
	// several centuries past what Date can represent, so Intl.DateTimeFormat
	// throws formatting it and the whole folders render aborts before a
	// single card is appended.
	lastAdded := int64(0)
	if len(paths) > 0 {
		lastAdded = fileMtime(paths[0]) / int64(time.Second)
	}
	return map[string]any{
		"kind": kind, "name": name, "value": value, "key": key, "count": len(paths),
		"images": previews, "lastAdded": lastAdded, "favorite": preferences.Favorites[key], "coverId": coverID,
	}
}

func (s *server) appIndex(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode           string   `json:"mode"`
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
		defer guard(func(err error) { s.app.setJob("error", 0, 0, err.Error()) })
		if request.Mode == "incremental" {
			refresh, err := s.app.refreshLibrary(request.Folder)
			if err != nil {
				s.app.setJob("error", 0, 0, err.Error())
				return
			}
			message := "Library is up to date."
			if refresh.Added > 0 || refresh.Removed > 0 {
				message = fmt.Sprintf("Library updated: %d new, %d removed.", refresh.Added, refresh.Removed)
			}
			s.app.setJob("complete", refresh.Total, refresh.Total, message)
			return
		}
		if request.Mode != "" {
			s.app.setJob("error", 0, 0, "unknown indexing mode")
			return
		}
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
	result, status, err := s.saveImportedImage(r.Body, r.URL.Query().Get("name"), r.URL.Query().Get("folder"), r.URL.Query().Get("source"))
	if err != nil {
		sendError(w, status, err)
		return
	}
	// The share sheet's own endpoint, so this is where a picture most often
	// enters a phone. Sending starts now rather than on the next interval,
	// which is the difference between "share it and it is on the desktop" and
	// "share it and it is on the desktop in five minutes".
	s.syncSoon()
	sendJSON(w, http.StatusOK, result)
}

// syncSoon asks the outbox for a pass, where there is one. Safe on a build with
// no sync and on a device that has never paired: both are a no-op.
func (s *server) syncSoon() {
	if s.sync != nil && s.sync.outbox != nil {
		s.sync.outbox.nudge()
	}
}

func (s *server) deleteImage(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Pictogrep-Action") != "delete-image" {
		sendError(w, http.StatusForbidden, fmt.Errorf("image deletion requires confirmation"))
		return
	}
	path, found := s.imagePathByID(r.PathValue("id"))
	if !found {
		sendError(w, http.StatusBadRequest, fmt.Errorf("invalid image"))
		return
	}
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if request.Path == "" || expandPath(request.Path) != path {
		sendError(w, http.StatusConflict, fmt.Errorf("image changed; reopen it before deleting"))
		return
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			// Continue and clean stale Pictogrep metadata.
		} else {
			sendError(w, http.StatusBadRequest, fmt.Errorf("could not delete image from disk: %v", err))
			return
		}
	}
	for _, name := range s.collectionNames() {
		s.unlinkTag(name, path)
	}
	if err := s.app.removePath(path); err != nil {
		sendError(w, http.StatusInternalServerError, fmt.Errorf("file was deleted, but library metadata could not be fully updated: %v", err))
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": true, "name": filepath.Base(path), "path": path})
}

func (a *application) duplicatePath(candidate string) (string, bool, error) {
	digest, err := fileDigest(candidate)
	if err != nil {
		return "", false, err
	}
	paths, _, _ := a.snapshot()
	for _, path := range paths {
		other, err := fileDigest(path)
		if err == nil && other == digest {
			return path, true, nil
		}
	}
	return "", false, nil
}

func validImageFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if config, format, err := image.DecodeConfig(file); err == nil {
		return imageFormatMatches(strings.ToLower(filepath.Ext(path)), format) && safeImageDimensions(config.Width, config.Height)
	}
	if strings.ToLower(filepath.Ext(path)) != ".webp" {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	header := make([]byte, 30)
	_, err = io.ReadFull(file, header)
	info, statErr := file.Stat()
	if err != nil || statErr != nil || !validWebPHeader(header, info.Size()) {
		return false
	}
	width, height, ok := webPDimensions(header)
	return ok && safeImageDimensions(width, height)
}

func safeImageDimensions(width, height int) bool {
	if width < 1 || height < 1 || width > maxDecodedImageDimension || height > maxDecodedImageDimension {
		return false
	}
	return int64(width) <= int64(maxDecodedImagePixels)/int64(height)
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

func webPDimensions(header []byte) (int, int, bool) {
	if len(header) < 25 {
		return 0, 0, false
	}
	chunkSize := binary.LittleEndian.Uint32(header[16:20])
	switch string(header[12:16]) {
	case "VP8X":
		if len(header) < 30 || chunkSize < 10 {
			return 0, 0, false
		}
		width := 1 + int(header[24]) + int(header[25])<<8 + int(header[26])<<16
		height := 1 + int(header[27]) + int(header[28])<<8 + int(header[29])<<16
		return width, height, true
	case "VP8L":
		if chunkSize < 5 || header[20] != 0x2f {
			return 0, 0, false
		}
		width := 1 + int(header[21]) + int(header[22]&0x3f)<<8
		height := 1 + int(header[22]>>6) + int(header[23])<<2 + int(header[24]&0x0f)<<10
		return width, height, true
	case "VP8 ":
		if len(header) < 30 || chunkSize < 10 || header[20]&1 != 0 || string(header[23:26]) != "\x9d\x01\x2a" {
			return 0, 0, false
		}
		width := int(binary.LittleEndian.Uint16(header[26:28]) & 0x3fff)
		height := int(binary.LittleEndian.Uint16(header[28:30]) & 0x3fff)
		return width, height, true
	default:
		return 0, 0, false
	}
}

func collectionName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	parts := strings.Split(filepath.ToSlash(value), "/")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		var builder strings.Builder
		for _, r := range part {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				builder.WriteRune(r)
			} else if r == ' ' {
				builder.WriteRune('-')
			}
		}
		clean := strings.Trim(builder.String(), "-_")
		if clean == "" {
			return "", fmt.Errorf("folder name must contain letters or numbers")
		}
		cleanParts = append(cleanParts, clean)
	}
	name := strings.Join(cleanParts, "/")
	if len(name) > 80 {
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
		ImageID string    `json:"imageId"`
		Into    string    `json:"into"`
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
		path, found := s.imagePathByID(request.ImageID)
		if !found {
			sendError(w, 400, fmt.Errorf("unknown image"))
			return
		}
		added, err := s.linkTag(name, path)
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
		mtimes := s.app.mtimeSnapshot()
		results := s.app.vectorSearch(request.Vector, request.Limit, mtimes)
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
			"matched": len(selected), "indexed": s.app.indexedEmbeddingCount(mtimes), "total": len(paths),
		})
	case "rename":
		into, err := collectionName(request.Into)
		if err != nil {
			sendError(w, 400, err)
			return
		}
		if into == name {
			sendJSON(w, 200, map[string]any{"ok": true, "tag": name})
			return
		}
		oldDirectory := filepath.Join(s.app.tagsDir, name)
		newDirectory := filepath.Join(s.app.tagsDir, into)
		if info, statErr := os.Stat(oldDirectory); statErr != nil || !info.IsDir() {
			sendError(w, 404, fmt.Errorf("unknown folder"))
			return
		}
		if _, statErr := os.Stat(newDirectory); !os.IsNotExist(statErr) {
			sendError(w, 409, fmt.Errorf("a folder named %s already exists", into))
			return
		}
		if pathInside(newDirectory, oldDirectory) {
			sendError(w, 400, fmt.Errorf("a folder cannot be moved inside itself"))
			return
		}
		oldNames := append([]string{name}, s.subCollections(name)...)
		if err := os.MkdirAll(filepath.Dir(newDirectory), 0o755); err != nil {
			sendError(w, 400, err)
			return
		}
		if err := os.Rename(oldDirectory, newDirectory); err != nil {
			sendError(w, 400, err)
			return
		}
		for _, oldName := range oldNames {
			newName := into + strings.TrimPrefix(oldName, name)
			s.app.moveFolderPreferences("tag", oldName, newName)
		}
		sendJSON(w, 200, map[string]any{"ok": true, "tag": into, "renamed": name})
	case "merge":
		// The dragged folder keeps its name and swallows the one it was
		// dropped on, so nothing has to be renamed by hand.
		into, err := collectionName(request.Into)
		if err != nil {
			sendError(w, 400, err)
			return
		}
		if into == name {
			sendError(w, 400, fmt.Errorf("choose two different folders"))
			return
		}
		if children := s.subCollections(into); len(children) > 0 {
			sendError(w, 400, fmt.Errorf("move the subfolders of %s out first", into))
			return
		}
		combined := existingUniquePaths(append(s.collectionImages(name), s.collectionImages(into)...))
		directory := filepath.Join(s.app.tagsDir, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			sendError(w, 400, err)
			return
		}
		if err := writeTagManifest(directory, combined); err != nil {
			sendError(w, 400, err)
			return
		}
		if err := os.RemoveAll(filepath.Join(s.app.tagsDir, into)); err != nil {
			sendError(w, 400, err)
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "tag": name, "merged": into, "count": len(combined)})
	case "delete":
		if children := s.subCollections(name); len(children) > 0 {
			sendError(w, 400, fmt.Errorf("move the subfolders of %s out first", name))
			return
		}
		// Only the collection's own links and manifest go away; the pictures
		// themselves stay wherever they live on disk.
		if err := os.RemoveAll(filepath.Join(s.app.tagsDir, name)); err != nil {
			sendError(w, 400, err)
			return
		}
		sendJSON(w, 200, map[string]any{"ok": true, "tag": name, "deleted": true})
	case "remove":
		path, found := s.imagePathByID(request.ImageID)
		if !found {
			sendError(w, 400, fmt.Errorf("unknown image"))
			return
		}
		removed := s.unlinkTag(name, path)
		sendJSON(w, 200, map[string]any{"ok": true, "tag": name, "removed": removed})
	default:
		sendError(w, 400, fmt.Errorf("unknown tag action"))
	}
}

func (s *server) subCollections(name string) []string {
	children := []string{}
	for _, candidate := range s.collectionNames() {
		if strings.HasPrefix(candidate, name+"/") {
			children = append(children, candidate)
		}
	}
	return children
}

func (s *server) collectionNames() []string {
	names := []string{}
	_ = filepath.WalkDir(s.app.tagsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == s.app.tagsDir || !entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(s.app.tagsDir, path)
		if err == nil {
			if name, err := collectionName(filepath.ToSlash(relative)); err == nil {
				names = append(names, name)
			}
		}
		return nil
	})
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

// taggedPaths collects every image that belongs to a collection in one sweep,
// which keeps "unsorted" cheap compared with asking tagsForImage per picture.
func (s *server) taggedPaths() map[string]bool {
	tagged := map[string]bool{}
	for _, name := range s.collectionNames() {
		for _, path := range s.collectionImages(name) {
			tagged[expandPath(path)] = true
		}
	}
	return tagged
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
	paths := []string{}
	if data, err := os.ReadFile(filepath.Join(directory, "images.json")); err == nil {
		_ = json.Unmarshal(data, &paths)
	}
	kept := make([]string, 0, len(paths))
	removed := false
	for _, path := range paths {
		path = expandPath(path)
		if path == expandPath(source) {
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
	path, found := s.imagePathByID(r.PathValue("id"))
	if !found {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

func (s *server) thumbnail(w http.ResponseWriter, r *http.Request) {
	path, found := s.imagePathByID(r.PathValue("id"))
	if !found {
		http.NotFound(w, r)
		return
	}
	// Grid previews default to 960 px; the viewer may request up to 2048 px.
	// The requested size is part of the cache key so a grid preview can never be
	// reused as the larger viewer image.
	maximum := boundedInt(r.URL.Query().Get("size"), 960, 320, 2048)
	// Include the preview format version so quality changes never reuse an old,
	// visibly undersized thumbnail from a previous Pictogrep release.
	key := sha256.Sum256([]byte("preview-v3\x00" + path + "\x00" + strconv.FormatInt(embeddingMtime(path), 10) + "\x00" + strconv.Itoa(maximum)))
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
	config, format, configErr := image.DecodeConfig(file)
	if configErr != nil || !imageFormatMatches(strings.ToLower(filepath.Ext(path)), format) {
		_ = file.Close()
		s.image(w, r)
		return
	}
	if !safeImageDimensions(config.Width, config.Height) {
		_ = file.Close()
		http.Error(w, "image dimensions are too large for a safe preview", http.StatusUnprocessableEntity)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		s.image(w, r)
		return
	}
	thumbnail, decodeErr := renderPreview(file, config, maximum)
	_ = file.Close()
	if decodeErr != nil {
		s.image(w, r)
		return
	}
	output, err := os.CreateTemp(s.app.thumbnailDir, "thumbnail-*.tmp")
	if err != nil {
		s.image(w, r)
		return
	}
	temporary := output.Name()
	// A grid tile is displayed a fraction of the size it is encoded at, so the
	// last few quality points are downscaled away before anyone sees them and
	// cost roughly half the file size to keep. The viewer's larger sizes stay
	// near-lossless: that image is looked at closely, and it is the one a
	// reference gets drawn from.
	quality := 96
	if maximum <= 1024 {
		quality = 82
	}
	err = jpeg.Encode(output, thumbnail, &jpeg.Options{Quality: quality})
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

// resizePreview uses bilinear resampling instead of nearest-neighbour sampling.
// That keeps diagonal edges and fine details smooth even when a large original
// is reduced to a grid-sized preview. Transparent pixels are composited onto
// white because the preview cache is JPEG; the original is never changed.
// How many pixels may be held in decoded form at once, across every request.
//
// Decoding costs memory in proportion to the picture, not to the thumbnail
// coming out of it, and a browser opens six connections to fill a screen of
// thumbnails. Six simultaneous decodes at the 25 megapixel ceiling is more
// memory than a phone lends a process before killing it, and on Android this
// process dying is the app dying, because the server is a child of the app.
//
// 32 megapixels is roughly 48 MB of JPEG planes or 128 MB of RGBA, which a
// phone can hold while the rest of the app is running.
const previewPixelBudget = 32_000_000

// A budget rather than a count, because what runs the process out of memory is
// area. One 24 megapixel picture and eight small ones cost the same to admit
// under a counting semaphore, and nothing like the same to decode.
type pixelBudget struct {
	mu    sync.Mutex
	room  *sync.Cond
	limit int64
	spent int64
}

func newPixelBudget(limit int64) *pixelBudget {
	budget := &pixelBudget{limit: limit}
	budget.room = sync.NewCond(&budget.mu)
	return budget
}

func (b *pixelBudget) acquire(pixels int64) {
	pixels = max(pixels, 1)
	b.mu.Lock()
	defer b.mu.Unlock()
	// b.spent > 0 is what keeps a picture larger than the whole budget from
	// waiting forever: it goes through on its own once nothing else is
	// running, rather than becoming a thumbnail that can never be made.
	for b.spent > 0 && b.spent+pixels > b.limit {
		b.room.Wait()
	}
	b.spent += pixels
}

func (b *pixelBudget) release(pixels int64) {
	pixels = max(pixels, 1)
	b.mu.Lock()
	b.spent = max(b.spent-pixels, 0)
	b.mu.Unlock()
	b.room.Broadcast()
}

var previewBudget = newPixelBudget(previewPixelBudget)

// renderPreview decodes one picture and shrinks it, holding a share of the
// decode budget for exactly as long as the full sized image is in memory.
func renderPreview(file io.Reader, config image.Config, maximum int) (*image.RGBA, error) {
	pixels := int64(config.Width) * int64(config.Height)
	previewBudget.acquire(pixels)
	defer previewBudget.release(pixels)

	source, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > maximum || height > maximum {
		scale := math.Min(float64(maximum)/float64(width), float64(maximum)/float64(height))
		width = max(1, int(float64(width)*scale))
		height = max(1, int(float64(height)*scale))
	}
	return resizePreview(source, width, height), nil
}

// eight converts one 16 bit channel to the 8 bits a JPEG can actually carry.
func eight(value float64) uint8 {
	return uint8(min(255, math.Round(value/257)))
}

func resizePreview(source image.Image, width, height int) *image.RGBA {
	bounds := source.Bounds()
	// RGBA rather than RGBA64: this is encoded straight to JPEG, which is eight
	// bits a channel, so the extra byte per channel was carried through the
	// whole resize and thrown away at the end. Halves the picture held in
	// memory while it is being written.
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	scaleX := float64(bounds.Dx()) / float64(width)
	scaleY := float64(bounds.Dy()) / float64(height)
	for y := 0; y < height; y++ {
		sy := (float64(y)+0.5)*scaleY - 0.5
		y0 := max(bounds.Min.Y, min(bounds.Max.Y-1, int(math.Floor(sy))+bounds.Min.Y))
		y1 := min(bounds.Max.Y-1, y0+1)
		wy := sy - math.Floor(sy)
		for x := 0; x < width; x++ {
			sx := (float64(x)+0.5)*scaleX - 0.5
			x0 := max(bounds.Min.X, min(bounds.Max.X-1, int(math.Floor(sx))+bounds.Min.X))
			x1 := min(bounds.Max.X-1, x0+1)
			wx := sx - math.Floor(sx)
			r00, g00, b00, a00 := source.At(x0, y0).RGBA()
			r10, g10, b10, a10 := source.At(x1, y0).RGBA()
			r01, g01, b01, a01 := source.At(x0, y1).RGBA()
			r11, g11, b11, a11 := source.At(x1, y1).RGBA()
			blend := func(v00, v10, v01, v11 uint32) float64 {
				top := float64(v00) + (float64(v10)-float64(v00))*wx
				bottom := float64(v01) + (float64(v11)-float64(v01))*wx
				return top + (bottom-top)*wy
			}
			a := blend(a00, a10, a01, a11)
			r := min(65535.0, blend(r00, r10, r01, r11)+65535-a)
			g := min(65535.0, blend(g00, g10, g01, g11)+65535-a)
			b := min(65535.0, blend(b00, b10, b01, b11)+65535-a)
			target.SetRGBA(x, y, color.RGBA{R: eight(r), G: eight(g), B: eight(b), A: 255})
		}
	}
	return target
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
		path, found := s.imagePathByID(selected)
		if !found {
			sendError(w, 400, fmt.Errorf("unknown image"))
			return
		}
		paths = []string{path}
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
	if config, format, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
		return imageFormatMatches(extension, format) && safeImageDimensions(config.Width, config.Height)
	}
	if extension != ".webp" || !validWebPHeader(raw, int64(len(raw))) {
		return false
	}
	width, height, ok := webPDimensions(raw)
	return ok && safeImageDimensions(width, height)
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
		ImageID    string `json:"imageId"`
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
	path, found := s.imagePathByID(request.ImageID)
	if err != nil || !validImageData(raw, ".png") || !found || request.Index < 1 || request.Index > 9999 {
		sendError(w, 400, fmt.Errorf("invalid drawing"))
		return
	}
	validAspects := map[string]bool{"16:9": true, "4:3": true, "2:1": true, "3:4": true}
	if !validAspects[request.Aspect] {
		sendError(w, 400, fmt.Errorf("invalid drawing aspect"))
		return
	}
	aspect := strings.ReplaceAll(request.Aspect, ":", "x")
	// The stable image ID prevents drawings for same-named images in different
	// folders (or different search result sets) from silently replacing one
	// another. imagePathByID above guarantees this is a complete SHA-256 ID.
	filename := fmt.Sprintf("%04d_%s_%s_%s.png", request.Index, aspect, cleanStem(filepath.Base(path)), request.ImageID)
	target := filepath.Join(s.app.boardsDir, filename)
	if err := writeFileAtomically(target, raw, 0o644); err != nil {
		sendError(w, 400, err)
		return
	}
	metadata := map[string]any{"file": filename, "source": path, "aspect": request.Aspect, "tags": s.tagsForImage(path), "query": strings.TrimSpace(request.Query)}
	data, _ := json.MarshalIndent(metadata, "", "  ")
	if err := writeFileAtomically(strings.TrimSuffix(target, ".png")+".json", append(data, '\n'), 0o644); err != nil {
		sendError(w, 500, fmt.Errorf("drawing was saved, but its metadata could not be saved"))
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "file": filename})
}

func writeFileAtomically(target string, data []byte, mode fs.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, target)
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
