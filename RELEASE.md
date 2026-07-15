# Release Process

This document describes how TPT DataCenter is versioned and released.

## Versioning

- We follow [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).
- Pre-1.0 (`0.x.y`): the public API is unstable; MINOR bumps may break APIs.
- Post-1.0: `MAJOR` = breaking change, `MINOR` = backwards-compatible
  feature, `PATCH` = backwards-compatible bug fix.

## Version sources

- Rust (root workspace): versions live in `[workspace.package]` in
  `Cargo.toml` and are inherited by members (`version.workspace = true`).
- Go (`go-telemetry`): version is implicit; tag releases with `vX.Y.Z`.
- Independent crate publication (`ai-brain` → crates.io) keeps its own
  `Cargo.toml` version, coordinated with the workspace during pre-1.0.

## Changelog

- Maintain [`CHANGELOG.md`](CHANGELOG.md) in
  [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.
- Add entries under `## [Unreleased]` as work lands; move them under a new
  version heading at release time.
- Categorize under `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`,
  `Security`.

## Cutting a release

1. Ensure `main` is green in CI.
2. Update `CHANGELOG.md`: move `[Unreleased]` content under `## [X.Y.Z]`.
3. Bump versions (workspace + `go.mod` as applicable) and commit
   (`chore: release vX.Y.Z`).
4. Tag: `git tag vX.Y.Z && git push origin vX.Y.Z`.
5. CI publishes tagged artifacts (crates.io dry-run for `ai-brain` until
   stable; Docker/Helm images for services once they exist).
6. Open a GitHub Release referencing the changelog section.
