# AGENTS.md

Contributor + AI-agent guide for `gravity-cli`. This is the source of truth for
how the code is organized and the standard it's held to. For end-user command
usage see [README.md](README.md); this file is about building and extending the
tool.

## Purpose

`gravity` is a CI/pipeline companion for the Gravity docs platform. It generates
release notes from git history, checks API-doc and docs-completeness drift
against the checked-out repo, and authors doc blocks. It's a single static Go
binary designed to run inside CI (GitHub Actions, GitLab, Bitbucket) and signal
results through its exit code.

## Architecture

One-way layering — dependencies always point downward. **The capability and
foundation packages never import `internal/cli`.**

```
cmd/gravity            entrypoint: signal-aware context
  └─ internal/cli      cobra command tree, config resolution, exit codes (orchestration)
       ├─ internal/api      HTTP client: REST endpoints + LLM gateway (Messages subset)
       ├─ internal/agent    tool-using loop + sandboxed read-only git tools + submit tools
       ├─ internal/checks   OpenAPI operation diff + source-binding hash verification
       ├─ internal/docs     block authoring: OpenAPI→api blocks, Markdown→native blocks
       ├─ internal/git      thin wrapper over the system `git` binary (os/exec)
       ├─ internal/output   text / json / github findings formatters
       ├─ internal/config   flag/env/file precedence + typed .gravity.yaml manifest
       ├─ internal/prompts  system prompts for the release-notes/docs-gap/nucleus agents
       ├─ internal/version  the single version variable (ldflags stamp, else build info)
       └─ internal/pathsafe  repo-root path validation (leaf, stdlib-only)
```

`internal/cli` orchestrates: it resolves config into an `*api.Client` plus the
loaded `.gravity.yaml`, then each command's `RunE` consumes that. Capability
packages are independently testable and know nothing about cobra. `pathsafe` is
a stdlib-only leaf imported by `agent`, `checks`, `config`, and `docs` — keep it
dependency-free so it can never create an import cycle.

## Build / Test / Lint

```bash
make build   # build ./bin/gravity
make test    # go test ./...
make lint    # go tool golangci-lint run  (govet + staticcheck + the curated set)
make fmt     # go tool golangci-lint fmt  (gofumpt + goimports)
make ci      # lint + test + build  — what CI runs
```

The linter is pinned via the go.mod `tool` directive, so `make lint` uses the
same `golangci-lint` version as CI with no separate install. Config lives in
[`.golangci.yml`](.golangci.yml). CI runs on every push/PR via
`.github/workflows/ci.yml`.

## Releasing / distribution

