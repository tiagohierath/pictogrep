package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real PNG, because the importer downstream decodes what arrives and a file
// of the word "picture" would pass the downloader and fail the library.
func testPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			canvas.Set(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// A Pinterest that behaves like the real one: the two resource endpoints, the
// mandatory handler header, and a board that pages.
func fakePinterest(t *testing.T, pins, perPage int) *httptest.Server {
	t.Helper()
	pin := func(index int) map[string]any {
		return map[string]any{
			"id": fmt.Sprintf("%09d", index),
			"images": map[string]any{
				"236x": map[string]any{"url": "/img/small-" + fmt.Sprint(index) + ".png", "width": 236, "height": 236},
				"orig": map[string]any{"url": "/img/" + fmt.Sprint(index) + ".png", "width": 1000, "height": 1000},
			},
		}
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	// Without the handler header the real endpoints answer 403, and getting
	// that wrong is the difference between a working import and none.
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Pinterest-PWS-Handler") == "" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("Invalid Resource Request"))
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/resource/BoardResource/get/", guard(func(w http.ResponseWriter, r *http.Request) {
		var query struct {
			Options struct {
				Username string `json:"username"`
				Slug     string `json:"slug"`
			} `json:"options"`
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("data")), &query)
		if query.Options.Username != "someone" || query.Options.Slug != "a-board" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource_response": map[string]any{"error": map[string]any{"message": "Board not found."}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource_response": map[string]any{"data": map[string]any{"id": "8891", "pin_count": pins}},
		})
	}))

	mux.HandleFunc("/resource/BoardFeedResource/get/", guard(func(w http.ResponseWriter, r *http.Request) {
		var query struct {
			Options struct {
				BoardID   string   `json:"board_id"`
				Bookmarks []string `json:"bookmarks"`
			} `json:"options"`
		}
		if err := json.Unmarshal([]byte(r.URL.Query().Get("data")), &query); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if query.Options.BoardID != "8891" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		from := 0
		if len(query.Options.Bookmarks) > 0 {
			fmt.Sscanf(query.Options.Bookmarks[0], "at-%d", &from)
		}
		data := []map[string]any{}
		for index := from; index < from+perPage && index < pins; index++ {
			data = append(data, pin(index))
		}
		bookmark := fmt.Sprintf("at-%d", from+perPage)
		if from+perPage >= pins {
			bookmark = "-end-"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource_response": map[string]any{"data": data, "bookmark": bookmark},
		})
	}))

	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG(t, 200))
	})

	t.Cleanup(server.Close)
	return server
}

// downloadFrom runs the whole downloader against a test server, which for any
// host that is not Pinterest is the same path the app takes.
func downloadFrom(t *testing.T, server *httptest.Server, path string, request galleryDLRequest) error {
	t.Helper()
	request.URL = server.URL + path
	if request.Directory == "" {
		request.Directory = t.TempDir()
	}
	return runNativeGallery(context.Background(), request)
}

