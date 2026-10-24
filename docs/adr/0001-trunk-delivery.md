# ADR-0001: Trunk delivery and releases from a workflow

**Status:** Accepted (2026-10-24) · **Follows:** Gravity ADR-0001, _Trunk delivery and promotion by
tag_ (`Grupo-Impulso-Digital/gravity`, `docs/adr/0001-trunk-delivery.md`), which adopts origin
ADR-0043

## Context

The CLI released from `main`: every push to `main` ran `auto-release.yml`, which tagged the commit
when the top `CHANGELOG.md` heading carried a date, and any hand-pushed `v*` tag ran GoReleaser.
Merging a pull request could therefore release, nothing checked that the tagged commit had passed
CI, and there was no way to try a fix before every `GRAVITY_VERSION=1`, `ci/github@v1`, Homebrew
and Scoop user received it. Gravity now delivers from a trunk and releases only from a workflow run;
this record applies the same model to the CLI and keeps only what is specific to it.

## Decision

1. **`develop` is the trunk and default branch.** Every change lands by pull request (squash or
   rebase, linear history). `hotfix/vX.Y` branches patch the newest released line and are protected
   like `develop`. `main` is frozen (infra `gravity_cli_frozen_branches` ruleset); nothing merges
   into it again.
2. **One required check, `ci-ok`.** `ci.yml` runs on every pull request to and every push of
   `develop` and `hotfix/**`: gofmt, `go vet`, golangci-lint, `go test -race -cover`,
   `goreleaser check` and a GoReleaser snapshot that cross-builds the six archives, the checksums,
   the cask and the Scoop manifest. `ci-ok` aggregates them.
3. **Releases come from `release.yml`, never from a hand-pushed tag** (owner rule).
   `gh workflow run release.yml -f bump=patch|minor|major [-f prerelease=true] [-f commit=<sha>]`
   runs on `develop` (or `hotfix/vX.Y`, patch only, newest line only) and, before tagging, checks
   that the actor and any re-runner are in `PRODUCTION_APPROVERS`, that the commit (by default the
   newest commit of the branch whose `ci-ok` passed) is on the branch, has a green `ci-ok`, is not
   released yet and is newer than the last release, and, for a stable version, that `CHANGELOG.md`
   has its `## vX.Y.Z` section. It then tags the next version (`internal/release`: the newest
   `vX.Y.Z` bumped, `v1.0.0` for the first, or that version's next `-rc.N`) with `GITHUB_TOKEN`,
   publishes it with GoReleaser from that tag, moves the `v<major>` tag of the action for a stable
   version, and dispatches `docs.yml` on a stable tag (`GRAVITY_TRIGGER=release`). A tag pushed with
   `GITHUB_TOKEN` triggers no workflow, so nothing publishes twice; a hand-pushed tag publishes
   nothing at all.
4. **Pre-releases are opt-in.** `prerelease=true` cuts `vX.Y.Z-rc.N`, a GitHub pre-release that is
   never marked latest. `install.sh` and `install.ps1` resolve a major and `latest` to stable
   `vX.Y.Z` releases only, so the action (`version: 1`) and the CI templates never pick an RC; the
   cask and the Scoop manifest skip it (`skip_upload: auto`). An RC installs only by its exact
   version: `GRAVITY_VERSION=v1.2.0-rc.1`, or `version: v1.2.0-rc.1` in the action.
5. **Build once is not literally possible; release builds are reproducible.** The version is
   stamped into the binary at link time, and OSS GoReleaser cannot build in CI and publish those
   bytes later under a version that did not exist yet (the split `--prepare`/`publish` flow is
   GoReleaser Pro). The release therefore rebuilds from the exact commit whose `ci-ok` passed (the
   tag is checked to resolve to it), with the toolchain pinned by `go.mod`, `CGO_ENABLED=0`,
   `-trimpath`, fixed ldflags whose date is the commit date, `mod_timestamp` and archive entries
   stamped with the commit time and owned by `root`. Two builds of one commit and version produce
   identical archives and `checksums.txt`. An RC and the stable release of the same commit differ
   only in the stamped version.

## Consequences

- Merging never releases. A release is one workflow run by an approver, on a commit CI already
  passed, and the commit, version and previous release appear in the run summary.
- A fix can ship to volunteers first: cut an RC, install it by its exact version, then release the
  same commit stable with the same bump.
- Retry a failed publish with "Re-run failed jobs": a full re-run refuses because the commit is
  already tagged. Release tags are immutable (org `org-release-tags` ruleset); there is no
  `promotion-tags` ruleset because GitHub refuses GitHub Actions as a ruleset bypass actor.
- Anyone with write access can still push a `v*` tag; it publishes nothing and installers ignore a
  tag without a release.
- `CHANGELOG.md` no longer drives releases: keep the next version's section at the top
  (`## vX.Y.Z — Unreleased` is fine), and the release run refuses a stable version without one.
- `docs.yml` runs `gravity run` on pushes to `develop` and on stable tags; the platform must list
  `develop` as the repository's default branch for those runs to write.
