# Every place Pictogrep can use art

This is the complete branding surface map for Pictogrep: icons, logos,
illustrations, screenshots, installer art, and promotional images.

Legend:

- **Exists** — already connected; replace or improve the asset.
- **Available** — the surface exists, but Pictogrep does not use art there yet.
- **Future** — relevant only if that product or distribution channel is added.

## Top 10 — quick and easy version

1. MASTER APP ICON
   Make the main square Pictogrep symbol. This is the source for almost every
   other icon.

2. WINDOWS EXE ICON
   Put the Pictogrep icon inside `pictogrep.exe` so the program file does not
   show a generic Windows icon.

3. MAIN APP LOGO
   Put the Pictogrep logo or wordmark at the top of the main picture library.

4. STORYBOARD LOGO
   Put the same logo or wordmark at the top of the Storyboard screen.

5. README HERO AND SCREENSHOT
   Add a strong cover image and one beautiful screenshot near the top of the
   GitHub README.

6. GITHUB SHARE IMAGE
   Create the large image people see when someone shares the Pictogrep GitHub
   link in Discord, social media, or messaging apps.

7. FIRST-RUN ILLUSTRATION
   Add welcoming art to the empty app users see before importing pictures.

8. BROWSER ICONS
   Make proper small icons for browser tabs, bookmarks, and phone home screens.

9. WINDOWS INSTALLER ART
   Add branded artwork inside the installer, not only on the installer file
   icon.

10. LINUX ICON SET
    Make sharp Linux icons in several sizes, plus a scalable SVG.

## The 10 most important placements

If branding time is limited, start with these. Together they cover the moments
when someone discovers Pictogrep, installs it, launches it, and uses it.

| Priority | Placement | Why it matters | Where to work |
|---:|---|---|---|
| 1 | **Master app icon** | It becomes the visual identity reused throughout the operating system, installer, and browser. It must remain recognizable at very small sizes. | Design the source mark, then export `assets/pictogrep.ico` and `assets/pictogrep.png`. |
| 2 | **Windows application EXE icon** | The installed `pictogrep.exe` currently lacks an embedded icon and can appear as a generic program even though its shortcuts are branded. | Embed `assets/pictogrep.ico` into the EXE during the Windows builds in `.github/workflows/check.yml` and `.github/workflows/release.yml`. |
| 3 | **Main library header logo/wordmark** | This is the brand users see most often while using Pictogrep. It also visually anchors the product around the user's pictures. | Add an SVG mark/wordmark to `web/index.html` and style it in `web/app.css`. |
| 4 | **Storyboard header logo/wordmark** | It makes Storyboard feel like part of Pictogrep rather than a separate utility. | Update the title in `web/practice.html` using the same identity as the library header. |
| 5 | **README hero and main screenshot** | This is the first product impression for most GitHub visitors and explains the application faster than text. | Add a hero and polished library/search screenshot under `docs/images/`, then place them near the top of `README.md`. |
| 6 | **GitHub social-preview image** | This is the branded card people see when the repository link is shared in chats and social media. | Create a 1280×640 share card, keep its source under `assets/brand/source/`, and upload the export in GitHub repository Settings. |
| 7 | **First-run/empty-library illustration** | New users initially have no pictures, making this Pictogrep's best in-app space for expressive artwork and a friendly introduction. | Add the illustration to the “No pictures yet” state in `web/index.html`. |
| 8 | **Dedicated browser icon family** | The current 512 px PNG is reused as a favicon; tailored assets will look sharper in tabs, bookmarks, shortcuts, and home screens. | Add `web/favicon.svg`, `web/favicon.ico`, a small PNG, and `web/apple-touch-icon.png`; link them from both HTML pages. |
| 9 | **Windows installer wizard artwork** | It turns installation from a generic setup flow into a clear Pictogrep experience before the app first opens. | Add `assets/windows/wizard-large.png` and `wizard-small.png`, then reference them in `packaging/windows/pictogrep.iss`. |
| 10 | **Complete Linux icon family** | One 512 px PNG is not enough for consistently sharp menus, launchers, docks, and different desktop environments. | Add a scalable SVG and tuned PNG sizes, then update `desktop_linux.go`, `install.sh`, and `flake.nix`. |

Before adding PNG/JPG artwork, update `.gitignore`: it currently ignores nearly
all image files except `assets/pictogrep.png`.

