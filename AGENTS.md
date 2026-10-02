# AGENTS.md

Contributor + AI-agent guide for `gravity-cli`. This is the source of truth for
how the code is organized and the standard it's held to. For end-user command
usage see [README.md](README.md); this file is about building and extending the
tool.

## Purpose

`gravity` connects a repository to the Gravity docs platform and runs its
documentation **passes** in CI. Code facts live in the repository
(`.gravity.yaml`, manifest v2); editorial intent (targets, instructions,
review) lives in the app. It is a single static Go binary that signals results
through its exit code. The authoritative design is the platform repository's
`docs/pipelines/spec.md` (CLI milestones C1-C4).

## Architecture

One-way layering: dependencies point downward. **No package imports
`internal/cli`.**

```
cmd/gravity              entrypoint: signal-aware context
  └─ internal/cli        cobra command tree, global flags, exit codes (orchestration)
       ├─ internal/ui         TTY / plain CI / --json envelope output, huh prompts, bubbletea progress, lipgloss cards
       ├─ internal/auth       profiles.yaml, credential precedence, device login
       ├─ internal/plan       plan fetch, manifest overlay, skip decisions
       ├─ internal/run        run orchestration: plan/lease loop, heartbeat, ingest, passes, finish
       │    └─ internal/passes  Pass interface + guides, reference, verbatim, changelog, nucleus, check, capture
       │         ├─ internal/verbatim  Markdown/MDX -> native blocks, file -> page mapping, link rewriting
       │         └─ internal/agent     one harness: forced submit, token budget, git + doc tools, submit tools
       ├─ internal/report     PR comment, step summary, annotations, page diffs
       ├─ internal/prompts    hosted pass prompts with baked fallbacks
       ├─ internal/changeset  range resolution, ChangeSet, OpenAPI diff, symbols, unit mapping
       ├─ internal/setup      init suggestions: product ranking, target site, pass templates, spaces to create
       │    └─ internal/detect  local repository detection (languages, specs, routes, docs, releases, CI files)
       ├─ internal/cisetup    CI file templates per provider, gh/glab secret installers (token on stdin)
       ├─ internal/ci         CI provider detection
       ├─ internal/api        REST client for the CLI 1.0 contract + LLM gateway
       ├─ internal/docs       OpenAPI -> api blocks
       ├─ internal/checks     OpenAPI parsing
       ├─ internal/config     manifest v2 (embedded JSON Schema, strict parse, did-you-mean), v1 detection + conversion, token scopes
       ├─ internal/normalize  productSlug, apiUnitKey, canonical JSON (golden fixtures shared with the platform)
       ├─ internal/git        thin wrapper over the system `git` binary
       ├─ internal/glob       doublestar matching (leaf)
       ├─ internal/version    the single version variable (ldflags stamp, else build info)
       └─ internal/pathsafe   repo-root path validation (leaf, stdlib-only)
```

`internal/cli` resolves credentials, the manifest and the repository, then each
command's `RunE` calls the capability packages, which know nothing about cobra.
`pathsafe` and `glob` are stdlib-only leaves.

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
  `3` license refusal; precedence 3 > 2 > 1 > 0. Commands return an
  `*ExitError` (with an `ErrCode` for the `--json` envelope); never call
  `os.Exit` inside a command.
- **License refusals** (`internal/api/license.go`): a 403 whose envelope code is
  `module_disabled` (with `module`) or `seat_limit` decodes to
  `*api.ModuleDisabledError` / `*api.SeatLimitError`, whose message tells the
  user to ask an administrator. They are *not* `IsAuth()` (the token is fine) and
  `CodeFor` maps them to `3` from anywhere in the chain — so wrap with `%w`, never
  flatten one into a `Failf` string. The CLI never calls `/api/mcp`, so the
  JSON-RPC `MODULE_DISABLED` shape needs no handling here.
