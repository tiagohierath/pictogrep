# Pictogrep

Pictogrep is a small tool for two closely related jobs: **finding** the images
you half-remember and **turning** them into rough storyboards before the idea
evaporates.

Point it at one or more folders of reference images, build a CLIP index, then
search with plain language in a terminal UI:

```bash
pictogrep
```

Type `red cloak`, `foggy street`, `girl sitting`, `ornate helmet`, or whatever
you remember. Pictogrep ranks your local images by visual meaning and opens the
ones you choose.

When a reference wants to become a shot, open the browser storyboard mode. It
puts the reference beside a simple drawing surface so you can make fast, ugly,
useful boards. Those boards can stand on their own or travel into Milklily to
be ordered, timed, and turned into an animatic.

## The Friend Setup

This is the shortest path for someone who just cloned the repo:

```bash
cd pictogrep
./bin/pictogrep setup
./bin/pictogrep install-user
pictogrep index ~/Pictures
pictogrep
```

If something feels wrong:

```bash
pictogrep doctor
```

## What It Does

- Searches your own image library with natural language.
- Runs locally; your images are not uploaded anywhere.
- Opens a simple TUI when you run `pictogrep`.
- Keeps the fast CLI flow: `pictogrep "girl sitting"`.
- Refreshes remembered image folders automatically every seven days when used.
- Creates editable image tags, manually or from CLIP search results.
- Opens a browser storyboard tool for turning references into quick, useful
  shot drawings.
- Uses `mpv` for viewing images when available.

## Quick Start With Nix

```bash
git clone https://github.com/tiagohierath/pictogrep.git
cd pictogrep
nix develop
./bin/pictogrep setup
./bin/pictogrep install-user
pictogrep index ~/Pictures/reference
pictogrep
```

The first run downloads the CLIP model weights from Hugging Face. After that the
model is cached locally.

## Quick Start Without Nix

You need Python 3.12+, pip, and preferably `mpv`.

```bash
git clone https://github.com/tiagohierath/pictogrep.git
cd pictogrep
./bin/pictogrep setup
./bin/pictogrep install-user
pictogrep index ~/Pictures/reference
pictogrep
```

If you do not have `mpv`, Pictogrep tries `xdg-open` or `gio open`. You can
also set your own viewer:

```bash
export PICTOGREP_VIEWER="feh"
```

## Commands

Every command supports `--help`. `pictogrep version` prints the installed
release and `pictogrep paths` prints the local state locations.

Open the TUI:

```bash
pictogrep
```

Build or rebuild the index. Pictogrep remembers these folders and refreshes
them automatically the next time you use it after seven days:

```bash
pictogrep index ~/Pictures/reference
pictogrep index ~/Pictures/reference ~/Pictures/archive
```

Create an image tag and add images by hand, or have CLIP fill it from the
current index. Tags are represented by local folders containing symlinks:
original images are never moved or duplicated, and one image can carry many
tags.

```bash
pictogrep tags create cats
pictogrep tags add cats ~/Pictures/cat.jpg
pictogrep tags fill cinematic "cinematic moody composition" --limit 50
pictogrep tags list
pictogrep tags send cinematic       # run inside a Milklily project
```

The resulting `collections/cats/` and `collections/cinematic/` folders are
ordinary local tag folders. Add or remove symlinks in a file manager whenever
you want; the next weekly refresh picks up manually added image files. The
storyboard browser has a tag selector, and its CLIP search respects that
selector.

## From search to storyboard to film

Pictogrep is useful before and during a film project. Search your library for
the image that has the right *feeling*, sketch a version that fits your shot,
then either keep the board here or hand it to Milklily for ordering and timing.

## Milklily Projects

From inside a Milklily project, Pictogrep can link a tag's references into
`refs/visual/pictogrep/<tag>/` and save sketches straight to the film's
inbox. Neither operation moves an original image.

```bash
pictogrep tags send cinematic
pictogrep storyboard --project
milklily intake boards main
```

Storyboard sidecars record the source image, selected tags, CLIP query, and
aspect ratio. Milklily can use that context when it imports the sketch.

### Order standalone boards in Milklily

The normal Pictogrep storyboard command saves drawings into its own
`storyboards/` folder. That is useful when you want to sketch first and decide
on the film later. Milklily's browser board can import that folder safely:

```bash
# Make drawings here; originals remain in Pictogrep's storyboards/ folder.
pictogrep storyboard

# From inside a Milklily project, copy and order those drawings as an animatic.
milklily board main --images-dir /path/to/pictogrep/storyboards --open
```