## Master checklist

| Area | Art placement | Status |
|---|---|---:|
| Windows | Installer `.exe` icon | **Exists** |
| Windows | Installer wizard side/large image | **Available** |
| Windows | Installer wizard header/small image | **Available** |
| Windows | Installed `pictogrep.exe` file icon | **Available** |
| Windows | Start menu icon | **Exists** |
| Windows | Desktop shortcut icon | **Exists** |
| Windows | Installed Apps/uninstaller icon | **Exists** |
| Windows | Running taskbar/window icon | **Future architecture** |
| Linux | Applications menu/search icon | **Exists** |
| Linux | Desktop launcher icon | **Exists** |
| Linux | Dock/task-switcher icon | **Partial** |
| Linux | Software-center icon | **Future packaging** |
| Linux | Software-center screenshots | **Future packaging** |
| Browser | Tab favicon | **Exists** |
| Browser | Bookmark icon | **Exists, basic** |
| Browser | Apple touch/home-screen icon | **Available** |
| Browser | Installable web-app icon | **Available** |
| Browser | Maskable/adaptive icon | **Available** |
| Browser | Generated launch/splash screen | **Available** |
| App | Library header logo/wordmark | **Available** |
| App | Storyboard header logo/wordmark | **Available** |
| App | Menu/About brand card | **Available** |
| App | First-run illustration | **Available** |
| App | Empty-library illustration | **Available** |
| App | Loading/indexing artwork | **Available** |
| App | Empty-folder placeholder | **Available** |
| App | Empty-board placeholder | **Available** |
| App | Branded 404/error page | **Available** |
| App | Consistent action-icon family | **Available** |
| Docs | README logo/hero | **Available** |
| Docs | Library/search screenshot | **Available** |
| Docs | Folders/canvas screenshot | **Available** |
| Docs | Storyboard screenshot | **Available** |
| Docs | Animated demo | **Available** |
| GitHub | Repository social-preview card | **Available** |
| GitHub | Release-note hero/screenshots | **Available** |
| GitHub | Issue/PR templates brand mark | **Optional** |
| Website | Favicon/touch icons | **Future** |
| Website | Open Graph/social-share image | **Future** |
| Website | Homepage hero and feature art | **Future** |
| Store | Microsoft Store art/screenshots | **Future** |
| Store | Flatpak/AppStream art/screenshots | **Future** |
| Store | Snap Store art/screenshots | **Future** |
| macOS | App icon and DMG artwork | **Future** |

## What Pictogrep has now

There are only two tracked branding images:

| File | Current uses |
|---|---|
| `assets/pictogrep.png` (512×512) | Browser favicon, Linux menu icon, Nix icon |
| `assets/pictogrep.ico` (16–256 px) | Windows setup, shortcuts, and uninstaller |

The PNG is doing too many jobs. A 512 px application icon is not an ideal
browser-tab icon, header logo, or small Linux icon. Pictogrep should have a
small family of assets derived from one master design.

## 1. Windows art placements

### Installer file icon — exists

`packaging/windows/pictogrep.iss` uses:

```ini
SetupIconFile=..\..\assets\pictogrep.ico
```

This is the icon people see on the downloaded installer file and during setup.
Replace `assets/pictogrep.ico` to update it.

### Installer large artwork — available

Inno Setup supports a large/tall wizard image through `WizardImageFile`.
Possible art:

- A branded collage of reference images.
- A visual combining picture search and storyboarding.
- A simple oversized Pictogrep mark with a quiet background pattern.

Keep text out of the bitmap when possible; installer text and translations
should remain real text.

Suggested source location:

```text
assets/windows/wizard-large.png
```

### Installer header artwork — available

Inno Setup supports compact header artwork through `WizardSmallImageFile`.
This should usually be the Pictogrep mark, not a detailed illustration.

Suggested source location:

```text
assets/windows/wizard-small.png
```

### Installed application EXE icon — important missing placement

The installer and its shortcuts have an icon, but the actual
`pictogrep.exe` does not currently embed one. That means the raw EXE can show a
generic Windows program icon.

Embed `assets/pictogrep.ico` as a Windows PE resource during `go build`. This is
a build change, not an Inno Setup change. It should be applied to both the CI
cross-build and release workflow.

### Start menu icon — exists

