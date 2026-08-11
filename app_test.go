package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			picture.Set(x, y, color.RGBA{R: 80, G: 100, B: 120, A: 255})
		}
	}
	if err := png.Encode(file, picture); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func testApplication(t *testing.T) *application {
	t.Helper()
	t.Setenv("PICTOGREP_HOME", t.TempDir())
	t.Setenv("PICTOGREP_AI_BACKEND", "")
	app, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestStandaloneLibraryIndexAndFilenameSearch(t *testing.T) {
	app := testApplication(t)
	folder := t.TempDir()
	picture := filepath.Join(folder, "foggy-street.png")
	writeTestPNG(t, picture)
	if err := app.indexFolders([]string{folder}); err != nil {
		t.Fatal(err)
	}
	paths, sources, _ := app.snapshot()
	if len(paths) != 1 || paths[0] != picture {
		t.Fatalf("unexpected paths: %#v", paths)
	}
	if len(sources) != 1 || sources[0] != folder {
		t.Fatalf("unexpected sources: %#v", sources)
	}
	results, ai, err := app.search("foggy street", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ai || len(results) != 1 || results[0].Path != picture {
		t.Fatalf("unexpected search: ai=%v results=%#v", ai, results)
	}
}

func TestFilenameSearchIncludesSourceSubfolders(t *testing.T) {
	app := testApplication(t)
	source := t.TempDir()
	folder := filepath.Join(source, "vehicles", "favorites")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	picture := filepath.Join(folder, "reference.png")
	writeTestPNG(t, picture)
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	results, _, err := app.search("vehicles", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != picture {
		t.Fatalf("subfolder text was not searchable: %#v", results)
	}
}

func TestLibraryStateSurvivesRestart(t *testing.T) {
	app := testApplication(t)
	picture := filepath.Join(app.libraryDir, "still.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	paths, _, _ := reloaded.snapshot()
	if len(paths) != 1 || paths[0] != picture {
		t.Fatalf("state was not restored: %#v", paths)
	}
}

func TestIndexedSourceSurvivesRestart(t *testing.T) {
	app := testApplication(t)
	source := t.TempDir()
	writeTestPNG(t, filepath.Join(source, "reference.png"))
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	_, sources, _ := reloaded.snapshot()
	if len(sources) != 1 || sources[0] != source {
		t.Fatalf("source was not restored: %#v", sources)
	}
}

func TestCompactEmbeddingStoreSurvivesRestart(t *testing.T) {
	app := testApplication(t)
	picture := filepath.Join(app.libraryDir, "still.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	vector := make([]float32, semanticVectorSize)
	vector[0] = 1
	record := embeddingRecord{Mtime: embeddingMtime(picture), Vector: vector}
	if err := app.updateEmbeddings(map[string]embeddingRecord{picture: record}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(app.embeddingStorePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 3<<10 {
		t.Fatalf("single compact embedding is unexpectedly large: %d bytes", info.Size())
	}
	initialSize := info.Size()
	if err := app.updateEmbeddings(map[string]embeddingRecord{picture: record}); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(app.embeddingStorePath)
	if info.Size() != initialSize {
		t.Fatalf("unchanged image was embedded again: %d -> %d", initialSize, info.Size())
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if missing := reloaded.missingEmbeddings(); len(missing) != 0 {
		t.Fatalf("compact embedding did not survive restart: %#v", missing)
	}
}

func TestLegacyEmbeddingMigratesWithoutReindexing(t *testing.T) {
	app := testApplication(t)
	picture := filepath.Join(app.libraryDir, "legacy.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	vector := make([]float32, semanticVectorSize)
	vector[1] = 1
	info, _ := os.Stat(picture)
	legacy := storedEmbedding{Path: picture, embeddingRecord: embeddingRecord{Mtime: info.ModTime().Unix(), Vector: vector}}
	data, _ := json.Marshal(legacy)
	legacyPath := filepath.Join(app.embeddingsDir, "legacy.json")
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if missing := reloaded.missingEmbeddings(); len(missing) != 0 {
		t.Fatalf("legacy embedding was not preserved: %#v", missing)
	}
	if _, err := os.Stat(reloaded.embeddingStorePath); err != nil {
		t.Fatalf("compact store was not created: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy cache was not retired after migration: %v", err)
	}
}

func TestQueryEmbeddingCacheSurvivesRestart(t *testing.T) {
	app := testApplication(t)
	vector := make([]float32, semanticVectorSize)
	vector[7] = 1
	if err := app.updateQueryEmbedding("  Red   CAR ", vector); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	cached, found := reloaded.queryEmbedding("red car")
	if !found || len(cached) != semanticVectorSize || cached[7] != 1 {
		t.Fatalf("query vector was not cached: found=%v vector=%#v", found, cached)
	}
}
