# AGENTS.md

Contributor + AI-agent guide for `gravity-cli`. This is the source of truth for
how the code is organized and the standard it's held to. For end-user command
usage see [README.md](README.md); this file is about building and extending the
tool.

## Purpose

`gravity` connects a repository to the Gravity docs platform and runs its
documentation **passes** in CI. Code facts live in the repository
(`.gravity.yaml`, manifest v2); editorial intent (targets, instructions,
review) lives in the app, and the CLI loads the effective configuration from
the app on every run. It is a single static Go binary that signals results
through its exit code. The authoritative design is the platform repository's
`docs/pipelines/spec.md` (CLI milestones C1-C4); CLI-side deviations are
recorded with each milestone.

## Architecture

One-way layering: dependencies point downward. **No package imports
`internal/cli`.**

```
cmd/gravity              entrypoint: signal-aware context
  └─ internal/cli        cobra command tree, global flags, exit codes, report publishing (orchestration)
       ├─ internal/ui         TTY / plain CI / --json envelope output, huh prompts, bubbletea progress, lipgloss cards
       ├─ internal/auth       profiles.yaml, credential precedence, device login
       ├─ internal/plan       plan fetch, manifest overlay, skip decisions
       ├─ internal/run        run orchestration: plan/lease loop, heartbeat, ingest (roles, documents), handoffs, passes, finish
       │    └─ internal/passes  Pass interface + guides, reference, verbatim, changelog, nucleus, check, capture;
       │         │              Ownership (product-wide units, roles, claim verdicts), unit reach, provenance
       │         ├─ internal/verbatim  Markdown/MDX -> native blocks, file -> page mapping, link rewriting
       │         └─ internal/agent     one harness: forced submit, token budget, git + doc tools, submit tools
       ├─ internal/report     doc-impact comment and run summary; comment upsert on GitHub, GitLab, Bitbucket, Azure;
       │                      GitHub/Azure annotations, GitLab Code Quality; page diffs
       ├─ internal/prompts    hosted pass prompts with baked fallbacks
       ├─ internal/changeset  range resolution, ChangeSet, OpenAPI diff, symbols, unit mapping
       ├─ internal/setup      init suggestions: product ranking, target site, pass templates, spaces to create
       │    └─ internal/detect  local repository detection (languages, specs, routes, docs, releases, CI files)
       ├─ internal/cisetup    CI file templates per provider (also published under ci/), gh/glab secret installers
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

internal/distribution    tests only: install.sh (major resolution, checksum), the GitHub action, ci/ templates, workflows
ci/                      the GitHub action (ci/github/action.yml) and the CI templates init writes, per provider
install.sh, install.ps1  release installers (also mirrored by the app's /install.sh route)
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
`.github/workflows/ci.yml`, which is self-contained on purpose: this repository
is public and cannot call the organization's private reusable `go-ci.yml`, so
it mirrors that contract directly.

## Releasing / distribution

The binaries are cut by [GoReleaser](https://goreleaser.com)
([`.goreleaser.yaml`](.goreleaser.yaml)), driven by
`.github/workflows/release.yml` on any `v*` tag. It builds static binaries for
`{linux,darwin,windows} × {amd64,arm64}`, uploads the archives and
`checksums.txt`, publishes the Homebrew cask (`Grupo-Impulso-Digital/homebrew-tap`)
and the Scoop manifest (`Grupo-Impulso-Digital/scoop-bucket`), then moves the
major tag (`v1`) that `uses: …/ci/github@v1` resolves. The version is stamped
via `-X …/internal/version.Version={{.Version}}` (plus `Commit` and `Date`),
the same symbols the Makefile sets; an unstamped `go install` build falls back
to `debug.ReadBuildInfo`.

- Releases are cut by `.github/workflows/auto-release.yml`. On every push to
  `main` it reads the first `## vX.Y.Z` heading of `CHANGELOG.md`; when that
  heading carries a date instead of `Unreleased` and the tag does not exist, it
  tags the commit and runs `release.yml` (GoReleaser, then the `v<major>` tag
  the GitHub action is pinned to). To ship, date the top section in the PR.
- Pushing a `v*` tag by hand still runs `release.yml` directly.
- A Homebrew tap or Scoop bucket failure (the `GORELEASER_TOKEN` PAT needs
  contents:write on `homebrew-tap` and `scoop-bucket`) leaves a warning on the
  run; the GitHub release and the major tag still ship.