The Inno Setup `[Icons]` section uses the installed ICO. This appears in Start,
application search, and the program shortcut.

### Desktop shortcut icon — exists

The optional desktop shortcut uses the same ICO.

### Installed Apps and uninstaller icon — exists

`UninstallDisplayIcon` uses the installed ICO. This is visible beside
Pictogrep in Windows settings and uninstall surfaces.

### Taskbar, Alt+Tab, and native window icon — not currently controllable

Pictogrep launches the default web browser. The visible window belongs to that
browser, so Windows normally shows the browser's identity while it is running.

To show Pictogrep art there, Pictogrep would need either:

- A native WebView window with its own icon and application identity.
- A well-tested installable browser/PWA experience with manifest icons.

Embedding the icon in `pictogrep.exe` alone will not rebrand a Chrome, Firefox,
or Edge window.

### Error dialogs

The Windows startup-error message currently uses the standard system error
symbol. Keep that symbol because it communicates the error state. If Pictogrep
later has a native dialog/window, the Pictogrep icon can appear in its title bar
without replacing the system error glyph.

## 2. Linux art placements

### Applications menu and desktop search icon — exists

`desktop_linux.go` installs:

```text
~/.local/share/icons/hicolor/512x512/apps/pictogrep.png
```

The generated desktop entry uses `Icon=pictogrep`. This covers application
menus, launchers, and desktop search.

### Nix application icon — exists

`flake.nix` installs the same 512×512 PNG into the Nix package's hicolor icon
tree.

### Complete Linux icon family — available

Only the 512 px PNG is installed today. Add:

- A scalable SVG at `hicolor/scalable/apps/pictogrep.svg`.
- Hand-checked PNGs at 16, 24, 32, 48, 64, 128, 256, and 512 px.

Small icons often need simplified detail and optical adjustment. Do not assume
that an automatic resize will look good at 16 or 24 px.

When adding them, update all of these together:

- `desktop_linux.go` installation.
- `desktop_linux.go` uninstallation.
- `install.sh` uninstallation.
- `flake.nix` packaging.

### Dock and task switcher — partial

The desktop launcher is branded, but Pictogrep's active window is still a
browser window. Reliable dock/task-switcher branding requires a native window
or tested installed-web-app approach.

### Linux notifications — future

If Pictogrep adds desktop notifications, use `pictogrep` as the themed
application icon. A notification may also contain a content thumbnail when the
notification is specifically about one image.

### Linux software centers — future packaging

Add AppStream metainfo if Pictogrep is distributed through Linux application
stores. It can provide:

- The app icon.
- A primary library/search screenshot.
- Folder/canvas screenshots.
- Storyboard screenshots.
- Optional short demo video, where supported.

This becomes relevant for Flatpak and distribution packages. The current
repository has no AppStream listing.

## 3. Browser art placements

### Tab favicon — exists, but should be specialized

Both `web/index.html` and `web/practice.html` use the full 512×512 PNG:

```html
<link rel="icon" href="/assets/pictogrep.png">
```

Create dedicated browser assets:

- `web/favicon.svg` — scalable modern favicon.
- `web/favicon.ico` — 16 and 32 px fallback.
- `web/favicon-32.png` — optional hand-tuned small icon.

Apply the links to both HTML pages.

### Bookmarks and browser history — exists, basic

Browsers reuse the favicon for bookmarks and history. A dedicated favicon
family improves these surfaces automatically.

### Apple touch/home-screen icon — available

Add a square touch icon and link it from both pages:

```text
web/apple-touch-icon.png
```

This is used when a page is saved to an Apple device's home screen. It should
be designed as an icon, not merely be a transparent logo dropped on a canvas.

### Installable web-app icons — available

Add a web app manifest if Pictogrep should be installable from a browser:

```text
web/app.webmanifest
web/icon-192.png
web/icon-512.png
web/icon-maskable-512.png
```

Use separate normal and maskable versions. Maskable artwork needs generous safe
padding and an opaque background because operating systems may crop it into a
circle, rounded square, or another shape.

### Launch/splash screen — available

Some browsers generate a launch screen from the manifest's:

- Application icon.
- Application name.
- `background_color`.
- `theme_color`.

This is usually generated rather than supplied as a standalone splash bitmap.
Pictogrep's changing localhost port and launch behavior must be tested before
advertising browser installation.

