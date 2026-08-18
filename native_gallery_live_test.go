package main

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
)

// The downloader against the real Pinterest, which is the only thing that can
// answer whether it still works.
//
// Skipped unless PICTOGREP_LIVE is set, because it needs the network and
// because Pinterest, not this repository, decides whether it passes. It exists
// for the day an import starts coming back empty: run it and the answer is
// immediate, rather than reasoning about which of three JSON shapes changed.
// The extractor has already been wrong once in exactly that way, silently, and
// the unit tests above could not have caught it: they test that the code reads
// the shape it was written for, not that Pinterest still sends that shape.
//
//	PICTOGREP_LIVE=1 go test -run TestLivePinterest -v
func TestLivePinterestBoardStillReads(t *testing.T) {
	if os.Getenv("PICTOGREP_LIVE") == "" {
		t.Skip("set PICTOGREP_LIVE=1 to run this against the real Pinterest")
	}
	// A board that belongs to Pinterest itself, so it is public, stays public,
	// and is nobody's personal collection.
	address, err := url.Parse("https://www.pinterest.com/pinterest/official-news/")
	if err != nil {
		t.Fatal(err)
	}

	images, err := pinterestBoardImages(context.Background(), address, 40)
	if err != nil {
		t.Fatalf("reading a public board failed: %v", err)
	}
	if len(images) < 10 {
		t.Fatalf("a board with dozens of pins gave up %d pictures, so something Pinterest sends has changed", len(images))
	}
	for _, image := range images {
		if !strings.HasPrefix(image.URL, "http") || !strings.HasPrefix(image.ID, "pinterest:") {
			t.Fatalf("unusable picture: %+v", image)
		}
	}

	// Downloading one proves the addresses are real and reachable, not just
	// well shaped.
	directory := t.TempDir()
	if err := downloadAll(context.Background(), images[:1], galleryDLRequest{Directory: directory}, 1); err != nil {
		t.Fatal(err)
	}
	if files := downloadedFiles(t, directory); len(files) != 1 {
		t.Fatalf("downloaded %v from a live board", files)
	}
	t.Logf("read %d pictures from a live board, first: %s", len(images), images[0].URL)
}
