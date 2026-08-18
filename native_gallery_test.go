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

// A Pinterest that behaves like the real one: a board page carrying its state
// in a script tag, and a feed endpoint that pages through the rest.
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

	mux.HandleFunc("/someone/a-board/", func(w http.ResponseWriter, r *http.Request) {
		first := map[string]any{}
		for index := 0; index < perPage && index < pins; index++ {
			first[fmt.Sprint(index)] = pin(index)
		}
		state := map[string]any{"props": map[string]any{"initialReduxState": map[string]any{
			"pins":   first,
			"boards": map[string]any{"b1": map[string]any{"id": "8891", "url": "/someone/a-board/"}},
		}}}
		encoded, _ := json.Marshal(state)
		fmt.Fprintf(w, `<html><body><script id="__PWS_DATA__" type="application/json">%s</script></body></html>`, encoded)
	})

	mux.HandleFunc("/resource/BoardFeedResource/get/", func(w http.ResponseWriter, r *http.Request) {
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
		from := perPage
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
	})

	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG(t, 200))
	})

	t.Cleanup(server.Close)
	return server
}

// Nothing has to pretend to be pinterest.com: the downloader decides what a
// page is from the state blob it carries, and pages the board against the host
// the page came from, which here is the test server.
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

func TestNativeDownloaderWalksAWholePinterestBoard(t *testing.T) {
	server := fakePinterest(t, 30, 10)
	directory := t.TempDir()
	if err := downloadFrom(t, server, "/someone/a-board/", galleryDLRequest{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	// Ten pins came with the page, twenty more from two pages of the feed.
	if files := downloadedFiles(t, directory); len(files) != 30 {
		t.Fatalf("downloaded %d pictures from a 30 pin board: %v", len(files), files)
	}
}

func TestNativeDownloaderTakesTheOriginalNotTheThumbnail(t *testing.T) {
	asked := make(chan string, 64)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/someone/a-board/", func(w http.ResponseWriter, r *http.Request) {
		state := map[string]any{"props": map[string]any{"initialReduxState": map[string]any{
			"pins": map[string]any{"0": map[string]any{
				"id": "1",
				"images": map[string]any{
					"236x": map[string]any{"url": server.URL + "/img/small.png", "width": 236, "height": 236},
					"orig": map[string]any{"url": server.URL + "/img/original.png", "width": 2000, "height": 2000},
				},
			}},
		}}}
		encoded, _ := json.Marshal(state)
		fmt.Fprintf(w, `<script id="__PWS_DATA__" type="application/json">%s</script>`, encoded)
	})
	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		asked <- r.URL.Path
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG(t, 10))
	})

	if err := downloadFrom(t, server, "/someone/a-board/", galleryDLRequest{}); err != nil {
		t.Fatal(err)
	}
	close(asked)
	for path := range asked {
		if path != "/img/original.png" {
			t.Fatalf("downloaded %s instead of the original", path)
		}
	}
}

func TestNativeDownloaderStopsAtTheLimit(t *testing.T) {
	server := fakePinterest(t, 100, 10)
	directory := t.TempDir()
	if err := downloadFrom(t, server, "/someone/a-board/", galleryDLRequest{Directory: directory, Limit: 12}); err != nil {
		t.Fatal(err)
	}
	if files := downloadedFiles(t, directory); len(files) != 12 {
		t.Fatalf("a limit of 12 downloaded %d pictures", len(files))
	}
}

// Following a board is only cheap if the second check downloads nothing.
func TestNativeDownloaderRemembersWhatItAlreadyHas(t *testing.T) {
	server := fakePinterest(t, 20, 10)
	archive := filepath.Join(t.TempDir(), "seen.sqlite")

	first := t.TempDir()
	if err := downloadFrom(t, server, "/someone/a-board/", galleryDLRequest{Directory: first, Archive: archive}); err != nil {
		t.Fatal(err)
	}
	if files := downloadedFiles(t, first); len(files) != 20 {
		t.Fatalf("the first check downloaded %d of 20 pictures", len(files))
	}

	second := t.TempDir()
	if err := downloadFrom(t, server, "/someone/a-board/", galleryDLRequest{Directory: second, Archive: archive}); err != nil {
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