### Browser shortcut icons — optional

If the web manifest later includes shortcuts such as **Library**,
**Storyboard**, or **Add pictures**, each shortcut can have a small icon.

## 4. Art inside the Pictogrep interface

### Main library header — high priority

`web/index.html` currently shows `pictogrep` as plain text. Good options:

- A small mark plus a text wordmark.
- A wordmark SVG by itself.
- The mark at the left with the wordmark centered, if the layout is adjusted.

Use SVG for a crisp interface logo. Keep the text alternative accessible.

### Storyboard header — high priority

`web/practice.html` currently shows `PICTOGREP / STORYBOARD` as plain text.
Use the same mark/wordmark system as the library so the two tools feel like one
product. A small “Storyboard” descriptor can remain real text.

### Side menu and About area

The drawer can contain:

- A compact mark at the top.
- A small Pictogrep wordmark.
- A brand card with the version and tagline.
- An About illustration or maker credit.

Do not make the menu header so large that it competes with its navigation.

### First-run welcome

Before any pictures are added, show a welcoming brand illustration above the
first call to action. Strong concepts include:

- A magnifying glass finding one picture in a loose collage.
- A trail from a picture library to a storyboard.
- The Pictogrep character/mascot, if one is developed.

This is one of the best places for expressive brand art because no user images
are present yet.

### Empty library

The current “No pictures yet” area can use the same first-run illustration or a
simpler variation.

### Empty folder

When a folder has no pictures, use a small folder-specific illustration. It can
share the first-run visual language while communicating a different action.

### Folder thumbnails with no preview

The UI currently uses a plain grey placeholder. A subtle branded pattern,
monogram, or simplified mark could appear when no image preview exists. Keep it
quiet so real photo previews remain dominant.

### Empty saved boards

Use a small blank-paper/storyboard illustration when no boards have been saved.

### Loading and first-time indexing

The first semantic search downloads/prepares a model and indexes pictures.
Possible art:

- A subtle animated mark.
- A sequence of pictures becoming searchable.
- A small illustration paired with real progress text.

Animation should not hide progress or make the app feel slower. Respect reduced
motion preferences.

### Search-empty state

When a search returns no pictures, use a distinct “nothing found” illustration
instead of reusing the empty-library art. It should suggest changing the query,
not adding a library.

### Image viewer

The opened picture should remain the visual focus. Appropriate branding is
limited to small interface marks or controls. Do not put decorative art behind
the user's image.

### Folder canvas

A subtle canvas texture or corner mark could brand the workspace, but it must
not interfere with arranging pictures. It should be optional or extremely
quiet.

### Storyboard paper and exported boards

The user's drawing is their creative output. Do not automatically watermark
saved storyboards. If branded exports are useful, add an explicit optional
template or “Include Pictogrep mark” setting.

### 404 and error pages

Unknown routes currently return a plain server 404. A branded error page can
use a small lost-picture/search illustration and a link back to the library.

### Action icons

Pictogrep can have a coherent SVG icon family for:

- Search.
- Add pictures.
- Add folder.
- Menu and close.
- Storyboard/draw.
- Saved boards.
- Tag.
- Canvas.
- Previous and next.
- Pen, eraser, colour lasso, trace, mirror, undo, clear, and save.
- Zoom in, zoom out, and reset view.

These are product UI symbols rather than logo placements, but consistency here
strongly affects brand quality. Use SVG or CSS/native symbols, preserve visible
labels where helpful, and always keep accessible names.

### Cursor art — optional

The drawing tools could use custom pen/eraser cursor shapes. Keep a normal
fallback and test cursor hot spots carefully. Avoid a custom cursor for ordinary
navigation.

### Mascot or character — optional

If Pictogrep develops a mascot, natural placements are:

- First run.
- Empty and no-result states.
- Loading/indexing.
- 404/error pages.
- README hero.
- Social cards and release announcements.

Avoid placing it over the user's image grid or drawing canvas.

## 5. README and documentation art

The current `README.md` contains no images. Add:

### README logo or hero

Place a horizontal wordmark or compact hero directly below the title. It should
tell visitors “local picture search + storyboarding” quickly.

### Main product screenshot

Show the library populated with an attractive, safe demo collection and a
meaningful search. This should be the primary screenshot.

### Feature screenshots

