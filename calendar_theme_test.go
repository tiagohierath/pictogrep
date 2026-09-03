package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// unitVector builds a vector pointing at one axis, so a picture can be made to
// sit exactly on a phrase in these tests.
func unitVector(axis int) []float32 {
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
	vector[axis] = 1
	return vector
}

func TestDescribeThemeNamesTheLeaningOfTheMonth(t *testing.T) {
	app := testApplication(t)
	source := t.TempDir()
	paths := []string{}
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		path := filepath.Join(source, name)
		writeTestPNG(t, path)
		paths = append(paths, path)
	}
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	records := map[string]embeddingRecord{}
	for _, path := range paths {
		records[path] = embeddingRecord{Mtime: embeddingMtime(path), Vector: unitVector(0)}
	}
	if err := app.updateEmbeddings(records); err != nil {
		t.Fatal(err)
	}

	// No phrase has been encoded yet, so there is nothing to say.
	if theme := app.describeTheme(paths, nil); theme != "" {
		t.Fatalf("expected no theme before the phrases are encoded, got %q", theme)
	}
	if len(app.missingThemePrompts()) != len(themePrompts()) {
		t.Fatalf("expected every phrase to be missing")
	}

	// Give every phrase a vector. One phrase per facet is put on the same axis
	// as the pictures; the rest point elsewhere and must lose.
	winners := []string{}
	axis := 1
	for _, facet := range themeFacets {
		for index, option := range facet.Options {
			vector := unitVector(axis)
			axis++
			if index == 2 {
				vector = unitVector(0)
				winners = append(winners, option.Label)
			}
			if err := app.updateQueryEmbedding(option.Prompt, vector); err != nil {
				t.Fatal(err)
			}
		}
	}
	if missing := app.missingThemePrompts(); len(missing) != 0 {
		t.Fatalf("expected no missing phrases, got %v", missing)
	}

	theme := app.describeTheme(paths, nil)
	expected := capitalizeFirst(strings.Join(winners, ", "))
	if theme != expected {
		t.Fatalf("theme = %q, want %q", theme, expected)
	}

	// A handful of pictures is not a month worth describing.
	if theme := app.describeTheme(paths[:2], nil); theme != "" {
		t.Fatalf("expected no theme for %d pictures, got %q", 2, theme)
	}
}

func TestDescribeThemeIgnoresWhatTheWholeLibraryAlreadyIs(t *testing.T) {
	app := testApplication(t)
	source := t.TempDir()
	paths := []string{}
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		path := filepath.Join(source, name)
		writeTestPNG(t, path)
		paths = append(paths, path)
	}
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	month := make([]float32, defaultEmbeddingModel.Dimensions)
	month[0], month[1] = 0.6, 0.8
	records := map[string]embeddingRecord{}
	for _, path := range paths {
		records[path] = embeddingRecord{Mtime: embeddingMtime(path), Vector: month}
	}
	if err := app.updateEmbeddings(records); err != nil {
		t.Fatal(err)
	}
	// The first phrase is the closest match to this month's pictures, but it is
	// equally what the whole library looks like, so it says nothing. The second
	// is further away and belongs to this month alone, which is the more
	// telling description.
	facet := themeFacets[0]
	for index, option := range facet.Options {
		vector := unitVector(index + 10)
		if index == 0 {
			vector = append([]float32(nil), month...)
		} else if index == 1 {
			vector = unitVector(1)
		}
		if err := app.updateQueryEmbedding(option.Prompt, vector); err != nil {
			t.Fatal(err)
		}
	}
	// A library that is mostly the sort of picture the first phrase describes.
	library := make([]float64, defaultEmbeddingModel.Dimensions)
	library[0] = 1
	theme := app.describeTheme(paths, library)
	if !strings.HasPrefix(theme, capitalizeFirst(facet.Options[1].Label)) {
		t.Fatalf("theme = %q, want it to start with %q", theme, facet.Options[1].Label)
	}
}

func TestCalendarViewCarriesThemesAndMissingPrompts(t *testing.T) {
	app, httpServer := testHTTPServer(t)
	if err := app.setPluginEnabled("calendar", true); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	paths := []string{}
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		path := filepath.Join(source, name)
		writeTestPNG(t, path)
		paths = append(paths, path)
	}
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	records := map[string]embeddingRecord{}
	for _, path := range paths {
		records[path] = embeddingRecord{Mtime: embeddingMtime(path), Vector: unitVector(0)}
	}
	if err := app.updateEmbeddings(records); err != nil {
		t.Fatal(err)
	}

	response, err := http.Get(httpServer.URL + "/api/app/plugins/calendar")
	if err != nil {
		t.Fatal(err)
	}
	data := responseJSON(t, response)
	prompts, _ := data["themePrompts"].([]any)
	if len(prompts) != len(themePrompts()) {
		t.Fatalf("expected every phrase to be reported as missing, got %d", len(prompts))
	}
	groups, _ := data["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected one group, got %d", len(groups))
	}
	if theme := groups[0].(map[string]any)["theme"]; theme != nil {
		t.Fatalf("expected no theme without encoded phrases, got %v", theme)
	}

	winner := themeFacets[0].Options[1]
	if err := app.updateQueryEmbedding(winner.Prompt, unitVector(0)); err != nil {
		t.Fatal(err)
	}
	response, err = http.Get(httpServer.URL + "/api/app/plugins/calendar")
	if err != nil {
		t.Fatal(err)
	}
	data = responseJSON(t, response)
	groups, _ = data["groups"].([]any)
	if theme := groups[0].(map[string]any)["theme"]; theme != winner.Label {
		t.Fatalf("theme = %v, want %q", theme, winner.Label)
	}
}
