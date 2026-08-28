// The plugin-side half of the broker: wraps postMessage to the parent page in
// promises, so a plugin calls pictogrep.images.search("hands") instead of
// building the message itself. Every method here maps to exactly one entry in
// web/plugin-host.js's capability table; there is no method on this object
// that reaches anywhere the host does not explicitly allow.
//
// Loaded by a plugin's own HTML with a plain <script src="/plugin-sdk.js">;
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
      list: () => call("images.list", {}),
      search: (query) => call("images.search", { query }),
      read: (id) => call("images.read", { id }),
      tag: (id, tags) => call("images.tag", { id, tags }),
    },
    storage: {
      get: async (key) => (await call("storage.kv.get", {})).value?.[key],
      set: (key, value) => call("storage.kv.set", { key, value }),
    },
  };
})();
