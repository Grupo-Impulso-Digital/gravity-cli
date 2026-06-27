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
cmd/gravity            entrypoint: signal-aware context + version wiring
  └─ internal/cli      cobra command tree, config resolution, exit codes (orchestration)
       ├─ internal/api      HTTP client: REST endpoints + LLM gateway (Messages subset)
       ├─ internal/agent    tool-using loop + sandboxed read-only git tools + submit tools
       ├─ internal/checks   OpenAPI operation diff + source-binding hash verification
       ├─ internal/docs     block authoring: OpenAPI→api blocks, Markdown→native blocks
       ├─ internal/git      thin wrapper over the system `git` binary (os/exec)
       ├─ internal/output   text / json / github findings formatters
       ├─ internal/config   flag/env/file precedence + typed .gravity.yaml manifest
       ├─ internal/prompts  system prompts for the release-notes/docs-gap/nucleus agents
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

## Coding standard

- **Comments explain *why*, not *what*.** Document contracts, security
  rationale, and non-obvious decisions; don't narrate the code. Exported symbols
  get a doc comment that starts with the symbol name (`revive`'s `exported` rule
  enforces this).
- **Errors.** `errors.New` for static messages; `fmt.Errorf("…: %w", err)` to
  wrap an underlying error (preserve the chain — `errorlint` guards it). Error
  strings are lowercase and unpunctuated. No `panic` in non-test code.
- **Exit-code contract** (`internal/cli/exit.go`): `0` success/no findings,
  `1` findings produced, `2` operational error (auth/network/bad input).
  Commands return an `*ExitError`; never call `os.Exit` inside a command.
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
  without a reason stated in the PR.
- **Testing**: stdlib `testing` + `httptest` mocks, table-driven where it fits.
  No live-server integration tests.
- **Greenfield degradation**: preview features (`capture`, `nucleus`) gate on
  `(*APIError).IsUnavailable()` → notice + skip (exit `0`) unless `--require`.
  Keep new preview features degrading the same way.

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

- **`read_file` reads from `HEAD`**, not the working tree, so uncommitted-but-
  tracked edits aren't visible to that one tool. This keeps it deterministic in
  CI (clean checkout) and avoids reading untracked files; use `git_show` for
  other refs.
- **`check api --openapi` "changed" detection compares the `summary` field.**
  Param-level diffing is intentionally deferred — `params` is provider-defined
  (`json.RawMessage`) and a naive structural diff would be noisy.
- **No live integration tests** — server interactions are exercised via
  `httptest` mocks.
- **The agent loop uses `tool_choice: auto`** and trusts the model to call the
  terminal submit tool; if it never does, the loop hits its cap and the command
  reports it could not obtain a structured result (exit `2`).
- **`gosec` is intentionally not enabled yet** — the git `exec.Command` and the
  computed-path `os.ReadFile` sites are already sandboxed; revisit with targeted
  excludes before turning it on.

## Pointers

- [README.md](README.md) — install + per-command usage + exit codes.
- [docs/platform-authoring-api.md](docs/platform-authoring-api.md) — the platform
  authoring API contract.
- [ci/](ci/) — composite Action / pipeline snippets for *downstream consumers*
  of the CLI. **Not** this repo's own CI (that's `.github/workflows/ci.yml`).