- **Token-security boundary** (`internal/auth`): a token comes only from
  `--token`, `GRAVITY_TOKEN` or a profile in `~/.config/gravity/profiles.yaml`
  (mode `0600`). A `token:` anywhere in `.gravity.yaml` is a manifest error.
  CLI 1.x never writes the v0.x `config.yaml`; it copies its token into the
  profile `default` once and leaves the file untouched.
- **Precedence**: token `--token` > `GRAVITY_TOKEN` > profile (`--profile` >
  `GRAVITY_PROFILE` > current). API URL `--api-url` > `GRAVITY_API_URL` >
  manifest `apiUrl` > profile `apiUrl` > default. `auth.Resolve` refuses a
  profile token whose issuing host differs from the resolved API URL
  (`HostMismatchError`), so a cloned repository's `apiUrl` never receives a
  stored token.
- **Path safety**: any caller-supplied path that hits the filesystem or git goes
  through `internal/pathsafe` (rejects absolute paths and `..` escapes). Don't
  re-implement the check inline.
- **One file per cobra command** in `internal/cli` (`newXxxCmd`, registered in
  `root.go`).
- **Dependencies**: stdlib-first. The HTTP client and the Messages protocol are
  hand-rolled on `net/http`. Direct deps and their reasons: `cobra` (command
  tree), `libopenapi` (spec parsing and the OpenAPI diff), `goldmark` (Markdown),
  `go.yaml.in/yaml/v3` (manifest), `santhosh-tekuri/jsonschema/v6` (validating
  the manifest against the embedded schema, as the spec requires), and the
  terminal UI the spec names (§11.1): `charmbracelet/huh` (init's prompts,
  with an accessible line mode that also drives the scripted tests),
  `charmbracelet/bubbletea` + `charmbracelet/bubbles` (the live per-step
  progress of `init`, `login`, `run`, `preview`, `check`; bubbles only for its
  spinner), `charmbracelet/lipgloss` + `muesli/termenv` (the summary card;
  termenv to force an ASCII profile with `--no-color`). They are used only on a
  terminal; `--json`, `CI=true` and non-terminals never start them. The UI
  stack stays on the v1 lines already in the module cache (huh v1.0.0,
  bubbletea v1.3.10, lipgloss v1.1.0), and `charmbracelet/x/cellbuf` is pinned
  to `v0.0.15`: the version huh's stack selects is incompatible with the newer
  `x/ansi` golangci-lint pulls in (see the v0.3 history).
- **Testing**: stdlib `testing` + `httptest` mocks, table-driven where it fits.
  No live-server integration tests.
- **Capabilities, not 404s**: server features come from `/whoami` (and
  `connect.serverFeatures` / `plan.capabilities`). CLI 1.0 refuses a server
  without `pipelines`. A `404` is always an error (`repo_not_connected` hints
  `gravity init`), never "feature unavailable".
- **Retries** (`internal/api`): `429` and `5xx` are retried three times with
  exponential backoff from 1 s, honoring `Retry-After`; network errors are
  retried for GET only. `lease_lost` and `run_not_running` stop a run without a
  finish call (`api.StopsRun`).
- **Output** (`internal/ui`): `--json` writes exactly one envelope on stdout and
  all human output on stderr. With `CI=true` or without a terminal, output is
  plain ASCII and nothing prompts. There is no global `--ci` flag in 1.x
  (`init --ci <provider>` picks the CI file to write). Prompts go through
  `ui.Prompter` (huh; `ACCESSIBLE=1` or `TERM=dumb` selects its line mode);
  long work goes through `ui.Progress`, which on a terminal redirects the
  printer's output above its live view until `Stop`; summaries go through
  `Printer.Card`.
- **Init** (`internal/cli/init.go`, `initflow.go`): at most three questions
  (product, passes with the site change inside the passes question, write);
  the site sub-list and the re-asked passes question do not count. Without a
  terminal `init` needs `--yes` (or `--dry-run`). Nothing is written before
  the real connect succeeds; the repository token is printed only on stderr
  and only when it was not installed; secret installers get it on stdin.
