# Status

A complete, tested Go CLI built to the Gravity CI-companion contract.

## Green checks

- `go build ./...` — clean
- `go vet ./...` — clean
- `gofmt -l .` — empty
- `go test ./...` — all packages pass

## Complete

### Commands (cobra)

- `gravity version` — version string (overridable via `-ldflags -X main.version`).
- `gravity init` — writes `.gravity.yaml` (site, apiUrl, optional space);
  interactive prompts with `--yes` for non-interactive flag/env use. Refuses to
  overwrite an existing `.gravity.yaml` unless `--force` is passed.
- `gravity auth login --token --api-url` — stores credentials in
  `~/.config/gravity/config.yaml` at mode `0600`.
- `gravity doctor` — calls `/api/v1/whoami` + `/api/llm/v1/config`; reports token
  validity, org, model, tone, hasKey; exit `2` on auth/network failure.
- `gravity release-notes` — resolves the git range, runs the release-notes agent
  loop (git tools + `submit_release_notes`), and routes output to
  `proposal` (POST + review URL) / `file` (prepend `CHANGELOG.md`) / `stdout`;
  `--dry-run`, `--title`, `--ci` supported.
- `gravity check api` — pulls `/api-blocks`; with `--openapi` diffs spec
  operations (undocumented/orphaned/changed via libopenapi), without it verifies
  source-binding sha256 hashes against repo files (stale), logging skipped
  blocks. `--format text|json|github`. Exit `0/1/2`.
- `gravity check docs` — pulls `/pages`, verifies machine/hybrid source bindings
  (stale), and with `--ai` runs the docs-gap agent over the `from..to` diff plus
  a docs digest, merging deterministic + AI findings.
- `gravity selfdoc` — keeps the CLI's own docs current: walks the cobra command
  tree and deterministically (no AI) emits a `command-reference` page (machine
  blocks: command table, per-command usage + flag tables, global flags, exit
  codes — each bound to its source file via `sourceBinding` kind `cli`) plus an
  `overview` page (one `hybrid` prose block humans may edit). Ensures the target
  space (`--space`, default `cli`, honours `GRAVITY_SPACE`/`.gravity.yaml`) then
  upserts both pages as draft + proposal (`status: "proposed"`); `--output
  proposal|stdout`, `--dry-run`, `--title`. Must run inside the CLI git repo so
  block hashes use the same git toplevel as the drift checker.

### Internals

- **HTTP client** (`internal/api`) — built exactly to the contract: bearer auth
  on every request, the `{"error":{"code","message"}}` envelope decoded into
  `*APIError` (with `IsAuth()`), all REST endpoints, and the LLM Messages subset
  including content that may be a string or a part array.
- **Agent harness** (`internal/agent`) — the tool-using loop against
  `/api/llm/v1/messages`: dispatches tools, feeds `tool_result` back, terminates
  on the submit tool or `end_turn`, and caps iterations (24) and tool calls (80)
  with a partial-result warning. Read-only git tools are sandboxed to the repo
  root (rejects absolute paths and `..` escapes); output sizes are capped.
- **Git** (`internal/git`) — shells out to the system `git` (no pure-Go lib):
  range resolution (latest tag → HEAD, else first commit → HEAD), log, diff,
  show, ls-files, grep.
- **Checks** (`internal/checks`) — libopenapi v2/v3 operation extraction, the
  operation diff, and source-binding hash verification with explicit skip
  reasons.
- **Config** (`internal/config`) — viper-based precedence: flags > env > project
  file > user file; empty env values do not clobber file values.
- **Output** (`internal/output`) — text, JSON, and GitHub-annotation formatters
  with stable severity-ordered output and AI-severity normalisation.

### Tests

- `internal/agent` — loop dispatches a tool then terminates on a
  `submit_release_notes` terminal call (httptest mock); end-turn stop; iteration
  cap; path-sandbox and truncation unit tests.
- `internal/git` — temp repo with commits + a tag; range resolution (tag→HEAD
  and no-tags→first-commit), changed files, log, diff, show, ls-files.
- `internal/api` — httptest mocks for whoami/pages/api-blocks/release-notes:
  auth header, query params, request-body shape, response parsing, and error
  envelope; LLM message string-vs-array content marshalling.
- `internal/checks` — OpenAPI parse + undocumented/orphaned/changed diff on a
  fixture spec; binding hash fresh/stale/skip cases.
- `internal/config` — env beats file; flags beat everything; project beats user.
- `internal/output` — text/json/github formatters and severity normalisation.

### CI ergonomics (`ci/`)

- `ci/github/action.yml` — composite GitHub Action wrapping the binary.
- `ci/gitlab/.gitlab-ci.yml` and `ci/bitbucket/pipe` snippets.
- `ci/README.md` — exit-code convention and the `GRAVITY_TOKEN` secret model.

## Deliberate limitations

- **`read_file` reads from `HEAD`** rather than the working tree, so
  uncommitted-but-tracked changes are not visible to that one tool. This keeps
  the tool deterministic against committed state in CI (where the checkout is
  clean) and avoids reading untracked files; `git_diff`/`git_show` cover ref
  comparisons. Could be extended to read the working copy if a use case needs
  it.
- **`check api --openapi` "changed" detection compares the `summary`** field.
  The contract also mentions params; param-level diffing is intentionally left
  as a focused follow-up because the `params` shape is provider-defined
  (`json.RawMessage`) and a naive structural diff would be noisy. Summary drift
  is the high-signal, low-false-positive signal shipped here.
- **No live integration tests** — everything that needs a server is exercised
  via `httptest` mocks per the contract; there is no test against a real Gravity
  instance.
- **The agent loop uses `tool_choice: auto`** and trusts the model to call the
  terminal tool; if it never does, the loop hits the iteration cap and the
  command reports that it could not obtain a structured result (exit `2`).
- **`init` is "interactive-ish"** — it prompts on a TTY but is fully driveable
  via flags + `--yes` for scripted/CI use.
