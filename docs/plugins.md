# Plugin architecture

Design notes for third-party and paid plugins. Nothing here is built yet.

## Strategy

**Build this for our own plugins first, not for an ecosystem.** A permission
system, a marketplace, a dependency resolver, and a developer-accounts flow are
all things a theoretical plugin architecture grows before it has ever been
used. None of that gets built now.

The rule instead: **extract the API from real plugins, don't design it up
front.**

1. Build one real plugin.
2. Notice everything it needed from core that the API didn't have.
3. Turn those needs into clean public capabilities.
4. Build plugin #2 against the growing API.
5. Fix whatever abstraction #2 exposed as wrong.
6. Repeat. Only after roughly five plugins does the API get called stable.

Until then the capability list in this doc is expected to grow and occasionally
get reshaped. That's the process working, not scope creep.

What that implies for the parts below:

- **Core stays dumb and stable.** Plugins talk to a small, versioned API and
  never touch core internals, storage, or each other directly.
- **Web-tech plugins first.** HTML/CSS/JS UI calling Pictogrep over a local
  API. No Go native plugins (`plugin` package): ABI/version/platform pain,
  worst on Windows and mobile, for no benefit over an HTTP boundary.
- **A backend is optional and per-plugin**, added only when a plugin actually
  needs one, as a separate process talking JSON/RPC. Desktop-only, per the
  Android constraint below; WASM is the cross-platform answer, and it waits
  until a plugin genuinely needs sandboxed backend logic, not before.
- **Official plugins use the exact same API as third-party ones.** No hook
  reserved for our own code.
- **Signed packages, eventually.** For trust once third parties publish, not
  as a DRM mechanism, which this project isn't building anyway. See below.
- **Not built yet, on purpose:** marketplace, dependency resolver,
  plugin-to-plugin APIs, a permissions UI beyond a plain list, developer
  accounts.

## What already exists

The word "plugin" is already taken. Today it means one of eight compile-time
features that a config flag can switch off:

    wikimedia, calendar, sidebar, vim, commandPalette, pinterest, web, canvas

Two of those are misfiled. **Importing from Pinterest and from websites is a
core feature, not a plugin**, and importing is most of why an empty library
becomes useful at all. `pinterest` and `web` come out of the plugin registry and
become ordinary core functionality on both platforms, which means removing them
from the `setPluginEnabled` allowlist (`app.go:406`), from the `defaultEnabled`
pair in `pluginEnabled`, and from `freeOnPhone` in `premium.go`, where `web` is
currently listed as a phone freebie. Their settings rows stay; they simply stop
being switchable as plugins.

That leaves six real plugins. **Only two of those six are paid: `canvas`
("Folder canvas") and storyboarding**, plus one new plugin that doesn't exist
in any form today, **Darkroom** (non-destructive image effects: grayscale,
flip, rotate, invert, posterize, threshold, blur/sharpen, contrast/brightness,
crop/resize, dominant-palette extraction, reduce-to-N-colors, contact sheet,
pixelate). Everything else that exists today (Wikimedia, calendar, sidebar,
vim, command palette) stays free, on both platforms, forever — the paid set is
exactly these three, not "plugins in general."

Darkroom's non-destructive rule mirrors the one this user already holds
milklily to: source stays read-only, an effect is an instruction applied to
produce an output, never a mutation of the input. Every Darkroom operation
reads a picture and produces a derived preview or file; none of them are
allowed to overwrite what's already in the library.

Darkroom is also the first plugin to expose a real gap in the v1 capability
list: nothing in it lets a plugin save a result anywhere Pictogrep will
remember. `storage.kv` is plugin-scoped and wrong for this; a picture belongs
in the library, not in a plugin's private JSON blob. This needs a new
capability, something like `images.saveDerived(sourceID, blob) -> newID`, that
writes a new library entry and never touches the source path. Building
Darkroom for real is what should decide that capability's exact shape, per the
Strategy section above: don't design it now, let the plugin that needs it
shape it.