- **Manifest** (`internal/config`): YAML is decoded to a generic tree, checked
  for tokens and v1 shape, validated by friendly Go rules and the embedded JSON
  Schema (byte-identical to the spec's, pinned by sha256 in
  `manifest_test.go`), then decoded strictly into the typed `Manifest`. Add a
  key in the spec's schema first, then the typed field. The manifest hash is
  sha256 of its RFC 8785 canonical JSON.

## Recipes

- **Add a command**: new `internal/cli/<name>.go` with `newXxxCmd(a *app) *cobra.Command`;
  register it in `internal/cli/root.go`; print through `a.ui`, return data with
  `a.ui.Result(data)` for `--json`, and failures as `*ExitError`. Test it with
  the fake platform in `internal/cli/harness_test.go`.
- **Change init**: script it with the harness's accessible prompter
  (`h.terminal = true`, `h.stdin` holds one answer per line, `0` confirms a
  multi-select) and assert the prompt titles in `h.prompts`; fake `gh`/`glab`
  go on `PATH` with `h.secrets = cisetup.ExecRunner`. CI templates and v1
  conversions are golden files (`go test ./internal/cisetup -update`,
  `go test ./internal/config -update`).
- **Add an API endpoint**: typed request/response next to its siblings in
  `internal/api` and a thin method on `Client`; add an `httptest` case to
  `endpoints_test.go` built from the spec's JSON example.
- **Change range or ChangeSet logic**: cover it with a scripted repository in
  `internal/changeset` (`newScripted`), never a fixture checkout.
- **Add or change a pass kind**: implement `passes.Pass` in
  `internal/passes/<kind>.go`, register it in `passes.For`, write only through
  the `Sink`, and test it with the in-process fakes of
  `internal/passes/fake_test.go` on a scripted repository. Engine behaviour
  (lease, heartbeat, finish, exit codes) is tested against the httptest
  platform in `internal/run/platform_test.go`.
- **Change the Markdown converter**: update `internal/verbatim` and regenerate
  the golden with `go test ./internal/verbatim -update`; review the diff.

## Known limitations / deliberate decisions

- **Init writes CI files only when they do not exist**: an existing workflow,
  `bitbucket-pipelines.yml` or a `.gitlab-ci.yml` with its own `include:` list
  is kept and a snippet is printed instead. Jenkins and CircleCI always get a
  snippet.
- **The CLI never embeds instruction layers.** Every gateway call sends only
  the kind prompt (`internal/prompts`) and `context { runId, runPassId,
  purpose }`; the gateway composes the org/site/space/collection/pass/note
  layers server-side.
- **Writes only through a `passes.Sink`**: `PlatformSink` in write runs,
  `Recorder` in dry runs (PRs, `preview`, `--dry-run`), which records the
  would-be requests into the pass report's `impact` and the preview diffs.
- **Unit keys are filtered client-side** (`passes.Units`: plan inventory plus
  what this run ingested) before `/changes`, so a write never fails on a key
  the product does not know yet.
- **PR comments are GitHub-only in C2**; other providers get
  `gravity-report.md` until C4.
- **`ci/` templates and `.github/workflows/docs.yml` still target v0.x**; they
  are rewritten with the 1.0 action (milestone C4) Until then the dogfood
  `docs.yml` runs on `workflow_dispatch` only, so pushes and PRs stay green.
- **`read_file` reads at the end of the range under review** (`--to`, else
  `HEAD`), not the working tree, so the agent stays deterministic in CI.
- **No live integration tests**: server interactions use `httptest` mocks.
- **`gosec` is intentionally not enabled yet**; the git `exec.Command` and
  computed-path reads are sandboxed.

## Pointers

- [README.md](README.md): install, commands, flags, exit codes.
- The platform's `docs/pipelines/spec.md`: the REST contract, manifest schema
  and CLI milestones.
- [ci/](ci/): v0.x pipeline snippets for downstream consumers (rewritten in C4).
