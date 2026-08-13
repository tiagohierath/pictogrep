package main

import "testing"

func TestWikimediaEmptySearchUsesRandomFiles(t *testing.T) {
	params := wikimediaParams("", "ignored", 40)
	if params.Get("generator") != "random" || params.Get("grnnamespace") != "6" || params.Get("grnlimit") != "40" {
		t.Fatalf("empty Wikimedia search is not random: %s", params.Encode())
	}
	if params.Get("gsrsearch") != "" || params.Get("gsrcontinue") != "" {
		t.Fatalf("random Wikimedia request retained search state: %s", params.Encode())
	}
}

func TestWikimediaTextSearchUsesFileNamespace(t *testing.T) {
	params := wikimediaParams("red car", "next-page", 25)
	if params.Get("generator") != "search" || params.Get("gsrsearch") != "red car" || params.Get("gsrnamespace") != "6" || params.Get("gsrlimit") != "25" {
		t.Fatalf("Wikimedia text search is malformed: %s", params.Encode())
	}
	if params.Get("gsrcontinue") != "next-page" || params.Get("grnlimit") != "" {
		t.Fatalf("Wikimedia text search mixed random parameters: %s", params.Encode())
	}
}