Storyboarding is a wrinkle: it isn't in the plugin registry at all today. It's
a plain, always-on route (`GET /practice`, `server.go:106`) serving a 2000-line
standalone page (`web/practice.html`), linked directly from the main menu as
"New storyboard." Making it paid means pulling it out of core and into the
plugin system for the first time, not just re-flagging an existing plugin.
It's also currently free and unrestricted for every existing desktop install,
and this project's own stated principle (`premium.go`'s comments) is not to
take a working feature away from an install that already has it. That
principle is being knowingly overridden here, at the user's explicit
direction: storyboarding goes paid for existing installs too, no
grandfathering. Source lives in the private
[`pictogrep-plugins-paid`](https://github.com/tiagohierath/pictogrep-plugins-paid)
repo; `web/practice.html` stays in core only until the extraction happens, at
which point it's deleted from here, not duplicated.

The canvas plugin is the closer fit to the existing registry (it's already an
off-by-default toggle named `canvas`, backend in `canvas.go`), but its UI isn't
a standalone file: the frontend is woven into `web/app.js` across roughly two
dozen call sites rather than living in one place, so its extraction is messier
than storyboarding's despite starting from a better registry position. See
that repo's `canvas/README.md` for the extraction plan.

The allowlist is a literal string comparison in `setPluginEnabled`
(`app.go:406`), the state is a `map[string]bool` under `"plugins"` in the config
file, and each one is gated in three separate places: its routes in
`registerRoutes` (`server.go:105`), its panel section in `web/index.html`, and
its background work. `premium.go` layers a phone-only unlock on top through
`premiumLocks`, called from `pluginEnabled` so a locked plugin is off for its
panel, its routes, and its jobs at once.

That system is fine and should not be touched. It is a feature-flag system, not
an extension system, and it cannot grow into one: every name in it is a name
that exists in the Go binary. What follows is a second, additive mechanism for
code that does not ship in the binary.

To keep the two apart in the config file, installed plugins live under a new
`"installedPlugins"` key rather than joining the existing `"plugins"` bool map.
In the interface they can both be called plugins; the user does not need to
know which mechanism draws which panel.

## Constraints that decide the design

**There is no database.** The doc that prompted this assumed SQLite. The actual
store is JSON files: `data/metadata.json`, `data/index-state.json`,
`storyboards/*.json`, plus loose files in `library/` and
`data/optimized-images/`. This makes the "never let plugins touch storage
directly" rule more urgent, not less. A plugin that learns the shape of
`metadata.json` freezes that shape forever, and a plugin that writes it
non-atomically corrupts the library.

**The page is already fully trusted.** `PICTOGREP_TOKEN` is a per-launch secret
handed to the WebView and accepted as a cookie (`access_token.go`), because
thumbnails are `<img src>` and cannot carry a header. Anything running in the
main page inherits it. Plugin JS loaded with a `<script>` tag into `index.html`
would therefore have the entire API, and every permission in the manifest would
be decorative. This single fact determines the whole v1 shape.

**Android cannot run plugin executables.** The core already ships as an exec'd
`libpictogrep.so` under a WebView with PT_INTERP constraints. Dropping an
arbitrary binary into an app-writable directory and executing it is blocked by
W^X on current Android. Any plugin backend that is a subprocess is desktop-only
by construction.

**Google Play takes digital goods sold in-app.** `premium.go` already says this
out loud: the unlock button is a placeholder for Play Billing.

The QR path sidesteps this cleanly: a NavyLilyWorks entitlement is bought on the
web and the phone only reads a signed file, which is not an in-app purchase.

The **R$30 mobile purchase does not sidestep it**. Sold inside the Android app it
is a digital good and Play takes its cut, which on R$30 is roughly R$4.50 to R$9
depending on the rate tier. Sold on the web instead, Play's rules restrict
steering users to it from inside the app, and how much has been in flux across
jurisdictions since 2024. This is the one open commercial question in the model
and it does not block anything technical: the license format, the verification,
and the QR import are identical either way. Only the checkout button differs.

Worth noting the R$30 mobile price sits below the R$50/year desktop entitlement,
so a subscriber has no reason to buy it and a phone-only user has no reason to
subscribe. That is coherent, not a leak.

## v1: frontend plugins only

A plugin is a directory. It has a manifest and web assets, and that is all.

    ~/.local/share/pictogrep/plugins/
      challenge-prompts/
        plugin.json
        index.html
        main.js
        assets/

    plugin.json
      id            reverse-dns, immutable, e.g. works.navylily.challenges
      name          display name
      version       semver
      apiVersion    plugin API version this was built against
      entry         path to the HTML the panel loads
      permissions   list of capability names, see below
      panel         { title, icon }

`version` must name an actual plugin build. `0.0.0` is reserved for source-tree
scaffolds and is not shown as an installed plugin.

No backend entry point in v1. No commands, no context menu actions, no
dependency resolution. Those are all additive later and none of them can be
removed once shipped.

### How the UI attaches

The core gains one new panel type: a plugin panel, drawn as a `<section>`
alongside `imagesPanel` and the rest, containing one iframe.

    <iframe sandbox="allow-scripts" src="/plugin/{id}/{entry}">

`sandbox="allow-scripts"` without `allow-same-origin` is the entire security
model. The iframe gets an opaque origin, so it cannot read the
`pictogrep_token` cookie, cannot fetch the API, and cannot reach into the parent
document. Its only channel out is `postMessage`.

The core serves plugin files from a new `GET /plugin/{id}/{path...}` route that
resolves inside the plugin's own directory and refuses to escape it. Those
responses need `Content-Security-Policy` with no `connect-src`, so a plugin
cannot phone home either. Note the existing COEP header choice in `server.go:263`
(`credentialless` for the Commons plugin) when adding to the header set.

### How the API attaches

A small host-side broker in the parent page listens for messages from the
iframe, checks the calling plugin's granted permissions, calls the real API with
the real token, and posts the result back. Plugins never see the token.

The plugin side ships as an OSS SDK, a single file that wraps postMessage in
promises:

    const { images } = await pictogrep.images.search("hands");
    await pictogrep.images.tag(images[0].id, ["gesture"]);
    await pictogrep.storage.set("streak", 7);

Every method maps to exactly one capability. If a capability is not granted, the
broker rejects rather than asking the user mid-call; permissions are granted
once at install time, shown as a plain list.

### The v1 capability set

Deliberately small. Adding is cheap, removing is not.

    images.list       GET /api/app/images
    images.search     GET /api/app/search
    images.read       GET /api/app/images/{id}, thumbnails, related
    images.tag        POST /api/app/tags
    storage.kv        plugin-scoped JSON file, core owns the atomic write
    ui.panel          implied by having a panel at all

Image records returned through the SDK carry `url` and `thumbnailUrl` values
that are authorized for the sandboxed frame. Plugins must use those values
rather than constructing `/image/` or `/thumbnail/` paths themselves; the
ordinary paths deliberately remain same-origin-only.

Not in v1, on purpose: folder creation, image deletion, storyboard writes,
canvas writes, settings, sync, AI queries, network access. Each one gets added
when a real plugin cannot be written without it, and each addition is a public
API change that ships in the OSS core for everyone.

`storage.kv` writes go through the core so plugins inherit `writeFileAtomically`
rather than inventing their own half-written files.

## Distribution

A plugin package is a ZIP of the plugin directory, named
`{id}-{version}.pictogrep`. The manifest inside carries the id and version; the
filename is a convenience.

Install is: download, verify SHA-256 against the value in the store index,
unzip into the plugins directory, show the permission list, enable. The core
rescans that directory whenever the Plugins page opens, so a new or replaced
plugin does not require restarting Pictogrep.

**No signing in v1.** Signing buys provenance, which matters when third parties
publish plugins and users install bundles that did not come from you. For
first-party plugins downloaded over TLS from your own server it buys nothing,
and it costs a private key that must never be lost. There is already one
irreplaceable signing key in this project's life (the Android upload key); a
second one is a real liability. Add signing at the same time as the third-party
directory, not before.

## Licensing

Two ways in, one unlock. There is no per-plugin SKU.

    Desktop / web    NavyLilyWorks, R$50/year, sold at navylily.tv
                     -> unlocks every plugin

    Mobile           R$30 once
                     or import an active NavyLilyWorks entitlement by QR

Both produce the same thing: a signed license file.

    obtain entitlement
      -> receive signed license file
      -> import on any device
      -> verified offline
      -> plugins unlock, permanently

### Access is never taken away

**Cancelling the navylily.tv subscription does not remove a single plugin.**
Neither does letting it expire, neither does a card failing, neither does the
subscription ending for any other reason. Once the plugins are unlocked on a
machine they stay unlocked on that machine forever, with no further payment of
any kind.

This is the perpetual-fallback model, the same shape JetBrains uses: a year of
NavyLilyWorks permanently grants the plugins that existed during that year, and
the subscription ending stops new ones from arriving rather than removing old
ones.

**Note this splits from how the rest of navylily.tv works.** That subscription
also gates the drawing course at `/protected/`, and cancelling does end course
access, because the course is a service that keeps publishing. The plugins are
not: they are software already sitting on the buyer's disk. So one cancellation
produces two different outcomes on purpose, and the copy has to say so plainly
or it reads as a bug. Something like: *your lessons stop, your plugins are
yours.*

Practically, the license file is issued once when the subscription first grants
it and is never reissued, re-checked, or expired. Cancellation is an event on
the billing side that the Pictogrep install never hears about and has no way to
act on even if it did.

This is the decision that makes zero DRM coherent instead of merely lax. Once a
license is never revoked, the license file has no expiry to enforce, so there is
nothing to check on a schedule, nothing to phone home about, and no clock to
sync. The file is a permanent grant. Verification is one signature check at
import time, and after that the answer is stored and never revisited.

It also removes the failure mode that makes offline licensing hostile: there is
no state in which a paying customer opens Pictogrep on a plane and is told their
subscription could not be confirmed.

### The license file

A small signed document: a buyer reference, an issue date, and a tier. Signed
Ed25519; Pictogrep ships the public key and verifies locally. No expiry field is
enforced, per above. Nothing phones home, ever, on any platform.

**Moving it to a phone is a QR code.** The desktop shows the license as a QR
code and the phone's camera reads it, reusing the pairing flow already in
`sync_qr.go` and `sync_pairing.go`. This is also the path for a subscriber who
never pays the mobile R$30: an active NavyLilyWorks entitlement scanned onto a
phone is a permanent mobile unlock.

A license is a file the buyer owns. It can be copied to their second machine,
which is intended, and to a stranger, which is accepted. The signature proves
the license was issued, not who holds it. Zero DRM means exactly that: no device
binding, no activation count, no revocation list, no obfuscation. Anyone who
wants to bypass it can recompile the open-source core, and that is fine.

### Desktop reuses payment infrastructure that already exists

R$50/year at navylily.tv is the Navy Lily subscription that already runs on
AbacatePay. Desktop needs no new checkout, no new product, and no new webhook.
The work is issuing a license file to an existing subscriber, not building a
store.

### What this replaces

The current `premium.go` is a phone-only `unlocked` boolean with a Play Billing
placeholder. It is replaced by license verification on both platforms, not
extended.

## The rule

Paid plugins use the same manifest, the same broker, the same capability list,
and the same install path as anything a stranger could write. No branch anywhere
in the core distinguishes a first-party plugin from a third-party one.

The way to keep that honest is to build the first paid plugin as an unpacked
directory in the plugins folder with zero core changes. If it needs something
the API cannot do, the fix is a public capability, not a private hook.

## Later, in order

1. An optional backend, the first time a real plugin needs one it cannot get
   from the API alone. Desktop-first as a JSON/RPC subprocess, since that's
   the fastest thing to build and test against actual need; it will be
   Android-dead-weight from day one, which is fine for a desktop-only plugin
   and is exactly the signal that means the *next* backend-needing plugin
   should go straight to WASM instead of getting its own subprocess.
2. WASM with host functions, once cross-platform backend logic is needed for
   real, not speculatively.
3. Signing, when third parties publish.
4. Command palette entries and context menu actions, both of which are new
   capabilities over the existing built-in UI.
5. Cross-plugin dependencies. Probably never.
6. Calling the API "stable", after roughly five plugins have shaped it. See
   Strategy above.
