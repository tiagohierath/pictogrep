package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const wikimediaAPI = "https://commons.wikimedia.org/w/api.php"

type wikimediaImage struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	ThumbnailURL   string `json:"thumbnailUrl"`
	OriginalURL    string `json:"originalUrl"`
	DescriptionURL string `json:"descriptionUrl"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
}

type wikimediaSearchResult struct {
	Images   []wikimediaImage `json:"images"`
	Continue string           `json:"continue,omitempty"`
}

func searchWikimedia(ctx context.Context, query, continuation string, limit int) (wikimediaSearchResult, error) {
	query = strings.TrimSpace(query)
	if limit < 1 || limit > 50 {
		limit = 30
	}
	params := wikimediaParams(query, continuation, limit)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, wikimediaAPI+"?"+params.Encode(), nil)
	if err != nil {
		return wikimediaSearchResult{}, err
	}
	request.Header.Set("User-Agent", "Pictogrep/0.4 (local image browser; contact via GitHub)")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return wikimediaSearchResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return wikimediaSearchResult{}, fmt.Errorf("Wikimedia Commons returned %s", response.Status)
	}
	var payload struct {
		Query struct {
			Pages []struct {
				PageID    int    `json:"pageid"`
				Title     string `json:"title"`
				ImageInfo []struct {
					URL            string `json:"url"`
					ThumbURL       string `json:"thumburl"`
					Width          int    `json:"width"`
					Height         int    `json:"height"`
					MIME           string `json:"mime"`
					DescriptionURL string `json:"descriptionurl"`
				} `json:"imageinfo"`
			} `json:"pages"`
		} `json:"query"`
		Continue struct {
			Search string `json:"gsrcontinue"`
		} `json:"continue"`
		Error *struct {
			Info string `json:"info"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return wikimediaSearchResult{}, err
	}
	if payload.Error != nil {
		return wikimediaSearchResult{}, fmt.Errorf("Wikimedia Commons: %s", payload.Error.Info)
	}
	result := wikimediaSearchResult{Continue: payload.Continue.Search}
	for _, page := range payload.Query.Pages {
		if len(page.ImageInfo) == 0 || page.ImageInfo[0].ThumbURL == "" {
			continue
		}
		info := page.ImageInfo[0]
		if !strings.HasPrefix(info.MIME, "image/") {
			continue
		}
		result.Images = append(result.Images, wikimediaImage{
			ID: strconv.Itoa(page.PageID), Title: strings.TrimPrefix(page.Title, "File:"),
			ThumbnailURL: info.ThumbURL, OriginalURL: info.URL,
			DescriptionURL: info.DescriptionURL, Width: info.Width, Height: info.Height,
		})
	}
	if query == "" {
		// Wikimedia chooses a random starting point. Shuffle that returned batch
		// as well so every empty-search visit feels genuinely unsorted.
		rand.Shuffle(len(result.Images), func(i, j int) {
			result.Images[i], result.Images[j] = result.Images[j], result.Images[i]
		})
	}
	return result, nil
}

func wikimediaParams(query, continuation string, limit int) url.Values {
	params := url.Values{
		"action":        {"query"},
		"format":        {"json"},
		"formatversion": {"2"},
		"prop":          {"imageinfo"},
		"iiprop":        {"url|size|mime"},
		"iiurlwidth":    {"720"},
	}
	if query == "" {
		params.Set("generator", "random")
		params.Set("grnnamespace", "6")
		params.Set("grnfilterredir", "nonredirects")
		params.Set("grnlimit", strconv.Itoa(limit))
	} else {
		params.Set("generator", "search")
		params.Set("gsrsearch", query)
		params.Set("gsrnamespace", "6")
		params.Set("gsrlimit", strconv.Itoa(limit))
	}
	if query != "" && continuation != "" {
		params.Set("gsrcontinue", continuation)
	}
	return params
}
