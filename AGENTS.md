# Pictogrep development workflow

## Integration

- `main` moves continuously. Commit and merge completed work whenever useful; multiple integrations per day are expected.
- Push each completed feature or fix to GitHub when it lands; do not wait for release day.
- Do not hold small fixes for a release branch or give each small fix its own release.

## Releases

- Make one scheduled release per week, normally on Sunday.
- The weekly release bundles everything that landed since the previous release and advances the normal version (`v0.4.0`, `v0.5.0`, and so on).
- Release immediately for a critical production bug, especially startup failures, data loss, or data corruption. A critical hotfix does not wait for Sunday.
- Tiny fixes ride with the next weekly release.

## Changelog

- Maintain `CHANGELOG.md` with an `Unreleased` section.
- Add user-visible changes to `Unreleased` as they land.
- At release time, move those entries under the released version and start a fresh `Unreleased` section.
