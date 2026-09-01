package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pluginRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765"+target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestInstalledPluginCanLoadInsideSandbox(t *testing.T) {
	app := testApplication(t)
	directory := filepath.Join(app.pluginsDir, "room-view", "ui")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "id": "dev.navylily.roomview",
  "name": "Room view",
  "version": "0.1.0",
  "apiVersion": "0",
  "entry": "ui/index.html",
  "permissions": ["images.list", "filesystem.read", "ui.panel"]
}`
	if err := os.WriteFile(filepath.Join(app.pluginsDir, "room-view", "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte(`<script src="/assets/plugin-sdk.js"></script><script src="room.js"></script>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "room.js"), []byte(`pictogrep.images.list()`), 0o644); err != nil {
		t.Fatal(err)
	}
	brokenDirectory := filepath.Join(app.pluginsDir, "half-copied")
	if err := os.MkdirAll(brokenDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDirectory, "plugin.json"), []byte(`{
  "id": "dev.navylily.incomplete",
  "name": "Incomplete",
  "version": "0.1.0",
  "entry": "ui/missing.html",
  "permissions": ["ui.panel"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	scaffoldDirectory := filepath.Join(app.pluginsDir, "scaffold")
	if err := os.MkdirAll(scaffoldDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scaffoldDirectory, "plugin.json"), []byte(`{
  "id": "dev.navylily.scaffold",
  "name": "Scaffold",
  "version": "0.0.0",
  "entry": "index.html",
  "permissions": ["ui.panel"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scaffoldDirectory, "index.html"), []byte("not released"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()
	installed := pluginRequest(handler, "/api/app/plugins/installed")
	if installed.Code != http.StatusOK {
		t.Fatalf("installed plugins status=%d body=%s", installed.Code, installed.Body.String())
	}
	var installedPayload struct {
		Plugins []struct {
			ID          string   `json:"id"`
			Permissions []string `json:"permissions"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(installed.Body.Bytes(), &installedPayload); err != nil {
		t.Fatal(err)
	}
	if len(installedPayload.Plugins) != 1 || installedPayload.Plugins[0].ID != "dev.navylily.roomview" {
		t.Fatalf("incomplete plugin was offered as installed: %#v", installedPayload.Plugins)
	}
	if strings.Join(installedPayload.Plugins[0].Permissions, ",") != "images.list,ui.panel" {
		t.Fatalf("unknown manifest permission reached the broker: %#v", installedPayload.Plugins[0].Permissions)
	}
	// ServeFile canonicalizes an explicit index.html to the containing URL so
	// relative plugin assets resolve beside it, just as they do in the browser.
	page := pluginRequest(handler, "/plugin/dev.navylily.roomview/ui/")
	if page.Code != http.StatusOK {
		t.Fatalf("plugin page status=%d body=%s", page.Code, page.Body.String())
	}
	if got := page.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("plugin page cannot be framed by Pictogrep: X-Frame-Options=%q", got)
	}
	if got := page.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Fatalf("sandbox cannot load plugin subresources: CORP=%q", got)
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("unexpected plugin CSP: %q", csp)
	}

	script := pluginRequest(handler, "/plugin/dev.navylily.roomview/ui/room.js")
	if script.Code != http.StatusOK || script.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatalf("plugin script is unavailable inside sandbox: status=%d CORP=%q", script.Code, script.Header().Get("Cross-Origin-Resource-Policy"))
	}
	sdk := pluginRequest(handler, "/assets/plugin-sdk.js")
	if sdk.Code != http.StatusOK || sdk.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatalf("plugin SDK is unavailable inside sandbox: status=%d CORP=%q", sdk.Code, sdk.Header().Get("Cross-Origin-Resource-Policy"))
	}
}

func TestPluginMediaURLIsScopedByBearerToken(t *testing.T) {
	app := testApplication(t)
	directory := filepath.Join(app.pluginsDir, "board", "ui")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.pluginsDir, "board", "plugin.json"), []byte(`{
  "id": "dev.navylily.board",
  "name": "Board",
  "version": "1.0.0",
  "apiVersion": "0",
  "entry": "ui/index.html",
  "permissions": ["images.list", "ui.panel"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("board"), 0o644); err != nil {
		t.Fatal(err)
	}
	pictures := t.TempDir()
	picture := filepath.Join(pictures, "reference.png")
	writeTestPNG(t, picture)
	if err := app.indexFolders([]string{pictures}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()

	installed := pluginRequest(handler, "/api/app/plugins/installed")
	if installed.Code != http.StatusOK {
		t.Fatalf("installed plugins status=%d body=%s", installed.Code, installed.Body.String())
	}
	var payload struct {
		Plugins []struct {
			MediaToken string `json:"mediaToken"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(installed.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Plugins) != 1 || payload.Plugins[0].MediaToken == "" {
		t.Fatalf("installed plugin did not receive its media bearer: %#v", payload.Plugins)
	}

	id := stableImageID(picture)
	previousAccessToken := accessToken
	accessToken = "native-shell-secret"
	t.Cleanup(func() { accessToken = previousAccessToken })
	denied := pluginRequest(handler, "/plugin-media/"+id+"?token=wrong")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("plugin media accepted wrong token: status=%d", denied.Code)
	}
	media := pluginRequest(handler, "/plugin-media/"+id+"?size=320&token="+url.QueryEscape(payload.Plugins[0].MediaToken))
	if media.Code != http.StatusOK {
		t.Fatalf("plugin media status=%d body=%s", media.Code, media.Body.String())
	}
	if got := media.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Fatalf("sandbox cannot render authorized plugin media: CORP=%q", got)
	}
	ordinary := pluginRequest(handler, "/thumbnail/"+id+"?size=320")
	if got := ordinary.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Fatalf("ordinary thumbnails lost anti-probing policy: CORP=%q", got)
	}
}

func TestPluginSDKMapsImageCallsToCoreShape(t *testing.T) {
	host, err := embeddedFiles.ReadFile("web/plugin-host.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(host), `imageId: args.id, tag: args.tag`) {
		t.Fatal("images.tag no longer maps the public SDK fields to /api/app/tags")
	}
	sdk, err := embeddedFiles.ReadFile("web/plugin-sdk.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sdk), `(await call("images.read", { id })).image`) {
		t.Fatal("images.read no longer unwraps the core response")
	}
	if !strings.Contains(string(sdk), `(Array.isArray(tags) ? tags : [tags]).map`) {
		t.Fatal("images.tag no longer accepts the documented tag list")
	}
}
