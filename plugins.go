package main

// The plugin runtime. A plugin is a directory under pluginsDir with a
// plugin.json manifest and a web/ui entry point; nothing about it is compiled
// into this binary. See docs/plugins.md for why this exists and what it
// deliberately does not do yet (a marketplace, dependency resolution,
// third-party signing).
//
// v1 is frontend-only: a plugin has no backend process. Its UI runs inside a
// sandboxed iframe with no access to this app's cookie or token, and reaches
// the API only through the postMessage broker in web/plugin-host.js, which
// enforces the manifest's declared permissions before calling through.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type pluginManifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	APIVersion  string   `json:"apiVersion"`
	Entry       string   `json:"entry"`
	Permissions []string `json:"permissions"`
	Panel       struct {
		Title string `json:"title"`
		Icon  string `json:"icon"`
	} `json:"panel"`

	// dir is where this manifest was read from, not part of the JSON: it is
	// what /plugin/{id}/{path...} resolves file requests against.
	dir string
}

// pluginCapabilities is the v1 allowlist. A plugin can ask for exactly these;
// anything else in its manifest is ignored rather than rejected, so an older
// Pictogrep can still load a plugin manifest written for a newer one.
var pluginCapabilities = map[string]bool{
	"images.list":   true,
	"images.search": true,
	"images.read":   true,
	"images.tag":    true,
	"storage.kv":    true,
	"ui.panel":      true,
}

// loadPlugins scans pluginsDir for one level of subdirectories, each expected
// to hold a plugin.json. A subdirectory with no manifest, or a manifest that
// fails to parse, is skipped rather than failing the scan: one broken plugin
// should not take the others down, and the app has no user to report a parse
// error to at startup.
func loadPlugins(pluginsDir string) map[string]pluginManifest {
	found := map[string]pluginManifest{}
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return found
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(pluginsDir, entry.Name())
		data, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
		if err != nil {
			continue
		}
		var manifest pluginManifest
		if json.Unmarshal(data, &manifest) != nil {
			continue
		}
		if manifest.ID == "" || manifest.Entry == "" {
			continue
		}
		manifest.dir = dir
		found[manifest.ID] = manifest
	}
	return found
}

func (a *application) plugins() map[string]pluginManifest {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.installedPlugins
}

func (a *application) reloadPlugins() {
	loaded := loadPlugins(a.pluginsDir)
	a.mu.Lock()
	a.installedPlugins = loaded
	a.mu.Unlock()
}

// pluginList is what the app state and the settings panel show: manifests in
// a stable order, since a map iterates in none.
func (a *application) pluginList() []pluginManifest {
	plugins := a.plugins()
	list := make([]pluginManifest, 0, len(plugins))
	for _, manifest := range plugins {
		list = append(list, manifest)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// servePluginFile answers GET /plugin/{id}/{path...}. The response carries a
// CSP with no connect-src and no default network access, because the iframe's
// opaque origin (see web/index.html's use of sandbox="allow-scripts" with no
// allow-same-origin) already stops it reading this app's cookies or calling
// its API directly; the CSP is what stops it reaching out to some other
// server entirely, which the sandbox attribute alone does not prevent.
func (s *server) servePlugin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rest := r.PathValue("path")
	plugins := s.app.plugins()
	manifest, found := plugins[id]
	if !found {
		sendError(w, http.StatusNotFound, fmt.Errorf("no plugin named %s is installed", id))
		return
	}
	if rest == "" {
		rest = manifest.Entry
	}
	// filepath.Clean collapses ../ segments before the join, and the prefix
	// check after cleaning is what actually stops a request for
	// ../../../../etc/passwd from resolving outside the plugin's own
	// directory rather than merely making it look tidy.
	target := filepath.Join(manifest.dir, filepath.Clean("/"+rest))
	if !strings.HasPrefix(target, manifest.dir+string(filepath.Separator)) && target != manifest.dir {
		sendError(w, http.StatusForbidden, fmt.Errorf("path escapes the plugin directory"))
		return
	}
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'none'; frame-ancestors 'self'")
	http.ServeFile(w, r, target)
}

func (s *server) pluginsInstalled(w http.ResponseWriter, _ *http.Request) {
	list := s.app.pluginList()
	response := make([]map[string]any, 0, len(list))
	for _, manifest := range list {
		response = append(response, map[string]any{
			"id": manifest.ID, "name": manifest.Name, "version": manifest.Version,
			"entry": manifest.Entry, "permissions": manifest.Permissions, "panel": manifest.Panel,
		})
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": response})
}
