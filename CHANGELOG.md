# Changelog

## Unreleased

- Preparing pictures for search uses more than one core. The search runtime will not start a second thread unless the page it runs in is allowed shared memory, Pictogrep never sent the two headers that allow it, and so every release so far prepared a library on one core no matter how many the machine had. Nothing about what gets prepared changed, only how much of the computer works on it.
- Preparing a picture reads a preview of it instead of the whole file. The model looks at a 224 pixel square, so a 24 megapixel photograph was decoded in full to produce something the size of a postage stamp. A picture too large to preview safely is still read whole, so nothing drops out of search, and pictures already prepared stay prepared.
- A page on the internet cannot probe your library any more. Any website can point an image tag at an address on your own computer and learn from what loads, and Pictogrep now refuses to answer anything that is not its own page.
- Pictogrep can be run by an Android app. The server can be told to shut down when whatever started it goes away, to leave the library unlocked, and to stop looking for its own updates, which are the three habits that make sense for a program you start yourself and none of which make sense inside an app. Nothing changes for a normal installation.
- Pictogrep can require a secret. A local address keeps other computers out but not other programs on the same computer, which barely matters on a desktop you own and matters a great deal on a phone full of other people's apps. Given a secret to expect, Pictogrep answers only requests that carry it. Set nothing and nothing changes.

## 0.8.6 - 2026-08-17

- A folder on a second drive can be chosen. The folder picker started in your home folder and walked one click at a time, which left an external or second disk reachable only by climbing all the way to the top of the drive, so the way to use those pictures was to copy them into the library. The picker now has the current path in a box you can type or paste into, so a path like /media/disk2/art gets you there in one go. Nothing about where pictures live changed: Pictogrep still reads them where they are, and copies made to work around this can be deleted.
- The picker stops refusing folders it did not manage to count. It counts pictures under a folder to show how many you are about to add, and that count gives up early on a slow drive or a deeply nested tree. A folder it gave up on was reported as having no pictures and could not be chosen at all, which is exactly what happened on external disks. A count that did not finish is now treated as unknown rather than as empty.
- The interface is one interface. Spacing, type sizes, greys, corners and control heights now come from a single set of values instead of being decided again in every panel, so a dialog, a settings row and an import panel are built from the same parts. Nothing moved; things that were meant to match now do.
- One button. Every button in the app is the same shape and size, with exactly two variations: the one action a panel is about is filled dark, and anything that deletes a file is filled red. Buttons used to differ by a pixel or two of padding and three shades of border depending on which panel they landed in.
- One dialog. Every dialog has the same width step, padding, heading size and action row, with Cancel and the main action always in the same place. The create-folder dialog no longer has its own oversized centred heading.
- One word for each thing. A file you can look at is a picture everywhere, not an image in half the app; a group you put pictures in is a folder, never a collection or a tag; board means a Pinterest board and nothing else; and what happens before a picture is searchable is called preparing rather than indexing, so Settings, the search bar, the dialogs and the status line stop describing the same operation three ways. The Portuguese interface got the same pass, including telling a Pinterest board apart from your own folders.
- Small print got readable. The smallest text in the app was 9 and 10 pixels in places, mostly on phones, and is now 11 at minimum.

## 0.8.5 - 2026-08-17

- The installer no longer refuses to install Pictogrep when the picture downloader it bundles cannot run. That downloader is an ordinary dynamically linked program, which NixOS and other systems without the usual loader will not start, and failing over it meant those systems could not install or update Pictogrep at all. Pictogrep is installed either way now, and says so: imports use a gallery-dl already on the system if there is one, and name where to get it if there is not.

## 0.8.4 - 2026-08-17

- Pictogrep keeps itself up to date. It knew how to install an update and never once offered to: the check ran only when somebody opened About and pressed a button, so an installation sat on the version it was installed with until its owner went looking. It now checks a few times a day on its own, installs what it finds, and the new version starts the next time you open Pictogrep. It can be turned off under Settings.
- A Pictogrep somebody built themselves, or copied into their own bin directory, can be updated. Only an installation carrying the installer's marker file counted before, which left every other standalone binary with no way to update at all: no button, and nothing automatic either.
- Nothing is ever written over a copy a package manager owns. A Nix or system package is still updated from there, and Pictogrep only says when a newer version exists.

## 0.8.3 - 2026-08-17

- Import from web, under Official plugins below the Pinterest import. Paste an artist's gallery, profile, or tag page from any site the included downloader supports, and choose either or both of two things: download all past images, which takes a whole gallery in one go, or download all from now, which checks the page daily and adds only new work the way a feed reader does. Pinterest was never the only place art lives, and following an artist used to mean remembering to go look.
- Websites you follow are listed with the folder each one fills, both in the import panel and under Official plugins, and any one of them can be dropped on its own.
- Following websites has its own switch, separate from the Pinterest one, so turning boards off no longer quietly stops every artist you follow.
- A followed website remembers what it has already downloaded, so a daily check fetches only new work instead of pulling the same newest few every time. A check that finds nothing new is not reported as a failure, because that is the ordinary case.
- A picture a followed page has that is already somewhere in your library is listed in that page's folder instead of being left out of it, so the folder mirrors the page. The file is never stored twice.

## 0.8.2 - 2026-08-17

- Boards Pictogrep keeps up to date are listed under Official plugins, each one able to be dropped on its own. Turning the whole feature off was the only way to stop following a board, so one board you no longer wanted meant giving up the ones you did.
- Folders Pictogrep reads can be removed from Settings. Indexing only ever added folders to the list, so a folder chosen by mistake, or an enormous one chosen without meaning to, stayed for good and was rescanned forever. Removing one stops Pictogrep reading it. The folder and every picture in it are left exactly where they are.

## 0.8.1 - 2026-08-17

- The folder canvas is an optional plugin now, and off until you turn it on. It was an action on every folder menu for something most libraries never use. Turn it on under Official plugins.
- In Portuguese it is no longer called "Abrir tela", which reads as "open screen" rather than naming the workspace it opens.

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
