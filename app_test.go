package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 32, 24))
	// Two fixtures must never come out byte for byte identical, because the
	// library deduplicates exact copies on purpose and would drop one of them.
	// An eight bit tint collided about once in every 256 runs, since temporary
	// directory names are random, which is what made canvas and folder tests
	// fail for no reason anybody could reproduce.
	var tint uint64 = 14695981039346656037
	for _, value := range []byte(path) {
		tint = (tint ^ uint64(value)) * 1099511628211
	}
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			picture.Set(x, y, color.RGBA{R: 64 + uint8(tint)/3, G: 96 + uint8(tint>>8)/4, B: 112 + uint8(tint>>16)/5, A: 255})
		}
	}
	// The full hash goes into a corner pixel so paths that happen to agree on
	// those three channels still produce different files.
	picture.Set(0, 0, color.RGBA{R: uint8(tint >> 24), G: uint8(tint >> 32), B: uint8(tint >> 40), A: 255})
	picture.Set(1, 0, color.RGBA{R: uint8(tint >> 48), G: uint8(tint >> 56), B: uint8(len(path)), A: 255})
	if err := png.Encode(file, picture); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestPNGDimensions(t *testing.T, path string, width, height uint32) {
	t.Helper()
	writeTestPNG(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the valid encoded test image but replace its IHDR dimensions and CRC.
	// DecodeConfig reads this metadata without allocating the declared pixels.
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if err := os.WriteFile(path, data, 0o644); err != nil {
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

func TestIndexFoldersPreservesManagedLibraryImports(t *testing.T) {
	app := testApplication(t)
	managed := filepath.Join(app.libraryDir, "managed-import.png")
	writeTestPNG(t, managed)
	app.addPath(managed)
	untrackedManaged := filepath.Join(app.libraryDir, "not-yet-cataloged.png")
	writeTestPNG(t, untrackedManaged)

	source := t.TempDir()
	external := filepath.Join(source, "external.png")
	writeTestPNG(t, external)
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	paths, sources, _ := app.snapshot()
	if len(paths) != 3 || !containsPath(paths, managed) || !containsPath(paths, untrackedManaged) || !containsPath(paths, external) {
		t.Fatalf("external refresh orphaned managed imports: %#v", paths)
	}
	if len(sources) != 1 || sources[0] != source {
		t.Fatalf("unexpected remembered sources: %#v", sources)
	}

	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	paths, _, _ = reloaded.snapshot()
	if len(paths) != 3 || !containsPath(paths, managed) || !containsPath(paths, untrackedManaged) || !containsPath(paths, external) {
		t.Fatalf("combined managed and external catalog did not survive restart: %#v", paths)
	}
}

func TestIndexFoldersRejectsUnsafeDecodedDimensions(t *testing.T) {
	app := testApplication(t)
	source := t.TempDir()
	safe := filepath.Join(source, "safe.png")
	unsafe := filepath.Join(source, "unsafe.png")
	writeTestPNG(t, safe)
	writeTestPNGDimensions(t, unsafe, maxDecodedImageDimension+1, 1)

	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 1 || paths[0] != safe {
		t.Fatalf("unsafe decoded dimensions entered the index: %#v", paths)
	}
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}

func TestExactDuplicateImportedImagesKeepOneFile(t *testing.T) {
	app := testApplication(t)
	first := filepath.Join(app.libraryDir, "first.png")
	second := filepath.Join(app.libraryDir, "second.png")
	writeTestPNG(t, first)
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, data, 0o644); err != nil {
		t.Fatal(err)
	}
	app.addPath(first)
	app.addPath(second)
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	paths, _, _ := reloaded.snapshot()
	if len(paths) != 1 {
		t.Fatalf("exact duplicates were not reduced: %#v", paths)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("duplicate file still exists: %v", err)
	}
}

func TestStorageSettingsCannotEnableOriginalReplacement(t *testing.T) {
	app := testApplication(t)
	if err := app.saveStorageSettings(storageSettings{OptimizeImports: true}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.storageSettings().OptimizeImports {
		t.Fatal("retired original-replacement setting was enabled")
	}
	data, err := os.ReadFile(app.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("optimizeImports")) {
		t.Fatalf("retired setting remained in config: %s", data)
	}
}

func TestIndexingAndHomeViewSettingsSurviveRestart(t *testing.T) {
	app := testApplication(t)
	if !app.indexingSettings().Automatic {
		t.Fatal("automatic indexing should be enabled by default")
	}
	if err := app.saveIndexingSettings(indexingSettings{Automatic: false}); err != nil {
		t.Fatal(err)
	}
	if err := app.saveBrowserSettings(browserSettings{ThumbnailSize: "large", ShowFilenames: true, HomeOrder: "recent"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.indexingSettings().Automatic {
		t.Fatal("disabled automatic indexing did not survive restart")
	}
	browser := reloaded.browserSettings()
	if browser.HomeOrder != "recent" || browser.ThumbnailSize != "large" || !browser.ShowFilenames {
		t.Fatalf("browser settings did not survive restart: %#v", browser)
	}
}

func TestCommandPalettePluginIsDisabledByDefault(t *testing.T) {
	app := testApplication(t)
	if app.pluginEnabled("commandPalette") {
		t.Fatal("command palette should be opt-in")
	}
	if err := app.setPluginEnabled("commandPalette", true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.pluginEnabled("commandPalette") {
		t.Fatal("enabled command palette did not survive restart")
	}
}

func TestIncrementalRefreshPreservesExistingEmbeddings(t *testing.T) {
	app := testApplication(t)
	source := t.TempDir()
	first := filepath.Join(source, "first.png")
	writeTestPNG(t, first)
	if err := app.indexFolders([]string{source}); err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
	vector[0] = 1
	if err := app.updateEmbeddings(map[string]embeddingRecord{
		first: {Mtime: embeddingMtime(first), Vector: vector},
	}); err != nil {
		t.Fatal(err)
	}

	second := filepath.Join(source, "second.png")
	writeTestPNG(t, second)
	refresh, err := app.refreshLibrary(source)
	if err != nil {
		t.Fatal(err)
	}
	if refresh.Added != 1 || refresh.Removed != 0 || refresh.Total != 2 {
		t.Fatalf("unexpected incremental refresh: %#v", refresh)
	}
	if _, ready := app.imageEmbedding(first); !ready {
		t.Fatal("incremental refresh discarded an existing embedding")
	}
	missing := app.missingEmbeddings(nil)
	if len(missing) != 1 || missing[0]["path"] != second {
		t.Fatalf("incremental refresh did not queue only the new image: %#v", missing)
	}
}

func TestCollectionNamesSupportSubfolders(t *testing.T) {
	name, err := collectionName("Film references/Characters")
	if err != nil || name != "film-references/characters" {
		t.Fatalf("unexpected nested collection name: %q, %v", name, err)
	}
}

func TestCompactEmbeddingStoreSurvivesRestart(t *testing.T) {
	app := testApplication(t)
	picture := filepath.Join(app.libraryDir, "still.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
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
	if missing := reloaded.missingEmbeddings(nil); len(missing) != 0 {
		t.Fatalf("compact embedding did not survive restart: %#v", missing)
	}
}

func TestEmbeddingModelsUseIndependentStoresAndDimensions(t *testing.T) {
	t.Setenv("PICTOGREP_HOME", t.TempDir())
	app, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	picture := filepath.Join(app.libraryDir, "still.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	defaultVector := make([]float32, defaultEmbeddingModel.Dimensions)
	defaultVector[0] = 1
	if err := app.updateEmbeddings(map[string]embeddingRecord{
		picture: {Mtime: embeddingMtime(picture), Vector: defaultVector},
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.updateQueryEmbedding("ocean", defaultVector); err != nil {
		t.Fatal(err)
	}

	alternate := defaultEmbeddingModel
	alternate.Key = "test-embedding-v1"
	alternate.Dimensions = 3
	alternate.storeFile = "embeddings-test.bin"
	alternateApp, err := newApplicationWithEmbeddingModel(alternate)
	if err != nil {
		t.Fatal(err)
	}
	if missing := alternateApp.missingEmbeddings(nil); len(missing) != 1 {
		t.Fatalf("alternate model reused default vectors: %#v", missing)
	}
	if _, found := alternateApp.queryEmbedding("ocean"); found {
		t.Fatal("alternate model reused the default query cache")
	}
	alternateVector := []float32{1, 0, 0}
	if err := alternateApp.updateEmbeddings(map[string]embeddingRecord{
		picture: {Mtime: embeddingMtime(picture), Vector: alternateVector},
	}); err != nil {
		t.Fatal(err)
	}
	if results := alternateApp.vectorSearch(alternateVector, 1, nil); len(results) != 1 {
		t.Fatalf("alternate model was not searchable: %#v", results)
	}
	if app.embeddingStorePath == alternateApp.embeddingStorePath {
		t.Fatal("embedding models share a store path")
	}
}

func TestLegacyEmbeddingMigratesWithoutReindexing(t *testing.T) {
	app := testApplication(t)
	picture := filepath.Join(app.libraryDir, "legacy.png")
	writeTestPNG(t, picture)
	app.addPath(picture)
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
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
	if missing := reloaded.missingEmbeddings(nil); len(missing) != 0 {
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
	vector := make([]float32, defaultEmbeddingModel.Dimensions)
	vector[7] = 1
	if err := app.updateQueryEmbedding("  Red   CAR ", vector); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	cached, found := reloaded.queryEmbedding("red car")
	if !found || len(cached) != defaultEmbeddingModel.Dimensions || cached[7] != 1 {
		t.Fatalf("query vector was not cached: found=%v vector=%#v", found, cached)
	}
}

func TestVectorSearchSkipsEmbeddingAfterImageChanges(t *testing.T) {
	app := testApplication(t)
	picture := filepath.Join(app.libraryDir, "changing.png")
	writeTestPNG(t, picture)
	app.addPath(picture)

	vector := make([]float32, defaultEmbeddingModel.Dimensions)
	vector[0] = 1
	record := embeddingRecord{Mtime: embeddingMtime(picture), Vector: vector}
	if err := app.updateEmbeddings(map[string]embeddingRecord{picture: record}); err != nil {
		t.Fatal(err)
	}
	if results := app.vectorSearch(vector, 10, nil); len(results) != 1 {
		t.Fatalf("current embedding was not searchable: %#v", results)
	}

	changed := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(picture, changed, changed); err != nil {
		t.Fatal(err)
	}
	if results := app.vectorSearch(vector, 10, nil); len(results) != 0 {
		t.Fatalf("stale embedding remained searchable: %#v", results)
	}
}

func TestDamagedEmbeddingRecordOnlyCostsItsOwnPicture(t *testing.T) {
	app := testApplication(t)
	vector := make([]float32, app.embeddingModel.Dimensions)
	for index := range vector {
		vector[index] = 0.25
	}
	// Equal-length names keep every record the same size, so the test can point
	// at the middle one.
	names := []string{"/aaa.png", "/bbb.png", "/ccc.png"}
	store := []byte{}
	for _, name := range names {
		data, err := encodeEmbedding(app.embeddingModel, name, embeddingRecord{Mtime: 7, Vector: vector})
		if err != nil {
			t.Fatal(err)
		}
		store = append(store, data...)
	}
	recordSize := len(store) / len(names)
	store[recordSize+20] ^= 0xFF
	if err := os.WriteFile(app.embeddingStorePath, store, 0o600); err != nil {
		t.Fatal(err)
	}
	app.embeddings = map[string]embeddingRecord{}
	if err := app.loadEmbeddingStore(); err != nil {
		t.Fatal(err)
	}
	if _, found := app.embeddings[expandPath("/ccc.png")]; !found {
		t.Fatalf("a damaged record took every record after it: %d of 3 survived", len(app.embeddings))
	}
	if _, found := app.embeddings[expandPath("/aaa.png")]; !found {
		t.Fatal("a damaged record lost the record before it")
	}
	if _, found := app.embeddings[expandPath("/bbb.png")]; found {
		t.Fatal("a damaged record loaded anyway")
	}
	// The damage is rewritten away, so the next start reads a clean file. The
	// records come back keyed by their expanded path, which on Windows is
	// longer than the name written here ("/aaa.png" becomes "C:\aaa.png"), so
	// the size that survives is measured rather than assumed.
	want := 0
	for _, name := range []string{"/aaa.png", "/ccc.png"} {
		data, err := encodeEmbedding(app.embeddingModel, expandPath(name), embeddingRecord{Mtime: 7, Vector: vector})
		if err != nil {
			t.Fatal(err)
		}
		want += len(data)
	}
	info, err := os.Stat(app.embeddingStorePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(want) {
		t.Fatalf("the damaged store was not rewritten: size=%d want=%d", info.Size(), want)
	}
}

func TestInterruptedEmbeddingAppendIsTrimmed(t *testing.T) {
	app := testApplication(t)
	vector := make([]float32, app.embeddingModel.Dimensions)
	data, err := encodeEmbedding(app.embeddingModel, "/one.png", embeddingRecord{Mtime: 3, Vector: vector})
	if err != nil {
		t.Fatal(err)
	}
	// A crash mid-append leaves half a record behind, which is not corruption.
	store := append(append([]byte{}, data...), data[:len(data)/2]...)
	if err := os.WriteFile(app.embeddingStorePath, store, 0o600); err != nil {
		t.Fatal(err)
	}
	app.embeddings = map[string]embeddingRecord{}
	if err := app.loadEmbeddingStore(); err != nil {
		t.Fatal(err)
	}
	if len(app.embeddings) != 1 {
		t.Fatalf("a torn final record lost the whole store: %d records", len(app.embeddings))
	}
	info, err := os.Stat(app.embeddingStorePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(data)) {
		t.Fatalf("the torn record was not trimmed: size=%d want=%d", info.Size(), len(data))
	}
}

func TestAddPathsTakesAWholeImportInOneWrite(t *testing.T) {
	app := testApplication(t)
	first := filepath.Join(app.libraryDir, "first.png")
	second := filepath.Join(app.libraryDir, "second.png")
	writeTestPNG(t, first)
	writeTestPNG(t, second)
	if added := app.addPaths([]string{first, second, first}); added != 2 {
		t.Fatalf("addPaths counted a repeat twice: added=%d", added)
	}
	if added := app.addPaths([]string{second}); added != 0 {
		t.Fatalf("addPaths added a picture the library already had: added=%d", added)
	}
	paths, _, _ := app.snapshot()
	if len(paths) != 2 {
		t.Fatalf("the library does not hold the import: %#v", paths)
	}
	saved, err := os.ReadFile(app.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state libraryState
	if err := json.Unmarshal(saved, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Images) != 2 {
		t.Fatalf("the batched import was not saved to disk: %#v", state.Images)
	}
}

func TestPreparingAPictureReadsItsPreview(t *testing.T) {
	app := testApplication(t)
	picture := filepath.Join(app.libraryDir, "reference.png")
	writeTestPNG(t, picture)
	app.addPath(picture)

	missing := app.missingEmbeddings(nil)
	if len(missing) != 1 {
		t.Fatalf("the library did not queue its only picture: %#v", missing)
	}
	id := stableImageID(picture)
	// Reaching a 224 pixel square by decoding a whole photograph is the slowest
	// part of preparing a library, so preparing reads the preview instead.
	if missing[0]["url"] != "/thumbnail/"+id+"?size=512" {
		t.Fatalf("preparing did not read a preview: %#v", missing[0])
	}
	// A picture whose dimensions are unsafe to resize has no preview, and without
	// the original it would drop out of every search instead.
	if missing[0]["originalUrl"] != "/image/"+id {
		t.Fatalf("preparing has nothing to fall back to: %#v", missing[0])
	}
}
