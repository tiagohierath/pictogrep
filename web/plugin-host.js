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
  const mounts = new WeakMap();

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
    "images.tag": {
      method: "POST",
      path: () => "/api/app/tags",
      body: (args) => ({action: "add", imageId: args.id, tag: args.tag}),
    },
    "images.reveal": {
      method: "POST",
      path: () => "/api/app/images/reveal",
      body: (args) => ({imageId: args.id}),
    },
    "storage.kv.get": { method: "GET", path: (_args, id) => "/api/plugins/" + id + "/storage" },
    "storage.kv.set": { method: "POST", path: (_args, id) => "/api/plugins/" + id + "/storage", body: (args) => args },
  };

  function permissionFor(method) {
    if (method.startsWith("storage.kv")) return "storage.kv";
    return method;
  }

  // ui.openExternal isn't an API call: it opens a real browser tab from this
  // page, which the plugin's sandboxed iframe (allow-scripts, no
  // allow-popups) cannot do itself. Restricted to https:// so a plugin can't
  // hand back a javascript:/file:/data: URL and have the host page open it
  // with page-level privileges.
  function openExternal(id, permissions, args) {
    if (!permissions.includes("ui.openExternal")) {
      throw new Error("plugin " + id + " did not declare ui.openExternal");
    }
    const url = String(args?.url || "");
    if (!/^https:\/\//i.test(url)) throw new Error("ui.openExternal: only https:// URLs are allowed");
    window.open(url, "_blank", "noopener,noreferrer");
    return { ok: true };
  }

  async function handle(id, permissions, method, args) {
    if (method === "ui.openExternal") return openExternal(id, permissions, args);
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
    const result = await response.json();
    if (!response.ok || result?.ok === false) {
      throw new Error(result?.error || `Pictogrep returned ${response.status}`);
    }
    return result;
  }

  // Library media cannot use the normal /image URL from an opaque sandboxed
  // origin: that route intentionally carries same-origin CORP so websites
  // cannot probe a local library. Give the plugin a short-lived bearer URL
  // instead. Only image records returned through an allowed images.* call are
  // rewritten, so the token never crosses the broker on an unrelated call.
  function withPluginMedia(result, mediaToken) {
    if (!mediaToken || !result) return result;
    const rewrite = image => {
      if (!image?.id) return image;
      const base = `/plugin-media/${encodeURIComponent(image.id)}?token=${encodeURIComponent(mediaToken)}`;
      return {...image, url: `${base}&original=1`, thumbnailUrl: `${base}&size=960`};
    };
    if (Array.isArray(result.images)) return {...result, images: result.images.map(rewrite)};
    if (result.image) return {...result, image: rewrite(result.image)};
    return result;
  }

  // One listener per mounted plugin iframe, scoped to that iframe's
  // contentWindow so a reply never crosses to the wrong plugin's promise
  // table, and a manifest fetched once at mount time so permission checks
  // don't trust anything the iframe claims about itself at call time.
  window.mountPlugin = function mountPlugin(iframe, manifest) {
    const previous = mounts.get(iframe);
    if (previous) window.removeEventListener("message", previous);
    const listener = async (event) => {
      if (event.source !== iframe.contentWindow) return;
      const { callId, method, args } = event.data || {};
      if (!callId || !method) return;
      try {
        let result = await handle(manifest.id, manifest.permissions || [], method, args || {});
        if (method.startsWith("images.")) result = withPluginMedia(result, manifest.mediaToken);
        iframe.contentWindow.postMessage({ callId, ok: true, result }, "*");
      } catch (error) {
        iframe.contentWindow.postMessage({ callId, ok: false, error: String(error.message || error) }, "*");
      }
    };
    mounts.set(iframe, listener);
    window.addEventListener("message", listener);
  };

  window.unmountPlugin = function unmountPlugin(iframe) {
    const listener = mounts.get(iframe);
    if (!listener) return;
    window.removeEventListener("message", listener);
    mounts.delete(iframe);
  };
})();