- **1.0 release gate.** A `v1.*` tag fails its `gate` job unless a `v0.x`
  release exists whose action and `install.sh` default to major `0` (milestone
  C0.1): without it every v0.3 pipeline would install 1.0 and break. The 1.0
  release notes header tells v0.3 users how to stay on 0.x or migrate.
- **Majors are pinned everywhere.** `install.sh`/`install.ps1` default to
  `GRAVITY_VERSION=1` and resolve `<major>` to the newest `v<major>.x.y`
  release (pre-releases excluded); the action's `version` input defaults to
  `1`; the CI templates install `GRAVITY_VERSION=1`. Bump all of them together
  for a new major, and keep the app's `/install.sh` route byte-identical to
  `install.sh`.
- **Validate** with `goreleaser check`; dry-run with
  `goreleaser release --snapshot --clean --skip=publish` (writes `dist/`).
- **Secrets**: the default `GITHUB_TOKEN` creates the release and moves the
  major tag; a fine-grained `GORELEASER_TOKEN` (Contents read/write on the tap
  and bucket repositories only) publishes the cask and manifest. Without it the
  binaries still ship.
- `install.sh` reconstructs the archive name from the tag and `uname`; keep it
  in lockstep with the archive `name_template`.
- The binaries are unsigned; the cask strips the quarantine attribute.

## Coding standard

