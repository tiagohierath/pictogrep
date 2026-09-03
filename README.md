# Pictogrep

Pictogrep finds local pictures from a plain-language description and turns
references into rough storyboards. Your pictures stay on your computer.

[Website](https://navylily.tv/pictogrep) · [Releases](https://github.com/tiagohierath/pictogrep/releases)

## Windows

1. Download
   **[Pictogrep for Windows](https://github.com/tiagohierath/pictogrep/releases/latest/download/pictogrep-windows-x86_64-setup.exe)**.
2. Double-click the installer.
3. Open **Pictogrep** from the Start menu.

No terminal, Python, Git, or programming tools are required.

## Linux

Download the archive for your computer:

- **[Linux x86-64](https://github.com/tiagohierath/pictogrep/releases/latest/download/pictogrep-linux-x86_64.tar.gz)** — most Intel and AMD computers
- **[Linux ARM64](https://github.com/tiagohierath/pictogrep/releases/latest/download/pictogrep-linux-arm64.tar.gz)** — ARM computers

Extract it and double-click the `pictogrep` file. The archive contains one
standalone executable with no runtime dependencies.

For a user-menu installation instead:

```bash
curl -fsSL https://raw.githubusercontent.com/tiagohierath/pictogrep/main/install.sh | sh
```

This installs the same executable into `~/.local/bin` and adds Pictogrep to the
desktop applications menu. It does not need `sudo`.

## First Run

1. Click **Add pictures**.
2. Choose images or a folder.
3. Type what you remember into the search box.

After pictures are added, Pictogrep quietly prepares only the new or changed
ones for local image search. The first indexing pass downloads the search model
once; later passes reuse it and keep existing picture vectors untouched. Search
becomes useful as soon as the first pictures are ready. Image analysis and
searching happen locally; pictures are never uploaded.

The browser AI runtime is bundled with Pictogrep from pinned, checksum-verified
sources. Model files are fetched from a pinned Hugging Face revision.
Pictogrep keeps the image/text embedding model behind a small internal contract;
CLIP is the default backend today, while model identity and vector dimensions
remain explicit so a future backend cannot silently mix incompatible vectors.

Each picture is analyzed once and only reprocessed if the file changes. Its
compact search vector is kept on disk and loaded into memory when Pictogrep
starts. Repeated text searches are cached too, so they remain fast after closing
and reopening the app.

The menu also opens the storyboard studio, where a reference can be redrawn,
traced, annotated, and saved as a board.

The **Wikimedia Commons** plugin adds an optional tab for browsing Commons
images. It opens with a random selection and also supports text search. Enable
or disable it from **Menu → Plugins**. Results link back to each Commons source
page so licensing and attribution can be checked before using an image.

The optional **Sidebar** plugin provides a quick panel for collections and
storyboards. Images can be dragged from the library onto a collection to add
them to it.

The optional **Command palette** plugin opens with <kbd>Ctrl+K</kbd> (or
<kbd>Command+K</kbd> on macOS). Its initial prototype searches local images by
default and includes shortcuts to the library, folders, Settings, and the
storyboard. Like every official plugin, it is disabled by default.

The **Import from Pinterest** official plugin copies every downloadable image
from a public Pinterest board into the local library. It can add images directly
or organize them into a board folder, and exact duplicates can be skipped. It is
enabled by default: open **Menu → Import from Pinterest**, paste a public board
link, and choose **Download all**. You can turn it off or back on from
**Menu → Official plugins**.

Release installations for Windows and Linux x86-64 include the pinned
[`gallery-dl`](https://github.com/mikf/gallery-dl) helper. Arch and Nix packages
provide it as a package dependency. Source builds and Linux ARM64 standalone
archives use `gallery-dl` from `PATH` when available.

The **Folders** view mirrors indexed source folders and their nested subfolders.
Click any level of the hierarchy to see the pictures inside it. The folder's
**Canvas** action opens a free 2D workspace where pictures can be dragged into
any arrangement; those positions save automatically without moving the files.

The untouched home view mixes the library into a fresh random selection each
time it opens. Folder views and search results keep their meaningful order.

Open a picture and scroll below it to see its tags and semantically similar
pictures from the local index.

## Updating and Removing

Run the Linux installer again to update. Remove only the application with:

```bash
curl -fsSL https://raw.githubusercontent.com/tiagohierath/pictogrep/main/install.sh | sh -s -- --uninstall
```

On Windows, use **Installed apps → Pictogrep → Uninstall**. Removing the
application preserves the picture library and saved boards.

## Local Data

Pictogrep stores its own data separately from the executable:

- Linux: `~/.local/share/pictogrep`
- Windows: `%LOCALAPPDATA%\Pictogrep`

Imported images are copied into `library`; folder membership is stored under
`collections`; canvas coordinates stay under `data`; drawings are saved under
`storyboards`.

### Anonymous daily usage

Desktop releases keep a random installation UUID and its creation date in
`data/usage.json`. After you view a picture, search, open a folder, or add
pictures, Pictogrep records at most one anonymous active day. Offline days stay
queued locally and are retried later. The event contains only the UUID, calendar
date, Pictogrep version, and operating-system/CPU platform; pictures, filenames,
searches, folders, account details, and IP addresses are not stored with it.

## Configuration

Pictogrep also works without the Settings menu. Its user-editable configuration
file is:

```text
~/.config/pictogrep/config.json
```

Set `PICTOGREP_CONFIG` to use a different file. The file is created on first
run, and the Settings menu edits this same file. For example:

```json
{
  "language": "en",
  "browser": {
    "thumbnailSize": "medium",
    "showFilenames": false,
    "homeOrder": "random"
  },
  "indexing": {
    "automatic": true
  },
  "plugins": {
    "wikimedia": false,
    "calendar": false,
    "sidebar": false,
    "vim": false,
    "commandPalette": false,
    "pinterest": true
  }
}
```

Pictogrep always keeps imported originals unchanged. High-quality browsing
previews are generated in its disposable cache and can be rebuilt at any time.

Run `pictogrep paths` to print the active configuration path.

## Build From Source

Development requires Go 1.22 or newer. The built application does not.

```bash
git clone https://github.com/tiagohierath/pictogrep.git
cd pictogrep
go test ./...
go build .
./pictogrep
```

Nix users can build and run the same standalone application with:

```bash
nix run github:tiagohierath/pictogrep
```

Useful development commands are `pictogrep doctor`, `pictogrep paths`, and
`pictogrep web --no-open`. Run `pictogrep --help` for the complete list.

### The license signing key

Installed plugins that declare `"paid": true` in their manifest are unlocked by
an Ed25519-signed license file, verified once when it is imported and then
never checked again. `license.go` holds the public half of that key in
`shippedLicensePublicKey`.

The value committed here is a placeholder whose private half was generated and
thrown away, so no license verifies against a stock build. Anyone shipping paid
plugins has to generate a keypair, paste the public half into that constant,
and keep the private half wherever licenses are issued. It never belongs in
this repository or in a release. The format of a license and the few lines that
sign one are documented at the top of `license.go`.

Rotating the key is safe for people who already imported a license: their
unlock is a stored boolean under `installedPlugins` in the config file, and
nothing re-reads the license that produced it.

## Roadmap

- [x] Subfolders and folder structure visualizer
- [x] Similar images below an opened image
- [x] Optional plugin-style sources without expanding the local image library
- [x] Portuguese translation
- [ ] Japanese and German translations

## License

Pictogrep is available under the [MIT License](LICENSE).