func downloadedFiles(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// board runs the Pinterest extractor against a fake Pinterest. Dispatch is by
// hostname and a test server is not pinterest.com, so the extractor is reached
// directly; everything past that point is the code the app runs.
func board(t *testing.T, server *httptest.Server, path string, limit int) []nativeGalleryImage {
	t.Helper()
	address, err := url.Parse(server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	if limit == 0 {
		limit = maxPinterestImages
	}
	images, err := pinterestBoardImages(context.Background(), address, limit)
	if err != nil {
		t.Fatalf("reading the board failed: %v", err)
	}
	return images
}

func TestNativeDownloaderWalksAWholePinterestBoard(t *testing.T) {
	server := fakePinterest(t, 30, 10)
	images := board(t, server, "/someone/a-board/", 0)
	if len(images) != 30 {
		t.Fatalf("found %d pictures on a 30 pin board", len(images))
	}
	directory := t.TempDir()
	if err := downloadAll(context.Background(), images, galleryDLRequest{Directory: directory}, 0); err != nil {
		t.Fatal(err)
	}
	if files := downloadedFiles(t, directory); len(files) != 30 {
		t.Fatalf("downloaded %d of 30 pictures: %v", len(files), files)
	}
}

// The board endpoint and the feed endpoint both answer 403 without the handler
// header, which is the one header that matters and the easiest to lose.
func TestNativeDownloaderSendsThePinterestHandlerHeader(t *testing.T) {
	server := fakePinterest(t, 5, 5)
	if images := board(t, server, "/someone/a-board/", 0); len(images) != 5 {
		t.Fatalf("found %d pictures, so a request went out without the handler header", len(images))
	}
}

func TestNativeDownloaderTakesTheOriginalNotTheThumbnail(t *testing.T) {
	server := fakePinterest(t, 1, 1)
	images := board(t, server, "/someone/a-board/", 0)
	if len(images) != 1 {
		t.Fatalf("expected one picture, found %d", len(images))
	}
	if !strings.HasSuffix(images[0].URL, "/img/0.png") {
		t.Fatalf("took %s instead of the original", images[0].URL)
	}
}

func TestNativeDownloaderStopsAtTheLimit(t *testing.T) {
	server := fakePinterest(t, 100, 10)
	if images := board(t, server, "/someone/a-board/", 12); len(images) != 12 {
		t.Fatalf("a limit of 12 found %d pictures", len(images))
	}
}

// A board that is not there answers 404, and the message has to say so rather
// than reporting an empty board, which is what a silent parse failure looks
// like from the outside.
func TestNativeDownloaderSaysWhenABoardIsNotThere(t *testing.T) {
	server := fakePinterest(t, 10, 10)
	address, err := url.Parse(server.URL + "/someone/no-such-board/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pinterestBoardImages(context.Background(), address, 10)
	if err == nil || !strings.Contains(err.Error(), "no public board") {
		t.Fatalf("a missing board failed with: %v", err)
	}
}

// A profile holds many boards, and picking one of them at random is worse than
// saying which address is wanted.
func TestNativeDownloaderRefusesAProfile(t *testing.T) {
	server := fakePinterest(t, 10, 10)
	address, err := url.Parse(server.URL + "/someone/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pinterestBoardImages(context.Background(), address, 10)
	if err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("a profile address failed with: %v", err)
	}
}

// Following a board is only cheap if the second check downloads nothing.
func TestNativeDownloaderRemembersWhatItAlreadyHas(t *testing.T) {
	server := fakePinterest(t, 20, 10)
	archive := filepath.Join(t.TempDir(), "seen.sqlite")
	images := board(t, server, "/someone/a-board/", 0)

	first := t.TempDir()
	if err := downloadAll(context.Background(), images, galleryDLRequest{Directory: first, Archive: archive}, 0); err != nil {
		t.Fatal(err)
	}
	if files := downloadedFiles(t, first); len(files) != 20 {
		t.Fatalf("the first check downloaded %d of 20 pictures", len(files))
	}

	second := t.TempDir()
	if err := downloadAll(context.Background(), images, galleryDLRequest{Directory: second, Archive: archive}, 0); err != nil {
		t.Fatal(err)
	}
	if files := downloadedFiles(t, second); len(files) != 0 {
		t.Fatalf("checking an unchanged board downloaded %d pictures again: %v", len(files), files)
	}

	// gallery-dl keeps a SQLite database at the path it is handed, so the
	// native archive must not be that file.
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("the native downloader wrote over gallery-dl's own archive file")
	}
}

func TestNativeDownloaderReadsAnOrdinaryPage(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/gallery", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head>
			<meta property="og:image" content="/img/cover.png">
			</head><body>
			<img src="/img/one.png">
			<img srcset="/img/two-small.png 300w, /img/two.png 1200w">
			<a href="/img/three.png">a link straight to a picture</a>
			<a href="/somewhere-else">not a picture</a>
			<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=">
			</body></html>`)
	})
	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG(t, 128))
	})

	directory := t.TempDir()
	if err := downloadFrom(t, server, "/gallery", galleryDLRequest{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	// cover, one, two (the largest of the srcset, not the small one), three.
	// The data: URL is not something the library can hold.
	if files := downloadedFiles(t, directory); len(files) != 4 {
		t.Fatalf("took %d pictures off the page, expected 4: %v", len(files), files)
	}
}

func TestNativeDownloaderSaysWhenAPageHasNoPictures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body><p>Words, and nothing else.</p></body></html>")
	}))
	t.Cleanup(server.Close)
	err := downloadFrom(t, server, "/", galleryDLRequest{})
	if err == nil || !strings.Contains(err.Error(), "no pictures") {
		t.Fatalf("a page with no pictures failed with: %v", err)
	}
}

// A dead pin is normal on any board old enough to be worth following.
func TestNativeDownloaderKeepsGoingPastAPictureThatWillNotDownload(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/gallery", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<img src="/img/gone.png"><img src="/img/here.png">`)
	})
	mux.HandleFunc("/img/gone.png", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/img/here.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG(t, 90))
	})

	directory := t.TempDir()
	if err := downloadFrom(t, server, "/gallery", galleryDLRequest{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	if files := downloadedFiles(t, directory); len(files) != 1 {
		t.Fatalf("one dead picture took the rest of the import with it: %v", files)
	}
}

// The content type is the only thing standing between the library and a page
// of HTML saved as a .png.
func TestNativeDownloaderRefusesSomethingThatIsNotAPicture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/img/") {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>not a picture at all</html>")
			return
		}
		fmt.Fprint(w, `<img src="/img/liar.png">`)
	}))
	t.Cleanup(server.Close)
	directory := t.TempDir()
	if err := downloadFrom(t, server, "/", galleryDLRequest{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	if files := downloadedFiles(t, directory); len(files) != 0 {
		t.Fatalf("saved something that was not a picture: %v", files)
	}
}

func TestNativeDownloaderIsWhatRunsWithoutGalleryDL(t *testing.T) {
	// The point of the whole file: on a phone there is no gallery-dl binary,
	// and the import has to work anyway.
	if _, err := galleryDLBinary(); err == nil {
		t.Skip("this machine has gallery-dl installed, so it is what runs here")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/img/") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(testPNG(t, 44))
			return
		}
		fmt.Fprint(w, `<img src="/img/one.png">`)
	}))
	t.Cleanup(server.Close)

	directory := t.TempDir()
	err := downloadGallery(context.Background(), galleryDLRequest{URL: server.URL, Directory: directory})
	if err != nil {
		t.Fatalf("the import failed with no gallery-dl installed: %v", err)
	}
	if files := downloadedFiles(t, directory); len(files) != 1 {
		t.Fatalf("downloaded %v", files)
	}
}

func TestPinterestAddressesAreRecognized(t *testing.T) {
	for _, address := range []string{
		"https://www.pinterest.com/someone/a-board/",
		"https://pinterest.com/someone/a-board/",
		"https://br.pinterest.com/someone/a-board/",
		"https://pin.it/abcdef",
	} {
		parsed, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		if !isPinterestHost(parsed.Hostname()) {
			t.Fatalf("%s was not recognized as Pinterest", address)
		}
	}
	for _, address := range []string{
		"https://example.com/gallery",
		// The one that matters: a lookalike domain must not be handed
		// Pinterest's extractor, or a stranger's site decides what Pictogrep
		// downloads.
		"https://pinterest.com.evil.example/someone/a-board/",
		"https://notpinterest.com/x",
	} {
		parsed, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		if isPinterestHost(parsed.Hostname()) {
			t.Fatalf("%s was treated as Pinterest", address)
		}
	}
}
