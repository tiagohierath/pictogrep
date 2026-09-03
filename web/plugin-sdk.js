// The plugin-side half of the broker: wraps postMessage to the parent page in
// promises, so a plugin calls pictogrep.images.search("hands") instead of
// building the message itself. Every method here maps to exactly one entry in
// web/plugin-host.js's capability table; there is no method on this object
// that reaches anywhere the host does not explicitly allow.
//
// Loaded by a plugin's own HTML with a plain
// <script src="/assets/plugin-sdk.js"></script>;
// it has no dependency on anything else in this app.

window.pictogrep = (function () {
  let nextCallId = 1;
  const pending = new Map();

  window.addEventListener("message", (event) => {
    const { callId, ok, result, error } = event.data || {};
    if (!callId || !pending.has(callId)) return;
    const { resolve, reject } = pending.get(callId);
    pending.delete(callId);
    ok ? resolve(result) : reject(new Error(error));
  });

  function call(method, args) {
    return new Promise((resolve, reject) => {
      const callId = "pg" + nextCallId++;
      pending.set(callId, { resolve, reject });
      window.parent.postMessage({ callId, method, args }, "*");
    });
  }

  return {
    images: {
      // Options are deliberately the same small paging/scope vocabulary as
      // /api/app/images. A spatial plugin may need the whole library rather
      // than whichever first 120 records happen to fit the endpoint default.
      list: (options = {}) => call("images.list", options),
      search: (query) => call("images.search", { query }),
      read: async (id) => (await call("images.read", { id })).image,
      tag: (id, tags) => Promise.all(
        (Array.isArray(tags) ? tags : [tags]).map(tag => call("images.tag", { id, tag })),
      ),
      reveal: (id) => call("images.reveal", { id }),
    },
    storage: {
      get: async (key) => (await call("storage.kv.get", {})).value?.[key],
      set: (key, value) => call("storage.kv.set", { key, value }),
    },
    ui: {
      openExternal: (url) => call("ui.openExternal", { url }),
    },
  };
})();
