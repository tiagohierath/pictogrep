//go:build !android && !pictogrep_android

package main

// Reading a Pinterest board, which only a desktop build carries.
//
// Not merely unreachable on a phone but absent from it: the app build serves
// no board importer (see platform_mobile.go for why Play makes that the only
// sane call), and code that cannot be reached is still code that ships, still
// something a reviewer reads, and still the thing an app gets taken down for
// months after release. The stub next door is what the phone links instead.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func isPinterestHost(host string) bool {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	return host == "pinterest.com" || strings.HasSuffix(host, ".pinterest.com") ||
		host == "pin.it"
}

// ---------------------------------------------------------------------------
// Pinterest
// ---------------------------------------------------------------------------
//
// Boards are read through the same two JSON endpoints the site's own interface
// calls, and not out of the HTML.
//
// The HTML was the obvious route and it is a trap. Pinterest used to ship the
// first screen's state in a <script id="__PWS_DATA__"> tag; that tag is still
// there and now carries only routing configuration, so parsing it yields zero
// pins, no error, and an import that silently saves nothing. The state moved to
// __PWS_INITIAL_PROPS__, and the page also reports renderMode "shellReady" with
// unauthenticated lockdown experiments switched on, which is a payload being
// actively thinned. The JSON endpoints are both simpler and the more durable
// bet: 3.5 KB instead of a megabyte of HTML, a real 404 for a board that does
// not exist, and no dependence on how much of the page is rendered on the
// server this month.
//
// One header does all the work. Without X-Pinterest-PWS-Handler naming a real
// route, both endpoints answer 403 "Invalid Resource Request". With it, and
// nothing else, they answer 200: no cookies, no CSRF token, no sign-in, and the
// user agent turns out not to matter at all.
const pinterestHandler = "www/[username]/[slug].js"

// How many pins to ask for at a time. The site's own scroll uses 25; the
// endpoint honours far more, and a board of 800 pins is 4 requests instead of
// 33, which on mobile data is the difference worth having.
const pinterestPageSize = 250

// pinterestResource calls one of Pinterest's resource endpoints and returns the
// raw JSON body.
func pinterestResource(ctx context.Context, site *url.URL, resource string, options map[string]any) (string, error) {
	payload, err := json.Marshal(map[string]any{"options": options, "context": map[string]any{}})
	if err != nil {
		return "", err
	}
	endpoint := &url.URL{
		// The host the board was opened from. Pinterest runs a site per country
		// and a board opened on br.pinterest.com answers there.
		Scheme: site.Scheme, Host: site.Host,
		Path: "/resource/" + resource + "/get/",
		RawQuery: url.Values{
			"source_url": {site.EscapedPath()},
			"data":       {string(payload)},
		}.Encode(),
	}
	body, _, err := fetchText(ctx, endpoint.String(), map[string]string{
		"Accept":                  "application/json, text/javascript, */*, q=0.01",
		"X-Pinterest-PWS-Handler": pinterestHandler,
		"Referer":                 site.String(),
	})
	return body, err
}

