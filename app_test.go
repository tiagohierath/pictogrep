package main

import (
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
