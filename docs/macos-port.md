# Porting Pictogrep desktop to macOS

Written 2026-09-07. Not started. This is a plan, not a record of work done.

## The short version

Most of the port is already free. Pictogrep is pure Go with no CGO and no
webview binding: it starts a local HTTP server and opens the user's browser.
That means no Cocoa, no native window, no Objective-C bridge, nothing that
normally makes a Mac port expensive.

Verified on 2026-09-07:

```
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...   # exit 0
```

It already compiles clean. `main.go` already shells out to `open <url>` on
darwin, and `reveal_unix.go` already uses `open -R` for "show in file manager".
The Linux-only code is all behind `//go:build linux` with a working
`desktop_other.go` fallback.

So the work is not "make it run on macOS". It is "make it *install and update*
on macOS without embarrassing us".

## The one strategic decision: skip Apple's 99 USD

Apple's Developer Program costs 99 USD/year, renewing, and it is the only route
to a Developer ID certificate for signing and notarizing apps distributed
outside the App Store. There is no one-off purchase and no free distribution
tier.

We do not need it, because of how Gatekeeper actually works. The blocking
"unidentified developer" dialog only fires on files carrying the
`com.apple.quarantine` extended attribute, and that attribute is applied by the
*downloader*. Browsers set it. `curl` in a terminal does not.

Pictogrep is not a double-clickable GUI app. It is a binary that serves a page.
So we distribute it the way the Linux build is already distributed, through a
terminal install, and macOS never shows a warning.

Ranked, cheapest first:

1. **`curl | sh` install script.** Free, no signing, no prompts. Mirrors the
   existing `install.sh`. This is the plan.
2. **Homebrew formula in a personal tap** (`brew install tiago/tap/pictogrep`).
   Also free and also unquarantined, because a *formula* installs a plain
   binary. Good second step once there are real Mac users.
3. **Unsigned `.app` in a `.dmg`.** Free, but users hit the wall and have to
   right-click then Open. Bad first impression. Skip.
4. **Developer ID + notarization.** 99 USD/year *and* it needs a Mac to run
   `notarytool`, which we do not have. Only revisit if Mac users specifically
   ask for a drag-to-Applications experience.

Explicit non-goal for v1: no `.app` bundle, no `.dmg`, no code signing, no
Homebrew cask. Those were the single largest chunk of estimated work and they
buy nothing right now.

## The actual work

### 1. Audit the platform branches (~1h)

The code compiles for darwin, which is not the same as behaving correctly on
darwin. Each of these is currently written as "windows or else Linux", and
darwin silently takes the Linux path. Check each one is right:

- `update.go:158,164,177,319` - self-update. Handled in step 2 below, this is
  the big one.
- `instance_lock.go:45` and `instance_lock_unix.go` - flock on the unix path
  should be fine on darwin, confirm the lock file location is sane.
- `gallerydl_unix.go` - how gallery-dl is located. Homebrew installs to
  `/opt/homebrew/bin` on Apple Silicon, which is *not* on the PATH of a
  GUI-launched process. Since we launch from a terminal this is probably fine,
  but check the lookup and the error message.
- `app.go:166`, `pinterest.go:528` - windows branches, confirm the fallback is
  correct for darwin.
- Config and data directories. `os.UserConfigDir()` returns
  `~/Library/Application Support` on darwin. Decide deliberately whether the
  library lives there or in a dotfile directory like on Linux, and make it
  consistent. Look for any hardcoded XDG assumptions outside
  `desktop_linux.go`.
- `desktop_other.go` returns "desktop-menu installation is only available on
  Linux" for both install and uninstall. That is honest and fine for v1.

### 2. Darwin support in the updater (~1-2h)

`update.go:98-120` picks a release asset by name and currently only knows
`pictogrep-windows-x86_64-setup.exe` and `pictogrep-linux-{x86_64,arm64}`.

- Add `pictogrep-darwin-arm64` and `pictogrep-darwin-x86_64` to the asset name
  map.
- `update.go:319` gates the in-place binary replacement on
  `runtime.GOOS != "linux"`. Extend that to darwin. The replace-and-restart
  dance works the same way on macOS, since unlink-while-running is allowed.
- `installedByScript()` and `currentPackageManager()` need a darwin answer.
  Homebrew should be detected as a package manager so we tell the user to
  `brew upgrade` instead of self-replacing over Homebrew's file.
- Keep the SHA256 digest verification path intact, it is already
  platform-independent.

### 3. Build and release (~1h)

- Add darwin arm64 and amd64 to whatever produces release binaries. **These are
  built locally, never in a GitHub Action.** See the standing rule: Pictogrep
  releases are local-only.
- Cross-compiling from this NixOS box works today with
  `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0`, so no Mac is needed to *produce* the
  artifacts. Both arches, since Intel Macs are still around.
- Extend `install.sh` with a darwin branch: detect `uname -s` = Darwin, map
  `uname -m` (`arm64` / `x86_64`) to the asset name, install to
  `/usr/local/bin` or `~/.local/bin`.

### 4. Docs and changelog (~30 min)

- README install section gets a macOS line.
- CHANGELOG entry.
- Note in the docs that the desktop-menu shortcut is Linux-only.

## What this does not solve

**Nobody has tested it on a Mac.** Cross-compiling proves it links, not that it
works. Before announcing anything, someone with a Mac needs to run through:
onboarding, picking a library folder, thumbnail generation, gallery-dl, the
Pinterest sync, mDNS pairing with the Android app, and the self-update. mDNS in
particular is worth real attention, since macOS runs its own mDNSResponder and
may fight our implementation, and recent macOS versions prompt for local
network permission the first time a process does local discovery.

That human-with-a-Mac step is the real gate on shipping, not the code.

## Rough total

About half a day of implementation for a shippable terminal-installed build,
plus one testing session on borrowed hardware.
