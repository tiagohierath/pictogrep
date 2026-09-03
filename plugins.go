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
	"crypto/subtle"
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
	// Paid is how a plugin says it is behind the unlock, and it is the only
	// thing that decides: there is no list of paid plugin ids in this program.
	// A plugin a stranger wrote and sold declares this exactly the way ours
	// does, and is gated by exactly the same line (see pluginLocked in
	// license.go). Absent means free, which is the right default for the far
	// larger set.
	Paid  bool `json:"paid"`
	Panel struct {
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
	"images.list":     true,
	"images.search":   true,
	"images.read":     true,
	"images.tag":      true,
	"images.reveal":   true,
	"storage.kv":      true,
	"ui.panel":        true,
	"ui.openExternal": true,
}

// loadPlugins scans pluginsDir for one level of subdirectories, each expected
// to hold a plugin.json. A subdirectory with no valid manifest or entry file is
// skipped rather than failing the scan: one half-copied plugin should not take
// the others down or leave an Open button that can only render a 404 page.
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
		// 0.0.0 is the repository convention for a scaffold that has a manifest
		// but no usable release yet. Do not turn private design placeholders into
		// broken buttons in somebody's installed list.
		if manifest.ID == "" || manifest.Entry == "" || manifest.Version == "" || manifest.Version == "0.0.0" {
			continue
		}
		entryPath := filepath.Join(dir, filepath.Clean("/"+manifest.Entry))
		if !strings.HasPrefix(entryPath, dir+string(filepath.Separator)) {
			continue
		}
		info, err := os.Stat(entryPath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		permissions := manifest.Permissions[:0]
		for _, permission := range manifest.Permissions {
			if pluginCapabilities[permission] {
				permissions = append(permissions, permission)
			}
		}
		manifest.Permissions = permissions
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
	// A paid plugin with no license behind it serves nothing at all, not even
	// its own CSS: the panel it would draw is the product. 402 rather than 404
	// because the difference between "not installed" and "installed, not paid
	// for" is exactly what the person on the other end needs to be told.
	if s.app.pluginLocked(manifest) {
		sendError(w, http.StatusPaymentRequired, fmt.Errorf("%s needs a NavyLilyWorks unlock", id))
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
	// Name the local origin explicitly as well as with 'self'. Browsers give the
	// sandboxed document an opaque origin, and the explicit source leaves no
	// ambiguity about loading files from the URL that delivered the document.
	localOrigin := "http://" + r.Host
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(
		"default-src 'none'; script-src 'self' 'unsafe-inline' %s; style-src 'self' 'unsafe-inline' %s; img-src 'self' data: %s; connect-src 'none'; frame-ancestors 'self'",
		localOrigin, localOrigin, localOrigin))
	// securityHeaders denies framing and cross-origin subresource use by
	// default. A plugin is the one intentional exception: its document is
	// framed by this same app, then sandboxed into an opaque origin. SAMEORIGIN
	// still prevents any website from framing it, while cross-origin CORP lets
	// that opaque document load its own CSS and JavaScript files.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	http.ServeFile(w, r, target)
}

// servePluginMedia returns a library picture to a sandboxed plugin without
// opening the ordinary /image and /thumbnail routes to cross-origin embeds.
// The unguessable token reaches a plugin only in image records returned by the
// host broker after it has enforced an images.* manifest permission.
func (s *server) servePluginMedia(w http.ResponseWriter, r *http.Request) {
	if !validPluginMediaToken(r.URL.Query().Get("token"), s.pluginMediaToken) {
		sendError(w, http.StatusForbidden, fmt.Errorf("invalid plugin media token"))
		return
	}
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	r.SetPathValue("id", r.PathValue("image"))
	if r.URL.Query().Get("original") == "1" {
		s.image(w, r)
		return
	}
	s.thumbnail(w, r)
}

func validPluginMediaToken(provided, expected string) bool {
	return expected != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (s *server) pluginsInstalled(w http.ResponseWriter, _ *http.Request) {
	// Plugins are ordinary directories and are often installed by replacing a
	// development copy in place. Rescanning here makes that replacement visible
	// the next time the Plugins page opens instead of requiring an app restart.
	s.app.reloadPlugins()
	list := s.app.pluginList()
	response := make([]map[string]any, 0, len(list))
	for _, manifest := range list {
		// A locked plugin stays in the list rather than vanishing from it. It is
		// installed, it is on disk, and hiding it would leave a person who paid
		// on one machine and not another with no explanation for where their
		// plugin went.
		response = append(response, map[string]any{
			"id": manifest.ID, "name": manifest.Name, "version": manifest.Version,
			"entry": manifest.Entry, "permissions": manifest.Permissions, "panel": manifest.Panel,
			"paid": manifest.Paid, "locked": s.app.pluginLocked(manifest),
			"mediaToken": s.pluginMediaToken,
		})
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": response})
}
