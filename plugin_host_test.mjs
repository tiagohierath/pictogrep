// Standalone executable check of web/plugin-host.js's capability logic for
// the two capabilities added for the Find-me plugin: images.reveal
// (permission gating + route wiring) and ui.openExternal (permission gating
// + https:// scheme check + window.open call).
//
// This repo has no JS test framework and no package.json (checked before
// writing this). `node` is not on PATH in the sandbox this was written in,
// but it does run there via `nix shell nixpkgs#nodejs -c node
// plugin_host_test.mjs` (all 12 checks passed as of writing). Wherever plain
// Node is on PATH, run it directly:
//
//   node plugin_host_test.mjs
//
// It prints one PASS/FAIL line per check and exits non-zero on any failure.
// It is not wired into `go test` or any CI: nothing in this repo currently
// runs it automatically, so it only protects against a regression if someone
// remembers to run it by hand (or it gets wired into a script later).
//
// plugin-host.js is an IIFE with no exports that reaches for the ambient
// `window` and `fetch`, so this script loads the real file into a Node vm
// context with minimal window/fetch shims and drives it the same way
// index.html does (mountPlugin, then a "message" event shaped like the
// plugin-sdk.js side sends), instead of re-implementing its logic by hand.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";

const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(join(here, "web", "plugin-host.js"), "utf8");

let failures = 0;
function check(condition, message) {
  if (condition) {
    console.log("PASS: " + message);
  } else {
    console.log("FAIL: " + message);
    failures++;
  }
}

// Loads a fresh copy of plugin-host.js into its own sandbox so each test
// gets an isolated `mounts` WeakMap. Returns the sandbox's `window` (with
// mountPlugin/unmountPlugin attached, matching the real global) plus the
// list of registered "message" listeners, which the tests below invoke
// directly to simulate postMessage delivery (jsdom-free Node has no real
// postMessage/MessageEvent plumbing to drive instead).
function loadPluginHost({ fetchImpl, openImpl }) {
  const listeners = [];
  const window = {
    addEventListener(type, listener) {
      if (type === "message") listeners.push(listener);
    },
    removeEventListener(type, listener) {
      if (type !== "message") return;
      const index = listeners.indexOf(listener);
      if (index !== -1) listeners.splice(index, 1);
    },
    open: openImpl,
  };
  const sandbox = {
    window,
    fetch: fetchImpl,
    console,
    URLSearchParams,
    WeakMap,
    Map,
    Array,
    Boolean,
    String,
    Error,
    encodeURIComponent,
  };
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox, { filename: "web/plugin-host.js" });
  return { window, listeners };
}

async function deliver(listeners, fromWindow, data) {
  for (const listener of listeners.slice()) {
    await listener({ source: fromWindow, data });
  }
}

function fakeIframe() {
  const replies = [];
  const contentWindow = { postMessage: (msg) => replies.push(msg) };
  return { iframe: { contentWindow }, contentWindow, replies };
}

// --- images.reveal: refused without the permission ---
await (async () => {
  const fetchCalls = [];
  const { window, listeners } = loadPluginHost({
    fetchImpl: async (path, init) => {
      fetchCalls.push({ path, init });
      return { ok: true, json: async () => ({ ok: true, path: "/pictures/a.png" }) };
    },
    openImpl: () => {},
  });
  const { iframe, contentWindow, replies } = fakeIframe();
  window.mountPlugin(iframe, { id: "dev.pictogrep.findme", permissions: [] });
  await deliver(listeners, contentWindow, { callId: "c1", method: "images.reveal", args: { id: "img1" } });

  check(replies.length === 1 && replies[0].ok === false, "images.reveal without permission replies ok:false");
  check(fetchCalls.length === 0, "images.reveal without permission never calls fetch");
})();