The downloadable binaries behind the app's Download button are cut by
[GoReleaser](https://goreleaser.com) ([`.goreleaser.yaml`](.goreleaser.yaml)),
driven by `.github/workflows/release.yml` on any `v*` tag:

```bash
git tag v0.1.0 && git push origin v0.1.0   # → cross-compiled release
```

That builds static binaries for `{linux,darwin,windows} × {amd64,arm64}`,
uploads the archives + `checksums.txt` as GitHub Release assets, and publishes
the Homebrew cask (`Grupo-Impulso-Digital/homebrew-tap`) and Scoop manifest
(`Grupo-Impulso-Digital/scoop-bucket`). Version is stamped via
`-X github.com/Grupo-Impulso-Digital/gravity-cli/internal/version.Version={{.Version}}`,
the same symbol the Makefile sets; an unstamped `go install` build falls back to
`debug.ReadBuildInfo`. That one variable feeds `gravity version`, the ping
payload, generator stamps and the HTTP `User-Agent`.

- **Validate config changes** with `goreleaser check`, and dry-run the whole
  pipeline with `goreleaser release --snapshot --clean --skip=publish` (writes
  to `dist/`, gitignored) before tagging.
- **Two secrets** in repo settings: the default `GITHUB_TOKEN` creates the
  release; a `GORELEASER_TOKEN` PAT pushes the cask/manifest cross-repo into
  `Grupo-Impulso-Digital/homebrew-tap` + `…/scoop-bucket`. Without it binaries
  still ship — only the package-manager publish steps fail. Least-privilege PAT:
  a **fine-grained** token, resource owner `Grupo-Impulso-Digital`, scoped to
  just those two repos, with **Contents: read and write** (Metadata: read is
  added automatically). A classic PAT with the `repo` scope also works but grants
  far more than needed.
- **`install.sh`** (repo root) is the `curl … | sh` installer; it reconstructs
  the archive name from the release tag + `uname`, so keep it in lockstep with
  the archive `name_template` in `.goreleaser.yaml`.
- The binaries are **unsigned**. `curl`/terminal downloads aren't Gatekeeper-
  quarantined, and the Homebrew cask strips the quarantine xattr on install, so
  no notarization is wired up. Revisit only if a browser-download path is added.

## Coding standard

- **Code is comment-free.** Write no narrative or explanatory comments. Keep
  only what tooling requires: the one-line package doc, a one-line doc comment
  on exported symbols (`revive`'s `exported` rule), `//go:` directives, and
  `//nolint:x // reason`. Never edit string literals that merely look like
  comments (prompts, YAML templates).
- **Errors.** `errors.New` for static messages; `fmt.Errorf("…: %w", err)` to
  wrap an underlying error (preserve the chain — `errorlint` guards it). Error
  strings are lowercase and unpunctuated. No `panic` in non-test code.
- **Exit-code contract** (`internal/cli/exit.go`): `0` success/no findings,
  `1` findings produced, `2` operational error (auth/network/bad input),
  `3` license refusal. Commands return an `*ExitError`; never call `os.Exit`
  inside a command.
- **License refusals** (`internal/api/license.go`): a 403 whose envelope code is
  `module_disabled` (with `module`) or `seat_limit` decodes to
  `*api.ModuleDisabledError` / `*api.SeatLimitError`, whose message tells the
  user to ask an administrator. They are *not* `IsAuth()` (the token is fine) and
  `CodeFor` maps them to `3` from anywhere in the chain — so wrap with `%w`, never
  flatten one into a `Failf` string. The CLI never calls `/api/mcp`, so the
  JSON-RPC `MODULE_DISABLED` shape needs no handling here.
- **Token-security boundary** (`internal/config`): a token may come *only* from
  the `GRAVITY_TOKEN` env var or the user-level `~/.config/gravity/config.yaml`
  (mode `0600`). A `token:` committed to `.gravity.yaml` is a loud error, never
  honored. Don't add a code path that reads a token from the project file.
- **Config precedence**: flags > env > `./.gravity.yaml` >
  `~/.config/gravity/config.yaml`. Empty values never clobber a
  lower-precedence one.
- **Path safety**: any caller-supplied path that hits the filesystem or git goes
  through `internal/pathsafe` (rejects absolute paths and `..` escapes). Don't
  re-implement the check inline.
- **One file per cobra command** in `internal/cli` (`newXxxCmd`, registered in
  `root.go`).
- **Dependencies**: stdlib-first. The HTTP client and the Anthropic-style
  Messages protocol are hand-rolled on `net/http` — no SDKs. Don't add a module
  without a reason stated in the PR. The non-stdlib direct deps and their
  justifications: `cobra` (command tree), `libopenapi` (spec parsing),
  `goldmark` (Markdown parsing), `go.yaml.in/yaml/v3` (manifest), and
  **`charmbracelet/huh`** — the sanctioned interactive-prompt library, used only
  by `gravity init`'s wizard. Prefer `huh` over hand-rolled `bufio` prompting for
  any new interactive flow.
- **Testing**: stdlib `testing` + `httptest` mocks, table-driven where it fits.
  No live-server integration tests.
- **Feature availability comes from `/whoami` features only**
  (`env.gateFeature`): an unadvertised feature → notice + skip (exit `0`)
  unless `--require`. A 404 is never a missing feature: `explainNotFound` turns
  it into `site '<slug>' not found` / `space '<slug>' not found` with the
  available slugs (exit `2`). `(*APIError).IsUnavailable()` only honours the
  server's explicit `501` / `not_implemented` / `feature_disabled` /
  `unknown_route` answers.
- **CI mode** (`--ci`, or `CI=true`): never prompt, plain ASCII output
  (`plainWriter`). Don't add a command-local `--ci` flag; it shadows the global.
- **Manifest**: `.gravity.yaml` is decoded strictly (`config.ParseProject`); add a
  key by adding the typed field. Retire a key through `removedKeys` so loads fail
  clearly and `init --migrate` drops it. `init` renders the file from the typed
  `config.Project` (`renderManifest`), so every field round-trips.

## Recipes

- **Add a command**: new `internal/cli/<name>.go` with `newXxxCmd(...) *cobra.Command`;
  register it in `internal/cli/root.go`; resolve config/client via the existing
  `env` helpers; return `*ExitError` for failures.
- **Add a check**: put deterministic logic in `internal/checks` (pure, testable);
  surface findings through `internal/output`; mirror an existing `_test.go`
  (e.g. `binding_test.go`).
- **Add an API endpoint**: add the typed request/response in the matching
  `internal/api/*.go` file and a thin method on `Client` using `do`/`Get`/`Post`;
  decode the standard error envelope; mock it with `httptest` (see
  `rest_test.go`).

## Known limitations / deliberate decisions

- **`read_file` reads at the end of the range under review** (`--to`, else
  `HEAD`), not the working tree, so uncommitted-but-tracked edits aren't visible
  to that one tool. This keeps it deterministic in CI (clean checkout) and
  avoids reading untracked files; use `git_show` for other refs.
- **`check api --openapi` "changed" detection compares the `summary` field.**
  Param-level diffing is intentionally deferred — `params` is provider-defined
  (`json.RawMessage`) and a naive structural diff would be noisy.
- **No live integration tests** — server interactions are exercised via
  `httptest` mocks.
- **The agent loop uses `tool_choice: auto` until the last turn**, then forces
  the terminal submit tool (`tool_choice: {type: tool}`); it also forces it once
  after the model ends a turn without submitting and after the tool-call budget
  runs out. Only if even the forced turn fails does the command report it could
  not obtain a structured result (exit `2`).
- **`gosec` is intentionally not enabled yet** — the git `exec.Command` and the
  computed-path `os.ReadFile` sites are already sandboxed; revisit with targeted
  excludes before turning it on.
- **`charmbracelet/x/cellbuf` is pinned to `v0.0.15`** in `go.mod`. The
  `golangci-lint` `tool` directive pulls in a newer `charmbracelet/x/ansi` than
  `huh`'s `bubbletea`/`cellbuf` stack selects on its own, and the older `cellbuf`
  is incompatible with that `ansi` API. The pin is the minimum `cellbuf` that
  compiles against both; `go mod tidy` preserves it. Don't lower it.

## Releasing

- Releases are cut by `.github/workflows/auto-release.yml`. On every push to
  `main` it reads the first `## vX.Y.Z` heading of `CHANGELOG.md`; when that
  heading carries a date instead of `Unreleased` and the tag does not exist, it
  tags the commit and runs `release.yml` (GoReleaser, then the `v<major>` tag
  the GitHub action is pinned to). To ship, date the top section in the PR.
- Pushing a `v*` tag by hand still runs `release.yml` directly.
- A Homebrew tap or Scoop bucket failure (the `GORELEASER_TOKEN` PAT needs
  contents:write on `homebrew-tap` and `scoop-bucket`) leaves a warning on the
  run; the GitHub release and the major tag still ship.

## Pointers

- [README.md](README.md) — install + per-command usage + exit codes.
- [docs/platform-authoring-api.md](docs/platform-authoring-api.md) — the platform
  authoring API contract.
- [ci/](ci/) — composite Action / pipeline snippets for *downstream consumers*
  of the CLI, plus the CI cadence contract ([ci/README.md](ci/README.md)). **Not**
  this repo's own build CI (that's `.github/workflows/ci.yml`);
  `.github/workflows/docs.yml` does dogfood the composite action from `ci/github`.
