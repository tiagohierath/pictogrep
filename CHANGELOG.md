# Changelog

## Unreleased

## 0.8.0 - 2026-08-17

- Settings is written in plain language now. Groups say what they hold ("Your pictures", "Search", "Privacy" instead of "Browser" and "Search index"), and the descriptions no longer talk about search vectors or folder membership. Settings also shows how many folders Pictogrep reads and can add another one directly.
- Pinterest boards you import are now kept up to date. Pictogrep re-checks each imported board about once a week and adds only what is new, because a board people keep pinning to used to drift out of date silently. Nothing is ever deleted, and it can be turned off under "Keep imported boards up to date".
- An automatic board check says what it is doing at the bottom of the screen and can be stopped there. It deliberately does not open the import panel, because nobody asked for it.
- The library keeps loading as you scroll instead of stopping at the first batch. Pictogrep used to render one fixed set of pictures and that was the end of the page, so most of a big library was unreachable without searching for it. Pages are drawn one at a time as you get near the bottom, and a random view keeps the same shuffle across pages, so nothing repeats and nothing is skipped.
- Similar images under an open picture also keep loading as you scroll further down the ranking.
- Going to the Folders tab and back no longer reshuffles what you were looking at. The pictures you left on screen are still there.
- Escape and the × now leave the viewer entirely instead of stepping back through every similar picture you clicked to get there.
- Folders can be merged by dragging one onto another. There is no dialog and nothing to name: the merged folder keeps the name of the folder you dragged.
- Right-clicking a folder can delete it, after a confirmation. Only the folder goes away; the pictures stay where they live on disk.
- The welcome screen now offers importing a Pinterest board, which turns the plugin on for you. It stays optional.
- Deleting or merging a folder no longer leaves the library showing it. The pictures you were looking at stayed on screen under the old folder's name, scrolling kept asking for more of a folder that was gone, and the whole thing looked like the delete had failed.
- Deleting a picture no longer rearranges the rest of the library. The grid used to be thrown away and rebuilt, which dealt a random view a completely new order and put you back at the top of it. Only the deleted picture leaves now; everything else stays where it was, including how far down you had scrolled.
- Adding a picture to a folder keeps the shuffle and the pages you had already scrolled through, instead of starting the library over.
- A first run now opens with one screen asking where your pictures are, offering a folder or a Pinterest board, with the language switch right there so the question can be read before it is answered. It is reachable later from Settings.
- Choosing a folder finally means what it says. Every previous way of adding a folder copied its pictures into Pictogrep's own library, which is not what "choose a folder" sounds like and not what people expected. The new picker walks your disk and indexes the folder where it already sits, moving and copying nothing.
- One page of pictures that fails to load no longer ends the scroll for good. It used to put the rest of the library out of reach until you searched or changed folders, and the only sign was an error that disappeared after a few seconds. The place is kept, the footer says what went wrong, and either the retry button or scrolling again picks it back up. Similar pictures under an open picture recover the same way.

## 0.7.5 - 2026-08-15

- Importing a large Pinterest board is much faster. Each picture used to rewrite the entire library file before the next one could start, so a two thousand image board wrote that file two thousand times, every write longer than the one before it. The board now joins the library in a single write.
- A board whose name cannot be used as a folder is refused before the download starts. Finding out afterwards threw away everything the download had just spent half an hour fetching.
- An import that hits an unexpected internal error now fails on its own instead of closing Pictogrep. Work that runs in the background had no protection from that, so a single unexpected error during an import ended the whole program and took the open session with it. The same protection covers folder scans.
- Stopping an import says it is stopping while it puts away what already arrived. The panel used to keep reporting the download, which made the stop button look broken.
- Import progress now reaches the last picture instead of stopping one short of it.
- A downloader that will not exit can no longer wedge importing for the rest of the session. Pictogrep gives it ten seconds and then carries on, where it used to wait forever: no other board could be imported, and Pictogrep could never close itself.
- One damaged record in the search index no longer discards every record saved after it. Pictogrep skips the damaged record, keeps the rest, and writes the file back clean, so the damage costs the pictures it actually touched instead of an afternoon of re-indexing.

## 0.7.4 - 2026-08-15

- Starting a Pinterest import now closes the import panel and puts you back in your library, with a notice at the bottom of the screen saying the board is being taken care of. There is nothing to wait around for, so the panel no longer asks you to.
- Notices that are not errors are now actually shown. Every success message in Pictogrep, including the one that says an import finished, was only being written to the background log and never reached the screen.

## 0.7.3 - 2026-08-15

- Pinterest imports now run inside Pictogrep instead of inside the browser window. Close the import panel, the tab, or the whole browser and the board keeps downloading. Whatever window you open next reattaches to it and reports the result.
- The import says what it is doing while it runs: how many images have arrived, then how many of those have been added to the library.
- Stopping an import keeps the images that already downloaded instead of discarding the whole download.
- Stopping an import now actually stops the downloader. It used to survive the cancel and keep downloading into a folder Pictogrep had already deleted, burning bandwidth for nothing.
- Starting a second import while one is still running is refused instead of running two boards at once.
- An import in progress keeps Pictogrep from closing itself on the idle timer.

## 0.7.2 - 2026-08-15

- Pictogrep now closes itself after an hour with no windows open, instead of staying in the background and holding the library. Closing the tab used to leave the server running, so the next launch refused to start and looked like nothing happened at all.
- An open library or storyboard window keeps Pictogrep running even while you are only drawing, and an import or a folder scan is never cut short by the idle timer.

## 0.7.1 - 2026-08-15

- Fixed Pinterest imports that failed with "could not monitor Pinterest download". The download monitor read the temporary folder while gallery-dl was renaming finished files into place, then treated a file that had just been renamed as a fatal error and cancelled the whole board.
- Stopped Pinterest imports from downloading Idea Pin videos. Pictogrep cannot add them to a library, and they were spending the per-board size and count limits that public boards need for images.
- Made the Nix flake report the version it actually packages, which had fallen a release behind.
- Made the browser smoke test opt-in so `go test ./...` stays fast and deterministic in CI.

## 0.7.0 - 2026-08-14

- Completed Brazilian Portuguese localization across Pinterest import and the remaining browser, plugin, dialog, update, import, canvas, and storyboard interfaces.

## 0.6.1 - 2026-08-13

- Allowed Windows release builds to publish unsigned artifacts when code-signing credentials are not configured.

## 0.6.0 - 2026-08-13

- Added the official “Import from Pinterest” plugin for downloading a public board into the library or an automatically named folder. It is enabled by default, remains toggleable, uses bounded downloads and linear duplicate detection, and includes a guided, cancellable import flow.
- Bundled a pinned `gallery-dl` helper with Windows and Linux x86-64 release installations; Arch and Nix packages provide the helper as a dependency.
- Prevented storyboard saves from overwriting same-named drawings and fixed navigation races that could discard unsaved strokes. Storyboard files and metadata are now replaced atomically.
- Preserved managed-library imports when external folders are refreshed.
- Rejected images with unsafe decoded dimensions before thumbnail decoding, preventing compressed image bombs from exhausting memory.
- Prevented multiple Pictogrep processes from opening and mutating the same library, and limited process reuse to the same version and data home.
- Hardened Linux updates with GitHub-provided SHA-256 verification before a downloaded executable can run.
- Blocked clickjacking of the local interface with framing protections.
- Hardened the Linux installer and release workflow against unintended local builds, unchecked release archives, mutable actions, unpinned installer tooling, and release-signing credential exposure.
