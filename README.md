# Pictogrep

Pictogrep finds local pictures from a plain-language description and turns
references into rough storyboards. Your pictures stay on your computer.

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

The first search automatically prepares the local image-search model and shows
useful results as soon as the first pictures are ready. It keeps improving those
results in the background. This requires an internet connection once. Later
searches use the cached model. Image analysis and searching happen locally;
pictures are never uploaded.

Each picture is analyzed once and only reprocessed if the file changes. Its
compact search vector is kept on disk and loaded into memory when Pictogrep
starts. Repeated text searches are cached too, so they remain fast after closing
and reopening the app.

The menu also opens the storyboard studio, where a reference can be redrawn,
traced, annotated, and saved as a board.

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

## Roadmap

- [x] Subfolders and folder structure visualizer
- [x] Similar images below an opened image
- [ ] Optional plugin system for installing add-on features without expanding the core app
- [ ] Translations for Portuguese, Japanese, and German
