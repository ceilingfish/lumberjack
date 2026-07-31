# Changelog

## [0.3.0](https://github.com/ceilingfish/lumberjack/compare/v0.2.1...v0.3.0) (2026-07-31)


### Features

* bound each git invocation and sync three repositories at once ([#75](https://github.com/ceilingfish/lumberjack/issues/75)) ([857d715](https://github.com/ceilingfish/lumberjack/commit/857d715d8593e68f4444b4869ca0cd0e1f21a862))
* raise the per-package coverage floor to 95% ([#80](https://github.com/ceilingfish/lumberjack/issues/80)) ([2c05b09](https://github.com/ceilingfish/lumberjack/commit/2c05b09d6df88d996ef933742d7adab501027164)), closes [#6](https://github.com/ceilingfish/lumberjack/issues/6)


### Bug Fixes

* **sync:** prune ghost worktrees with neither a PR nor a directory ([#81](https://github.com/ceilingfish/lumberjack/issues/81)) ([da66ccf](https://github.com/ceilingfish/lumberjack/commit/da66ccf47e8b97b884d7bbb9ece866b7a3851b21))
* **worktree:** update Reconcile test calls for the prBranch parameter ([#71](https://github.com/ceilingfish/lumberjack/issues/71)) ([0b47c89](https://github.com/ceilingfish/lumberjack/commit/0b47c89c3b2fbc4bafdcc08167b12c79ad14801c))

## [0.1.1](https://github.com/ceilingfish/lumberjack/compare/v0.1.0...v0.1.1) (2026-07-31)

## [0.2.1](https://github.com/ceilingfish/lumberjack/compare/v0.2.0...v0.2.1) (2026-07-31)

### Bug Fixes

- read the root package's release-please outputs unprefixed ([#56](https://github.com/ceilingfish/lumberjack/issues/56)) ([2d6064d](https://github.com/ceilingfish/lumberjack/commit/2d6064d1235ffdfddfc4496244f3000f0f4882a3))

## [0.2.0](https://github.com/ceilingfish/lumberjack/compare/v0.1.0...v0.2.0) (2026-07-31)

### Features

- enforce per-package coverage floors with a committed exclusion list ([#51](https://github.com/ceilingfish/lumberjack/issues/51)) ([6c3a1f9](https://github.com/ceilingfish/lumberjack/commit/6c3a1f9f2becfd9c9bf0ecd4d079bc2993f42284))

### Bug Fixes

- run the macOS jobs on macos-15 for a Swift 6 toolchain ([#53](https://github.com/ceilingfish/lumberjack/issues/53)) ([6271d45](https://github.com/ceilingfish/lumberjack/commit/6271d45d668b5c8f21d3664b5cc4a93b378ca427))
