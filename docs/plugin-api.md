# Pictogrep plugin API

How to write a plugin. Everything below is what the code does today, not what
it will eventually do: `plugins.go`, `web/plugin-sdk.js`, `web/plugin-host.js`,
`plugin_storage.go`, and the routes in `server.go`.

For *why* the API is shaped this way, and what is deliberately missing, read
[`plugins.md`](plugins.md) instead. This file does not re-argue any of it.

There is one API version and it is `"0"`. It is not stable yet. The capability
list grows as real plugins need things (see plugins.md's Strategy section), so
pin your expectations to what you can read in the source, not to what feels
like it should exist.

## A plugin is a directory

    ~/.local/share/pictogrep/plugins/
      random-pick/
        plugin.json          required, at the top level of the directory
        ui/
          index.html         the entry, whatever the manifest points at
          main.js
          logo.png

The plugins directory is `plugins/` inside Pictogrep's data home:

    Linux/BSD    $XDG_DATA_HOME/pictogrep/plugins, else ~/.local/share/pictogrep/plugins
    macOS        ~/.local/share/pictogrep/plugins
    Windows      %LOCALAPPDATA%\Pictogrep\plugins
    any          $PICTOGREP_HOME/plugins, when that variable is set

Scanning is one level deep. Each direct subdirectory is expected to hold a
`plugin.json`; anything else in there is ignored. Nesting a plugin two levels
down does not work.

A subdirectory is skipped, silently and without failing the scan, when its
`plugin.json` is missing, is not valid JSON, has an empty `id`, `entry`, or
`version`, has `version` exactly `0.0.0`, has an `entry` that escapes the
plugin directory, or has an `entry` that is not an existing regular file. One
half-copied plugin never takes the others down with it, but it also never says
anything: if your plugin does not appear in the list, the cause is one of those
conditions and there is no log line to tell you which.

The directory is rescanned every time the Plugins page is opened, so replacing
a development copy in place is visible on the next open. Restarting Pictogrep
is not needed. Changing a file inside an already-open plugin panel is, since
the iframe is already loaded; close and reopen the panel.

## plugin.json

```json
{
  "id": "dev.example.randompick",
  "name": "Random pick",
  "version": "0.1.0",
  "apiVersion": "0",
  "entry": "ui/index.html",
  "permissions": ["images.list", "images.tag", "storage.kv", "ui.panel"],
  "panel": { "title": "Random pick", "icon": "randompick" }
}
```

**`id`** (required). Reverse-DNS, immutable, and the plugin's identity
everywhere: the URL its files are served from, the name its storage file gets,
and the handle the broker checks permissions against. Nothing validates its
shape, so the convention is the only thing keeping it sane. Two installed
directories claiming the same id is not an error and not a warning; the one
later in alphabetical directory order silently wins.

**`name`** (recommended). Display name. The panel is titled with this, falling
back to `id`.

**`version`** (required). Semver. **Must not be `0.0.0`.** That value is
reserved for source-tree scaffolds that have a manifest but nothing worth
running, and a plugin carrying it is skipped entirely rather than shown as an
installed plugin with a button that opens nothing.

**`apiVersion`** (convention). The API version the plugin was built against.
Parsed into the manifest and then not used: nothing compares it, nothing
refuses to load on a mismatch, and it is not even included in what the panel
receives. Write `"0"` anyway. It costs one line and it is the field a future
compatibility check will read.

**`entry`** (required). Path to the HTML the panel loads, relative to the
plugin directory, forward slashes. It is resolved inside the directory and
refused if it escapes. Relative URLs in that document resolve beside it, so
`entry: "ui/index.html"` makes `<script src="main.js">` load `ui/main.js`.

**`permissions`** (required in practice). List of capability names, see below.
Names outside the allowlist are **dropped silently** rather than rejected, so a
manifest written for a newer Pictogrep still loads on an older one; it just
quietly loses the capabilities that core does not know about. A plugin with no
permissions loads fine and can call nothing.

**`paid`** (optional boolean, defaults to false). Declares that the plugin is
behind the NavyLilyWorks unlock. This is the only thing that decides: there is
no list of paid plugin ids anywhere in core, so a paid plugin a stranger wrote
is gated by exactly the line that gates a first-party one. On an install with
no imported license, a `paid` plugin still appears in the list, but
`/plugin/{id}/...` answers **402 Payment Required** for every file including its
CSS, and its `storage.kv` route answers 402 as well. Leave the field out unless
you mean it.

**`panel`** (optional). `{ "title": ..., "icon": ... }`. Both are read from the
manifest and handed to the page, and the current UI uses neither: the panel
header shows `name` and draws no icon. Fill them in for the future, do not rely
on them now.

## Loading the SDK

```html
<script src="/assets/plugin-sdk.js"></script>
```

An absolute path, served by core, not something you copy into your plugin. It
defines one global, `window.pictogrep`, and has no other dependency. It is the
only script outside your own directory a plugin can load: `plugin-sdk.js` is
the one app asset served with a cross-origin resource policy loose enough for a
sandboxed frame to execute, and every other one keeps the default that blocks
it.

Every method returns a promise. A rejection is a plain `Error` whose message is
either core's own error string or one of the broker's:

    plugin dev.example.randompick did not declare images.tag
    unknown capability: images.delete
    ui.openExternal: only https:// URLs are allowed
    Pictogrep returned 404

There is no callback form, no event bus, and no way to be notified of anything
happening in the main app. A plugin asks; it is never told.

## The methods

### pictogrep.images.list(options)

Permission: `images.list`. Calls `GET /api/app/images`.

```js
const page = await pictogrep.images.list({ mode: "recent", count: 200, offset: 0 });
// { ok: true, images: [...], total: 4312, offset: 0, seed: 91827364501 }
```

Options, all optional. Anything `undefined`, `null`, or `""` is left off the
request; anything else is stringified and sent.

| Option | Meaning |
| --- | --- |
| `mode` | `"recent"` sorts newest file first. `"random"` shuffles. `"unsorted"` filters down to images in no folder, without reordering. Anything else, including omitting it, gives library order. |
| `count` | Page size. Default **120**, minimum 1, maximum **500**. A non-numeric value falls back to 120. |
| `offset` | Page start, clamped to `[0, total]`. |
| `seed` | Shuffle seed for `mode: "random"`. Must be `1 <= seed < 2^48`; anything else is replaced by a fresh random seed. |
| `tag` | Restrict to one folder, by its normalized name. An invalid name is a rejected promise, not an empty list. |
| `source` | Restrict to images under one filesystem path. |

The default page is 120 records and there is no way to raise the cap past 500,
so a plugin that wants the whole library pages for it. `total` is the count
before paging, which is what the loop terminates on:

```js
async function fetchAll() {
  const out = [];
  let offset = 0, total = Infinity;
  while (offset < total) {
    const page = await pictogrep.images.list({ count: 500, offset, mode: "recent" });
    if (!page.images.length) break;
    out.push(...page.images);
    offset += page.images.length;
    total = page.total;
  }
  return out;
}
```

**Paging in `random` mode requires the seed.** The shuffle is seeded per
request, so paging without passing back the `seed` from the first response
reshuffles the library under you and you get repeats and gaps. Read `seed` from
page one, send it on every page after that.

Records for files that have disappeared from disk since the last index are
dropped from the page, so a page can come back shorter than `count` without
being the last page. Page on `images.length`, as above, not on
`length < count`.

### pictogrep.images.search(query)

Permission: `images.search`. Calls `GET /api/app/search`.

```js
const found = await pictogrep.images.search("hands");
// { ok: true, images: [...], query: "hands", ai: false }
```

This is **filename matching, not semantic search**. The query is split on
whitespace and each word is checked as a substring of the image's path relative
to its source folder, lowercased, with `_`, `-`, `.` and the path separator
flattened to spaces. `score` on each record is the fraction of query words that
matched, and results are sorted by it. `ai` is always `false`: the AI search
path exists in core but the broker does not route to it, and no capability
grants it.

The broker sends only `q`. The endpoint's `limit`, `tag`, and `source`
parameters are not forwarded, so a search always returns **at most 80 records**
and always searches the whole library. Filter the rest yourself.

### pictogrep.images.read(id)

Permission: `images.read`. Calls `GET /api/app/images/{id}`.

```js
const image = await pictogrep.images.read(id);
// the image record itself, already unwrapped from { ok, image }
```

This is the one method that does not hand back the raw response body. An
unknown id rejects with `Pictogrep returned 404`.

Despite what the capability list in plugins.md suggests, `images.read` does not
reach thumbnails or `/api/app/related/{id}`. It fetches one record. Thumbnails
arrive as URLs on the records you already have; related images are not exposed.

### pictogrep.images.tag(id, tags)

Permission: `images.tag`. Calls `POST /api/app/tags` with
`{action: "add", imageId, tag}`.

```js
await pictogrep.images.tag(image.id, ["gesture", "keep"]);
// [ { ok: true, tag: "gesture", added: true },
//   { ok: true, tag: "keep",    added: false } ]
```

Accepts one tag or an array, and sends **one request per tag** in parallel,
resolving to the array of results. `added` is `false` when the image was
already in that folder. Because it is a `Promise.all`, one failing tag rejects
the whole call after the others may already have been written; there is no
rollback.

Tag names are normalized by core before anything is written, and the `tag`
field in the response is the name that was actually used:

- lowercased and trimmed
- `/` nests folders, one level per segment
- letters, digits, `-` and `_` survive; spaces become `-`; everything else is
  dropped
- each segment is trimmed of leading and trailing `-` and `_`, and an empty
  segment is an error
- longer than 80 characters is an error

**Tagging into a folder that does not exist creates it.** That is the only way
a plugin can make a folder, and it cannot make an empty one: the folder appears
as a side effect of the first image landing in it. Nothing here can untag,
rename, or delete.

### pictogrep.images.reveal(id)

Permission: `images.reveal`. Calls `POST /api/app/images/reveal`.

```js
await pictogrep.images.reveal(image.id);
// { ok: true, path: "/home/you/pictures/refs/hand-01.png" }
```

Opens the containing folder in the system file manager: Finder with the file
selected on macOS, Explorer with it selected on Windows, `xdg-open` on the
folder everywhere else. On a machine with no `xdg-open` it rejects with
`no file manager found on this system`.

The id is resolved through the library index, so this can only ever point at a
picture Pictogrep already knows about. There is no path argument and adding one
would defeat the point.

### pictogrep.storage.get(key) / pictogrep.storage.set(key, value)

Permission: `storage.kv`, one permission covering both directions. The SDK
methods are two broker calls, `storage.kv.get` and `storage.kv.set`, and both
map to the same manifest permission, so declaring `storage.kv` grants reads and
writes together. There is no read-only form.

```js
const settings = await pictogrep.storage.get("settings");   // value, or undefined
await pictogrep.storage.set("settings", { density: 4, sort: "name" });  // { ok: true }
```

The store is one JSON object per plugin, in
`$PICTOGREP_HOME/data/plugins/{id}.json`, written by core through the same
atomic write every other piece of app state uses. Values are anything
`JSON.stringify` accepts. A `set` merges one key into the object and rewrites
the whole file; the POST body is capped at **1 MiB**, so a single key's value
has to fit in that.

`get` fetches the entire store and returns one key out of it, so reading five
keys is five full reads of the same file. Keep related state under one key and
read it once, the way the Room view plugin saves its whole layout as a single
`{seed, spots}` value.

There is no delete, no key enumeration, and no way to reach another plugin's
store. A missing file, a corrupt file, and an empty store are all
indistinguishable: you get `undefined`.

**Do not persist image URLs here.** The token in them is regenerated every time
Pictogrep starts (see below); a saved URL is dead on the next launch. Save ids.

### pictogrep.ui.openExternal(url)

Permission: `ui.openExternal`. Not an API call at all.

```js
await pictogrep.ui.openExternal("https://lens.google.com/uploadbyurl?url=" + encodeURIComponent(u));
// { ok: true }
```

The plugin's iframe is sandboxed with `allow-scripts` and deliberately without
`allow-popups`, so it cannot open a tab itself. The host page opens it instead,
with `_blank` and `noopener,noreferrer`, after checking two things: the
manifest declares `ui.openExternal`, and the URL starts with `https://`.
Anything else, `http:`, `javascript:`, `file:`, `data:`, or an empty string,
rejects and `window.open` is never reached.

Nothing comes back from the opened page. This is a one-way door.

## The image record

Every image the API hands a plugin has this shape. Fields marked optional are
omitted when empty, so check before using them.

| Field | Type | Notes |
| --- | --- | --- |
| `id` | string | Stable, derived from the canonical file path. This is the handle for every other call. |
| `name` | string | Filename only. |
| `path` | string, optional | Absolute filesystem path. Readable as text; a plugin has no filesystem access, so it cannot open it. |
| `mtime` | number | Modification time, Unix seconds. |
| `url` | string | See below. |
| `thumbnailUrl` | string | See below. Added by the broker, not by core. |
| `tags` | string[], optional | Folder names this image is in. |
| `width`, `height` | number, optional | Pixel dimensions, missing when the file could not be decoded. |
| `score` | number, optional | Only on search results. |

### The image URL rule

**Use `url` and `thumbnailUrl` exactly as given. Never build an `/image/{id}`
or `/thumbnail/{id}` path yourself.** Those two routes carry a same-origin
resource policy on purpose, so that a random website cannot point an `<img>` at
your loopback port and learn what is in your library. A sandboxed plugin has an
opaque origin and is, as far as the browser is concerned, exactly that random
website. A hand-built `/image/` URL does not load.

What works instead is `/plugin-media/{id}`, carrying an unguessable bearer
token, which the broker splices into the records it returns:

    url           /plugin-media/{id}?token=...&original=1   the original file
    thumbnailUrl  /plugin-media/{id}?token=...&size=960     a JPEG preview

The preview fits inside 960x960, aspect preserved, and falls back to serving
the original file for anything that cannot be decoded into one.

That rewrite happens only on results of an `images.*` call that the broker
already allowed, so the token never travels on an unrelated call. The token is
a fresh UUID per app launch: URLs are good for this run of Pictogrep and no
longer. Cache ids, not URLs.

## Permissions

The full allowlist, from `pluginCapabilities` in `plugins.go`:

    images.list       page the library
    images.search     filename search, at most 80 results
    images.read       one image record by id
    images.tag        add an image to a folder, creating the folder if new
    images.reveal     open the containing folder in the OS file manager
    storage.kv        the plugin's own JSON store, read and write
    ui.panel          declarative, nothing checks it
    ui.openExternal   open an https:// URL in a real browser tab

How a grant actually happens today: **the manifest is the grant.** There is no
install prompt, no settings toggle, and no per-call consent. `loadPlugins`
reads `permissions`, drops every name not on the allowlist above, and the
filtered list is what the broker enforces for the whole session.

A call the manifest does not cover is a **rejected promise**, immediately, with
no network request made and nothing shown to the user. It is not a prompt and
it is not something a plugin can recover from at runtime. Declare what you need
up front; asking later is not a thing.

`ui.panel` is the one entry with no teeth: nothing in core or in the host reads
it, and a plugin without it still opens. Declare it anyway. Every real plugin
does, and it is the name a future check will look for.

## The sandbox

The panel is:

```html
<iframe sandbox="allow-scripts" src="/plugin/{id}/{entry}"></iframe>
```

`allow-scripts` without `allow-same-origin` is the whole security model. The
document gets an opaque origin. In practice:

- **No cookies, no token, no API.** The main page holds `pictogrep_token` and
  is fully trusted. Your frame is not it, cannot read from it, and cannot call
  the API directly. `postMessage` to the parent, which is what the SDK wraps,
  is the only channel out.
- **No `connect-src`.** Plugin files are served with
  `default-src 'none'; script-src 'self' 'unsafe-inline' {origin}; style-src
  'self' 'unsafe-inline' {origin}; img-src 'self' data: {origin};
  connect-src 'none'; frame-ancestors 'self'`, where `{origin}` is the
  loopback address Pictogrep is serving on. `fetch`, `XMLHttpRequest`,
  WebSocket, and EventSource are all dead in a plugin, to anywhere, including
  back to Pictogrep. A plugin cannot phone home, cannot check for its own
  updates, and cannot call a third-party service.
- **No CDN, no external fonts, no remote images.** Everything you load comes
  from your own directory or from `/assets/plugin-sdk.js`. Ship your
  dependencies. Note that `default-src 'none'` also covers what the list does
  not mention: fonts, media, workers, and nested frames have no source and are
  blocked, including from your own directory.
- **Inline `<script>` and `<style>` work.** `'unsafe-inline'` is there, so a
  whole plugin in one HTML file is a normal shape here.
- **`data:` images work.** Canvas `toDataURL()` output renders; anything not in
  the source list above does not.
- **No popups.** No `allow-popups`, no `allow-top-navigation`, no
  `allow-modals`. `ui.openExternal` exists because of the first one; there is
  no capability for the other two.
- **No shared CSS.** The app's custom properties live on a document you cannot
  read. Copy the values you want from `web/m3.css` and the scale from
  [`ui.md`](ui.md), and understand that the copy is a copy: when core's palette
  changes, yours does not follow.

Size-wise, an open plugin panel is the full window width and the full height
below the app header, on desktop and on a phone alike. That is a wide box on a
monitor and a narrow one on a phone, and the same document has to work in both.
Build for the narrow case first.

## A complete plugin

Two files. Copy this into
`~/.local/share/pictogrep/plugins/random-pick/`, open the Plugins page, and
press Open.

`plugin.json`:

```json
{
  "id": "dev.example.randompick",
  "name": "Random pick",
  "version": "0.1.0",
  "apiVersion": "0",
  "entry": "ui/index.html",
  "permissions": ["images.list", "images.tag", "storage.kv", "ui.panel"],
  "panel": { "title": "Random pick", "icon": "randompick" }
}
```

`ui/index.html`:

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Random pick</title>
<script src="/assets/plugin-sdk.js"></script>
<style>
  html, body { height: 100%; margin: 0; }
  body { display: flex; flex-direction: column; gap: 8px; padding: 16px;
         box-sizing: border-box; background: #16181d; color: #e8e9ee;
         font: 14px/1.4 sans-serif; }
  .row { display: flex; gap: 8px; align-items: center; }
  button { height: 36px; padding: 0 12px; border-radius: 8px; }
  small { color: #9a9cab; }
  img { flex: 1; min-height: 0; object-fit: contain; }
</style>
</head>
<body>
  <div class="row">
    <button id="next" type="button">Another</button>
    <button id="keep" type="button">Tag as keep</button>
    <small id="status"></small>
  </div>
  <img id="picture" alt="">
<script>
  const pictureEl = document.getElementById("picture");
  const statusEl = document.getElementById("status");
  let current = null;
  let kept = 0;

  // count:1 over a fresh shuffle is one random picture out of the whole
  // library, without paging any of the rest of it.
  async function showRandom() {
    const page = await pictogrep.images.list({ mode: "random", count: 1 });
    current = page.images[0] || null;
    if (!current) { statusEl.textContent = "The library is empty."; return; }
    // thumbnailUrl as handed over. A hand-built /thumbnail/ path would not
    // load from inside the sandbox.
    pictureEl.src = current.thumbnailUrl;
    statusEl.textContent = current.name + " (" + page.total + " in library)";
  }

  async function tagKeep() {
    if (!current) return;
    await pictogrep.images.tag(current.id, ["keep"]);
    kept += 1;
    await pictogrep.storage.set("kept", kept);
    statusEl.textContent = "Tagged. " + kept + " kept so far.";
  }

  function report(error) { statusEl.textContent = String(error.message || error); }

  document.getElementById("next").onclick = () => showRandom().catch(report);
  document.getElementById("keep").onclick = () => tagKeep().catch(report);

  (async () => {
    kept = (await pictogrep.storage.get("kept")) || 0;
    await showRandom();
  })().catch(report);
</script>
</body>
</html>
```

Drop `"images.tag"` from `permissions` and the Tag button starts reporting
`plugin dev.example.randompick did not declare images.tag`, which is the whole
permission model in one line.

## Developing against it

Unpacked directory in the plugins folder, reload the Plugins page, press Open.
That is the loop, and it is deliberately the same loop a packaged plugin lands
in: a `.pictogrep` package is a ZIP of exactly this directory.

Two things that will waste your time if you do not know them:

- A plugin whose `version` is `0.0.0` never appears. This is the single most
  common reason a freshly scaffolded plugin is invisible.
- The panel probes the entry file before loading the frame, so a missing or
  unreadable entry shows an error naming the file instead of a blank white
  panel. If you see the blank panel, the entry loaded and your own script threw.

A plugin has no way to report a failure to core: nothing it throws reaches the
main app, and there is no log capability. Catch your own rejections and put the
message somewhere in your own UI, the way the example above does. Otherwise
every permission mistake and every 404 looks like a panel that just sits
there.

## What is not available

No backend of any kind. No commands, no context menu entries, no command
palette entries. No folder creation beyond the side effect of tagging, no
untagging, no image deletion, no renaming, no writes to storyboards or the
canvas. No settings, no sync, no AI or embedding access, no filesystem, no
network, no cross-plugin calls, no plugin-to-plugin messaging, and no way to
observe what the user is doing in the rest of the app.

Every one of those is a deliberate omission with a reason, and the reasons are
in [`plugins.md`](plugins.md). The way to get one added is the way
`images.reveal` and `ui.openExternal` got added: write the plugin that cannot
exist without it, then propose the capability that plugin needed. Not the other
way around.
