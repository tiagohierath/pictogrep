//go:build android || pictogrep_android

package main

// The phone's half of native_gallery_pinterest.go: nothing.
//
// A board importer is not offered in the app build, so the address is never
// recognised as one and the reader is never called. Both exist only so the
// dispatch in native_gallery.go compiles the same way on both platforms
// without a build tag in the middle of it.

import (
	"context"
	"fmt"
	"net/url"
)

func isPinterestHost(string) bool { return false }

func pinterestBoardImages(context.Context, *url.URL, int) ([]nativeGalleryImage, error) {
	return nil, fmt.Errorf("boards are not imported in the app")
}