// --- images.reveal: calls the real route with the real body once granted ---
await (async () => {
  const fetchCalls = [];
  const { window, listeners } = loadPluginHost({
    fetchImpl: async (path, init) => {
      fetchCalls.push({ path, init });
      return { ok: true, json: async () => ({ ok: true, path: "/pictures/a.png" }) };
    },
    openImpl: () => {},
  });
  const { iframe, contentWindow, replies } = fakeIframe();
  window.mountPlugin(iframe, { id: "dev.pictogrep.findme", permissions: ["images.reveal"] });
  await deliver(listeners, contentWindow, { callId: "c2", method: "images.reveal", args: { id: "img1" } });

  check(replies.length === 1 && replies[0].ok === true, "images.reveal with permission succeeds");
  check(fetchCalls.length === 1 && fetchCalls[0].path === "/api/app/images/reveal", "images.reveal posts to /api/app/images/reveal");
  check(fetchCalls[0]?.init?.method === "POST", "images.reveal uses POST");
  check(
    fetchCalls[0]?.init?.body === JSON.stringify({ imageId: "img1" }),
    "images.reveal sends {imageId} matching what server.go's revealImage decodes",
  );
})();

// --- ui.openExternal: refused without the permission, window.open never called ---
await (async () => {
  const opened = [];
  const { window, listeners } = loadPluginHost({
    fetchImpl: async () => ({ ok: true, json: async () => ({ ok: true }) }),
    openImpl: (...args) => opened.push(args),
  });
  const { iframe, contentWindow, replies } = fakeIframe();
  window.mountPlugin(iframe, { id: "dev.pictogrep.findme", permissions: [] });
  await deliver(listeners, contentWindow, {
    callId: "c3",
    method: "ui.openExternal",
    args: { url: "https://example.com" },
  });

  check(replies.length === 1 && replies[0].ok === false, "ui.openExternal without permission replies ok:false");
  check(opened.length === 0, "ui.openExternal without permission never calls window.open");
})();

// --- ui.openExternal: refused for a non-https URL even with the permission ---
await (async () => {
  const opened = [];
  const { window, listeners } = loadPluginHost({
    fetchImpl: async () => ({ ok: true, json: async () => ({ ok: true }) }),
    openImpl: (...args) => opened.push(args),
  });
  const { iframe, contentWindow, replies } = fakeIframe();
  window.mountPlugin(iframe, { id: "dev.pictogrep.findme", permissions: ["ui.openExternal"] });
  for (const url of ["javascript:alert(1)", "file:///etc/passwd", "http://example.com", ""]) {
    await deliver(listeners, contentWindow, { callId: "u-" + url, method: "ui.openExternal", args: { url } });
  }

  check(replies.every((r) => r.ok === false), "ui.openExternal rejects every non-https:// URL: " + JSON.stringify(replies.map((r) => r.error)));
  check(opened.length === 0, "ui.openExternal never calls window.open for a non-https:// URL");
})();

// --- ui.openExternal: succeeds for an https URL with the permission ---
await (async () => {
  const opened = [];
  const { window, listeners } = loadPluginHost({
    fetchImpl: async () => ({ ok: true, json: async () => ({ ok: true }) }),
    openImpl: (...args) => opened.push(args),
  });
  const { iframe, contentWindow, replies } = fakeIframe();
  window.mountPlugin(iframe, { id: "dev.pictogrep.findme", permissions: ["ui.openExternal"] });
  await deliver(listeners, contentWindow, {
    callId: "c4",
    method: "ui.openExternal",
    args: { url: "https://example.com/path" },
  });

  check(replies.length === 1 && replies[0].ok === true && replies[0].result?.ok === true, "ui.openExternal with permission and https:// succeeds");
  check(
    opened.length === 1 && opened[0][0] === "https://example.com/path" && opened[0][1] === "_blank" && opened[0][2] === "noopener,noreferrer",
    "ui.openExternal opens the given URL with target _blank and noopener,noreferrer",
  );
})();

if (failures > 0) {
  console.log(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log("\nok: all checks passed");