Useful secondary images:

- Natural-language search results.
- Folders and nested folder structure.
- Freeform folder canvas.
- Open-image related results.
- Storyboard studio.
- Completed rough storyboard.

### Animated demo

A short video/GIF can show:

```text
Add pictures → search → open a result → draw it in Storyboard
```

Use a compressed format and provide a still image fallback. Avoid making the
README slow to load.

### Architecture/process illustrations

Optional diagrams can explain:

- Pictures remain local.
- Each picture is indexed once.
- Search text becomes a local match.
- User pictures flow into a storyboard without being uploaded.

These can reinforce trust and brand identity at the same time.

Store documentation media under:

```text
docs/images/
```

Use owned, fictional, or properly licensed demo images. Remove personal paths,
tags, thumbnails, and metadata before committing screenshots.

## 6. GitHub art placements

### Repository social preview — high priority

This is the large card shown when the GitHub repository link is shared on social
platforms and chat apps. Upload it manually in repository **Settings → Social
preview**.

Recommended working size: 1280×640, under 1 MB. Include:

- Pictogrep mark/wordmark.
- A short value proposition.
- A simple product visual or collage.
- Enough empty margin for unpredictable crops.

The uploaded image is managed by GitHub, but keep its editable source in the
repository so it can be regenerated.

### GitHub release notes

Each release can use:

- A release header/card.
- One screenshot for the headline feature.
- Before/after images for visual changes.
- A short demo clip.

Reuse stable assets from `docs/images/` rather than uploading unrelated copies
that become hard to maintain.

### Issue and pull-request templates — optional

A tiny header mark is possible, but usually unnecessary. Clear instructions
matter more than decorative art here. Reserve art for a welcoming community
page or contribution guide.

### GitHub profile/organization art — external

If Pictogrep gets its own GitHub organization, it can have an avatar and profile
README hero. The current repository lives under a personal account, so this is
not a repository-controlled product surface today.

## 7. Public website and social art

If a Pictogrep website is added, create:

- Website favicon family.
- Apple touch icon.
- Web manifest icons.
- Homepage hero illustration or product mockup.
- Feature-section illustrations.
- Screenshot gallery.
- Open Graph image for link previews.
- X/Twitter card image.
- Press-kit logo exports.
- Download-button platform icons.
- Branded 404 page.
- Blog/release post cover templates.

Do not add social-share metadata to the localhost application itself. Localhost
pages are not useful public share targets; those images belong on a public site.

## 8. Store and package listing art

These are future surfaces, activated only when the matching channel exists.

### Microsoft Store

- Store/application icon family.
- App-list and tile images.
- Listing screenshots.
- Promotional tiles and hero art.
- Optional trailer/video poster.

### Flatpak / Flathub / AppStream

- Reverse-DNS application icon.
- Primary screenshot.
- Additional feature screenshots.
- AppStream listing metadata.

### Snap Store

- Store icon.
- Listing screenshots.
- Banner/featured artwork where offered.
- `snapcraft.yaml` icon.

### AppImage

- Bundled desktop icon.
- `.DirIcon`.
- AppStream icon and screenshots.

### Chocolatey, Winget, and other catalogs

- Listing icon where the catalog supports one.
- Screenshots or package-page hero where supported.
- Repository/homepage social image, which many catalogs reuse indirectly.

Text-only package manifests do not need art added merely for completeness.

## 9. Future macOS art placements

Pictogrep does not currently ship a macOS app, but a future build would need:

- `.icns` application icon with the expected scale variants.
- Finder application icon.
- Dock and Cmd+Tab icon.
- Native window/title identity.
- DMG background illustration and volume icon, if using a styled DMG.
- Installer/package icon, if using a package installer.
- Apple touch/PWA icons for a browser-installed version.
- Mac App Store screenshots and promotional art, if distributed there.

## 10. Recommended asset library

Keep editable source art separate from generated exports:

```text
assets/
├── brand/
│   └── source/
│       ├── pictogrep-mark.svg
│       ├── pictogrep-wordmark.svg
│       ├── pictogrep-lockup.svg
│       ├── social-preview.svg
│       └── BRAND-GUIDE.md
├── pictogrep.png
├── pictogrep.ico
├── linux/
│   ├── pictogrep.svg
│   └── icons at 16, 24, 32, 48, 64, 128, 256, and 512 px
└── windows/
    ├── wizard-large.png
    └── wizard-small.png

web/
├── favicon.svg
├── favicon.ico
├── favicon-32.png
├── apple-touch-icon.png
├── icon-192.png
├── icon-512.png
├── icon-maskable-512.png
└── app.webmanifest

docs/images/
├── hero.png
├── library.png
├── search.png
├── folders-canvas.png
├── storyboard.png
└── demo.mp4
```

Suggested master pieces:

1. **App mark** — square symbol for icons and avatars.
2. **Wordmark** — the Pictogrep name for headers.
3. **Lockup** — mark and wordmark together.
4. **Small-size mark** — simplified for 16–32 px.
5. **Maskable mark** — padded and opaque for adaptive shapes.
6. **Hero composition** — wide composition for README and website.
7. **Social card template** — 2:1 share image.
8. **Illustration family** — first run, empty search, empty folder, and error.
9. **UI icon family** — consistent functional symbols.
10. **Screenshot/demo collection** — reusable product presentation media.

## 11. Repository traps to fix before adding art

### `.gitignore` currently hides new images

The repository ignores all loose `*.png`, `*.jpg`, `*.webp`, and `*.gif` files
and only exempts `assets/pictogrep.png`. New branding files can look correct
locally while never being committed.

Add narrow exceptions for approved locations such as:

```gitignore
!assets/brand/**
!assets/linux/**
!assets/windows/**
!web/*.png
!docs/images/**
```

Keep the broad ignore for user libraries and generated storyboard content.

### Embedded files and routes

`assets.go` currently embeds `web/*` plus `assets/pictogrep.png`.

- A new file directly inside `web/` is included by the current pattern.
- A new nested `web/icons/` directory needs the embed rule changed.
- A new runtime file under `assets/` must be added to the embed rule explicitly.
- `server.go` must serve the file with the correct content type.
- Both `web/index.html` and `web/practice.html` need their own `<head>` updates.

Promotional screenshots should not be embedded in the executable. Keep them in
`docs/images/`.

## 12. Brand art rules for Pictogrep

- Make the mark recognizable at 16 px.
- Use no more than one or two visual ideas in the app icon.
- Avoid words inside the square app icon; use a separate wordmark.
- Test transparent art on white, black, and mid-tone backgrounds.
- Create an optically simplified small icon instead of blindly shrinking.
- Keep the UI logo modest so user pictures remain the main visual content.
- Never watermark imported pictures.
- Make branded storyboard exports optional.
- Keep interface icons understandable and accessible.
- Respect reduced motion for animated brand elements.
- Use owned or licensed pictures in every public screenshot.
- Keep a deterministic export script so PNG, SVG, and ICO versions do not drift.

## Recommended order

1. Design the master mark, wordmark, and lockup.
2. Improve `assets/pictogrep.ico` and `assets/pictogrep.png`.
3. Embed the icon into the Windows application EXE.
4. Put the wordmark in the library and storyboard headers.
5. Add dedicated browser/favicon/touch icons.
6. Create first-run, empty-search, and empty-folder illustrations.
7. Add a README hero and polished screenshots.
8. Upload the GitHub social-preview image.
9. Add Windows installer wizard artwork.
10. Add the complete Linux icon family.
11. Add PWA, AppStream, store, and macOS art only with those products.

## Useful specifications

- [Microsoft Windows app icons](https://learn.microsoft.com/en-us/windows/apps/design/iconography/app-icon-construction)
- [Inno Setup `SetupIconFile`](https://jrsoftware.org/ishelp/index.php?topic=setup_setupiconfile)
- [Inno Setup `WizardImageFile`](https://jrsoftware.org/ishelp/index.php?topic=setup_wizardimagefile)
- [Inno Setup `WizardSmallImageFile`](https://jrsoftware.org/ishelp/index.php?topic=setup_wizardsmallimagefile)
- [Freedesktop Icon Theme Specification](https://specifications.freedesktop.org/icon-theme/latest/)
- [MDN web app icons](https://developer.mozilla.org/en-US/docs/Web/Progressive_web_apps/How_to/Define_app_icons)
- [MDN web app manifest](https://developer.mozilla.org/en-US/docs/Web/Progressive_web_apps/Manifest)
- [GitHub social previews](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repositorys-social-media-preview)
