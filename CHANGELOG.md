# Changelog

## Unreleased

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