`--images-dir` copies PNG, JPG, and WebP files into the Milklily project's
`storyboards/inbox/`; it never moves or changes the Pictogrep originals. In
the board, drag images from the unsorted pane into the EDL, set their duration,
preview the cut, and save. The board has Vim-style navigation: `Tab` switches
between the image pane and EDL, `h/j/k/l` moves the selection, `J/K` reorders
EDL shots, and `U/I` shortens or lengthens a shot.

Check whether dependencies, viewer, and index are ready:

```bash
pictogrep doctor
```

Search from the shell and open the top results:

```bash
pictogrep search "girl sitting"
```

Open the storyboard doodle tool:

```bash
pictogrep storyboard
```

Open storyboard mode for a specific folder and save boards somewhere else:

```bash
pictogrep storyboard ~/Pictures/reference --out ~/storyboards
```

Print results without opening a viewer:

```bash
pictogrep search "red cloak" --print
```

Limit results:

```bash
pictogrep search "foggy city" --limit 12
```

## TUI Keys

- Type normally; `Backspace`, `Delete`, `Left`, and `Right` edit the search.
- Press `Enter` to search and open one MPV slideshow with the results.
- `Up` / `Down`: move through results.
- `Ctrl+B`: open the browser storyboard tool.
- `Ctrl+O`: open the selected image.
- `Ctrl+P`: replay the whole result set as one slideshow.
- `Ctrl+Y`: copy the selected image path.
- `Ctrl+R`: reveal the selected image in its folder.
- `Ctrl+T`: type a tag for the selected image.
- `Ctrl+G`: type a tag for the top 30 current CLIP matches.
- `PageUp` / `PageDown`: move faster through results.
- `Ctrl+U`: clear the search line.
- `Esc` or `Ctrl+Q`: quit.

## Storyboard Mode

Storyboard mode opens a local browser page:

```bash
pictogrep storyboard
```

It shows one reference image on the left and a white canvas on the right. Drag
the wide divider between them when you want the reference or the drawing board
bigger; click `=` in the divider or double-click the divider to reset the view.
Use it for rough storyboard ideas, not polished art.

The important controls are:

- `Pen` / `p` / `1`: draw with a translucent upright nib; Wacom and other compatible
  pens control its weight and opacity with pressure.
- `Eraser` / `e` / `2`: erase with white strokes.
- `Colour lasso` / `s` / `3`: circle an area to add soft colored-pencil grain
  behind the graphite lines.
- `Trace` / `t` / `4`: show the reference faintly under the board while drawing.
  The reference is not saved into the storyboard PNG.
- Brush slider or `q` / `w`: decrease or increase stroke size.
- `Undo` / `Ctrl+Z` / `z`: undo the last stroke or clear.
- `Clear`: wipe the current board.
- `Save now`: save the current board.
- `Save + Next`: save and move forward.
- `Ctrl+Enter`: save and move forward.
- `Skip`: move forward without intentionally saving the current board.
- `Prev`: go back one image.
- Reference `-` / `+`: zoom only the reference image inside its pane.
- Divider `=`: reset the pane split and reference zoom.
- Reference `Mirror`: flip the reference and trace underlay horizontally.
- `Add refs`: add up to five persistent reference images around the board.
  Drag them to reposition, use the corner handle to resize, or click `×` to
  remove one. They are kept in the storyboard output's `references/` folder.
- Search: type a CLIP query and press `Enter` to load the top 80 reference
  images; clear it and press `Enter` to return to shuffled All images.
- Aspect selector: switch between `4:3`, `16:9`, `Pan H 2:1`, and `Pan V 3:4`.
  The pan formats give you moderate extra width/height for later camera moves.

The drawing canvas uses a soft dab brush rather than straight vector strokes,
so quick sketching feels more like marking pixels on paper. The dark surround
around the board makes the composition edge easier to see. Each board includes
a thin dark-blue center cross and safe-area border, visible while drawing and
saved into the final PNG.

`All images` is the default and is shuffled each time you load it. You can also
choose the `30 most recent`, `100 most recent`, or a custom recent count.
`4:3` is the default board format. Boards autosave after the board has been idle
briefly, but empty drawings are not saved; use `Skip` to move past a reference
without creating a board. Saved boards are written to:

```text
storyboards/
```

## Files

Pictogrep writes its local index here:

```text
data/embeddings.npy
data/metadata.json
data/index-state.json
collections/
storyboards/
```

Those files are ignored by git because they are machine-specific and can be
large. The same is true for `images/`, `.venv/`, and loose image files in the
project folder.

## Notes

The default model is `ViT-B-32` with `laion2b_s34b_b79k` weights. You can
override it:

```bash
export PICTOGREP_MODEL="ViT-B-32"
export PICTOGREP_PRETRAINED="laion2b_s34b_b79k"
```

On NixOS, use `nix develop`; it sets the library path that PyTorch wheels need.