- **Code is comment-free** (the owner's rule; it replaces the old "comment the
  why" guidance). Write no narrative or explanatory comments, no doc prose
  beyond what tools demand, and no arrange/act/assert markers in tests. Keep
  only what tooling requires: the one-line package doc, a one-line doc comment
  on exported symbols (`revive`'s `exported` rule), `//go:` directives, and
  `//nolint:x // reason`. The same holds for `install.sh`, `install.ps1`,
  `.goreleaser.yaml`, the workflows and the `ci/` files: the shebang is the only
  comment they need. Explanations belong in commit messages, README,
  `ci/README.md` and this file. Never edit string or template literals that
  merely look like comments (prompts, YAML templates).
- **Errors.** `errors.New` for static messages; `fmt.Errorf("…: %w", err)` to
  wrap an underlying error (preserve the chain — `errorlint` guards it). Error
  strings are lowercase and unpunctuated. No `panic` in non-test code.
- **Exit-code contract** (`internal/cli/exit.go`): `0` success/no findings,
  `1` findings produced, `2` operational error (network/bad input),
  `3` license refusal, `4` no usable credentials (`token_missing`,
  `token_unresolved`, any `401`); precedence 3 > 2 > 1 > 0, and `4` ends a
  command before it does anything else. Only a fork pull request detected by
  `internal/ci` (`Context.Fork`) may skip without a token. Commands return an
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
- **Dependencies**: stdlib-first. The HTTP clients (platform, LLM gateway,
  GitHub, GitLab, Bitbucket and Azure comment APIs) and the Messages protocol
  are hand-rolled on `net/http`; no provider SDK is worth its tree for four
  endpoints each. Every direct dependency has one job:
  - `spf13/cobra`: the command tree, flags and help.
  - `pb33f/libopenapi`: OpenAPI 3.x/Swagger parsing and the deterministic
    OpenAPI diff of the ChangeSet.
  - `yuin/goldmark`: Markdown parsing for the verbatim converter (GFM,
    footnotes, definition lists).
  - `go.yaml.in/yaml/v3`: the manifest, CI files and the action under test.
  - `santhosh-tekuri/jsonschema/v6`: validating the manifest against the
    embedded schema, as the spec requires.
  - `charmbracelet/huh`: init's prompts, with an accessible line mode that also
    drives the scripted tests.
  - `charmbracelet/bubbletea` + `charmbracelet/bubbles`: the live per-step
    progress of `init`, `login`, `run`, `preview`, `check` (bubbles only for its
    spinner).
  - `charmbracelet/lipgloss` + `muesli/termenv`: the summary card; termenv to
    force an ASCII profile with `--no-color`.

  The terminal libraries are used only on a terminal; `--json`, `CI=true` and
  non-terminals never start them. They stay on the v1 lines already in the
  module cache (huh v1.0.0, bubbletea v1.3.10, lipgloss v1.1.0), and
  `charmbracelet/x/cellbuf` is pinned to `v0.0.15` because the version huh's
  stack selects is incompatible with the newer `x/ansi` golangci-lint pulls
  in. `golangci-lint` itself is a go.mod `tool`, not a build dependency. A new
  dependency needs the same one-line justification here, in the same commit.
- **Testing**: stdlib `testing` + `httptest` mocks, table-driven where it fits.
  No live-server integration tests.
- **Capabilities, not 404s**: server features come from `/whoami` and
  `plan.capabilities` (connect's `serverFeatures` is decoded, not consulted). CLI 1.0 refuses a server
  without `pipelines`. A `404` is always an error (`repo_not_connected` hints
  `gravity init`), never "feature unavailable".
- **Retries** (`internal/api`): `429` and `5xx` get up to three attempts with
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
- **Change a CI template**: edit it in `internal/cisetup`, then
  `go test ./internal/cisetup -update` rewrites the init goldens and the
  published copies under `ci/`; `internal/distribution` checks that every
  template installs major `1`, runs `gravity run` and never uses
  `pull_request_target`.
- **Add a CI provider's comments or annotations**: a `report.Commenter`
  (`Upsert` by `report.Marker`, editing only its own comment) tested against an
  `httptest` API in `internal/report/providers_test.go`, then wire its token in
  `internal/cli/publish.go` (`commentTarget`).
- **Change ownership rules**: `passes.Ownership` (`Settle` for claim verdicts,
  `Others`, `Role`) is the one place that reads contributors; guides' unit
  reach is `internal/passes/reach.go`; expected handoffs and the `documents`
  role are `internal/run/handoffs.go`. Cover rule changes in the table test of
  `internal/passes/ownership_test.go`.
- **Change the installer or the action**: `internal/distribution` runs
  `install.sh` against a fake `curl` and runs the action's `gravity` step with a
  fake binary; keep both green.

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
- **Pull request comments need the provider's token** (`GITHUB_TOKEN`,
  `GITLAB_TOKEN`, `BITBUCKET_ACCESS_TOKEN`, `SYSTEM_ACCESSTOKEN`); without it
  the report goes to `gravity-report.md`. Jenkins and CircleCI only get the
  file. A comment is created only once a pull request has had impact; after
  that it is updated, down to "No documentation impact".
- **Unit reach is update-only and block-bound**: guides change a page outside
  their target only through existing blocks bound to a touched unit this
  repository declares or implements; the server re-checks reach and a refusal
  is a warning, not a failure.
- **Handoffs in pull requests are predictions** from API units added or removed
  in the ChangeSet; write runs report what the platform detected on ingest.
- **The dogfood `.github/workflows/docs.yml` builds the CLI from source**
  (`version: source`) so pull requests exercise their own code; it needs a 1.0
  repository token in `GRAVITY_TOKEN`.
- **`read_file` reads at the end of the range under review** (`--to`, else
  `HEAD`), not the working tree, so the agent stays deterministic in CI.
- **`gravity run` never reads the working tree.** Writes cite commits and
  watermarks advance to commits, so a run covers committed history only and
  warns about uncommitted changes; `gravity preview` is the working-tree view.
- **Manual runs and `--pass`.** Trigger matching is the server's
  (`evaluateSkip`): a write-mode manual run runs passes triggered on `manual`,
  `push` or `schedule`, and a pass named in the plan's `pass=` runs whatever
  its triggers (`POST /runs` recovers the selection from the `not_selected`
  skips the CLI sends). `plan.TriggerMatches` mirrors the rule for local
  overlays. The CLI fails a manual run whose named pass the plan still skips
  for `disabled`, `trigger_mismatch` or `branch_mismatch` (`pass_not_applicable`,
  older servers) instead of writing nothing; an unknown `--pass` is
  `pass_unknown`.
- **Check drift has a baseline.** Drift compares api blocks with the range's
  base and head: a change a push-triggered reference pass will apply is a note,
  drift that predates the range is a warning, and only a change nothing will
  follow is an error.
- **`init` and 0.x pipelines.** `cisetup.LegacyPipelines` finds CI files that
  still run gravity 0.x; init then keeps `GRAVITY_TOKEN` (0.x refuses
  repository tokens) unless `--replace-secret`.
- **No live integration tests**: server interactions use `httptest` mocks.
- **`gosec` is intentionally not enabled yet**; the git `exec.Command` and
  computed-path reads are sandboxed.

## Pointers

- [README.md](README.md): install, commands, flags, exit codes.
- The platform's `docs/pipelines/spec.md`: the REST contract, manifest schema
  and CLI milestones.
- [ci/](ci/): the GitHub action and the CI templates, with
  [ci/README.md](ci/README.md) for consumers.