// pinterestBoardImages lists every picture on a public board.
func pinterestBoardImages(ctx context.Context, board *url.URL, limit int) ([]nativeGalleryImage, error) {
	// A pin.it link is a redirect and carries nothing in its own path, so it
	// has to be followed before anything can be read out of the address. It
	// goes through api.pinterest.com and lands on a pin, or sometimes a board.
	shortened := strings.EqualFold(board.Hostname(), "pin.it")
	if shortened {
		_, final, err := fetchTextFrom(ctx, board, nil)
		if err != nil {
			return nil, importError(http.StatusBadGateway,
				"that pin.it link could not be opened: %v", err)
		}
		board = final
	}

	parts := strings.FieldsFunc(board.Path, func(r rune) bool { return r == '/' })
	// Where a pin.it link usually lands: one picture, not a board. Sharing a
	// single pin from the Pinterest app is the commonest thing anyone does on a
	// phone, so it has to mean "save this picture" rather than an error.
	if len(parts) == 2 && parts[0] == "pin" {
		return pinterestSinglePin(ctx, board, parts[1])
	}
	if len(parts) < 2 {
		// A shortened link that resolved to the front page: expired, or never
		// valid. Saying "that is a profile" would send the user looking for a
		// mistake they did not make.
		if shortened {
			return nil, importError(http.StatusBadRequest,
				"that pin.it link does not lead anywhere any more. Open the pin in Pinterest and "+
					"share it again, or paste the board's own address.")
		}
		return nil, importError(http.StatusBadRequest,
			"that is a Pinterest profile, not a board. Open the board you want and copy its address, "+
				"which looks like pinterest.com/someone/a-board/.")
	}
	username, slug := parts[0], parts[1]

	body, err := pinterestResource(ctx, board, "BoardResource", map[string]any{
		"username": username, "slug": slug,
	})
	var answered siteStatus
	if errors.As(err, &answered) && answered.code == http.StatusNotFound {
		err = nil
		body = ""
	}
	if err != nil {
		return nil, importError(http.StatusBadGateway,
			"Pinterest would not open that board: %v", err)
	}
	var boardAnswer struct {
		ResourceResponse struct {
			Data struct {
				ID       string `json:"id"`
				PinCount int    `json:"pin_count"`
			} `json:"data"`
		} `json:"resource_response"`
	}
	if err := json.Unmarshal([]byte(body), &boardAnswer); err != nil || boardAnswer.ResourceResponse.Data.ID == "" {
		return nil, importError(http.StatusBadRequest,
			"there is no public board at that address. Check it, and note that a private board "+
				"cannot be read even when you are the one who made it.")
	}

	images := []nativeGalleryImage{}
	seen := map[string]bool{}
	bookmark := ""
	for page := 0; page < nativeGalleryMaxPages && len(images) < limit; page++ {
		next, nextBookmark, err := pinterestBoardPage(ctx, board, boardAnswer.ResourceResponse.Data.ID, bookmark)
		if err != nil {
			// Losing pagination costs the rest of the board, not the part
			// already in hand.
			break
		}
		for _, image := range next {
			if !seen[image.ID] {
				seen[image.ID] = true
				images = append(images, image)
			}
		}
		// The last page says so in one of two places depending on the endpoint's
		// mood: an absent bookmark, or the sentinel.
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

// pinterestSinglePin reads one pin.
func pinterestSinglePin(ctx context.Context, site *url.URL, id string) ([]nativeGalleryImage, error) {
	body, err := pinterestResource(ctx, site, "PinResource", map[string]any{
		"id": id, "field_set_key": "detailed",
	})
	var answered siteStatus
	if errors.As(err, &answered) && answered.code == http.StatusNotFound {
		return nil, importError(http.StatusBadRequest, "that pin does not exist any more")
	}
	if err != nil {
		return nil, importError(http.StatusBadGateway, "Pinterest would not open that pin: %v", err)
	}
	var answer struct {
		ResourceResponse struct {
			Data json.RawMessage `json:"data"`
		} `json:"resource_response"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		return nil, importError(http.StatusBadGateway, "Pinterest sent a pin Pictogrep could not read")
	}
	image, ok := pinterestPinImage(answer.ResourceResponse.Data, site)
	if !ok {
		return nil, importError(http.StatusBadRequest,
			"there is no picture on that pin. Video pins and idea pins cannot be saved.")
	}
	return []nativeGalleryImage{image}, nil
}

// pinterestBoardPage asks for one page of a board the way the site's own
// scrolling does.
func pinterestBoardPage(ctx context.Context, board *url.URL, boardID, bookmark string) ([]nativeGalleryImage, string, error) {
	options := map[string]any{"board_id": boardID, "page_size": pinterestPageSize}
	if bookmark != "" {
		options["bookmarks"] = []string{bookmark}
	}
	body, err := pinterestResource(ctx, board, "BoardFeedResource", options)
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
