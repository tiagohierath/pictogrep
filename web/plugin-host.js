// The host-side half of the plugin broker. Runs in the main Pictogrep page,
// which is the only place that ever holds the pictogrep_token cookie; a
// plugin's iframe has sandbox="allow-scripts" with no allow-same-origin, so
// its document has an opaque origin and cannot read that cookie or call the
// API directly. This is the only door between a plugin and the app, and every
// call through it is checked against the manifest's declared permissions
// before anything real happens.
//
// See web/plugin-sdk.js for the plugin-side half of the same channel.

(function () {
  function imageListPath(args) {
    const query = new URLSearchParams();
    for (const name of ["mode", "count", "offset", "seed", "tag", "source"]) {
      if (args[name] !== undefined && args[name] !== null && args[name] !== "") query.set(name, String(args[name]));
    }
    const suffix = query.toString();
    return "/api/app/images" + (suffix ? "?" + suffix : "");
  }

  const CAPABILITY_ROUTES = {
    "images.list": { method: "GET", path: imageListPath },
    "images.search": { method: "GET", path: (args) => "/api/app/search?q=" + encodeURIComponent(args.query || "") },
    "images.read": { method: "GET", path: (args) => "/api/app/images/" + encodeURIComponent(args.id) },
    "images.tag": { method: "POST", path: () => "/api/app/tags", body: (args) => args },
    "storage.kv.get": { method: "GET", path: (_args, id) => "/api/plugins/" + id + "/storage" },
    "storage.kv.set": { method: "POST", path: (_args, id) => "/api/plugins/" + id + "/storage", body: (args) => args },
  };

  function permissionFor(method) {
    if (method.startsWith("storage.kv")) return "storage.kv";
    return method;
  }

  async function handle(id, permissions, method, args) {
    const capability = CAPABILITY_ROUTES[method];
    if (!capability) throw new Error("unknown capability: " + method);
    if (!permissions.includes(permissionFor(method))) {
      throw new Error("plugin " + id + " did not declare " + permissionFor(method));
    }
    const init = { method: capability.method, credentials: "same-origin" };
    if (capability.body) {
      init.headers = { "Content-Type": "application/json" };
      init.body = JSON.stringify(capability.body(args));
    }
    const response = await fetch(capability.path(args, id), init);
    return response.json();
  }

  // One listener per mounted plugin iframe, scoped to that iframe's
  // contentWindow so a reply never crosses to the wrong plugin's promise
  // table, and a manifest fetched once at mount time so permission checks
  // don't trust anything the iframe claims about itself at call time.
  window.mountPlugin = function mountPlugin(iframe, manifest) {
    window.addEventListener("message", async (event) => {
      if (event.source !== iframe.contentWindow) return;
      const { callId, method, args } = event.data || {};
      if (!callId || !method) return;
      try {
        const result = await handle(manifest.id, manifest.permissions || [], method, args || {});
        iframe.contentWindow.postMessage({ callId, ok: true, result }, "*");
      } catch (error) {
        iframe.contentWindow.postMessage({ callId, ok: false, error: String(error.message || error) }, "*");
      }
    });
  };
})();
