# Changelog

## Unreleased

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
