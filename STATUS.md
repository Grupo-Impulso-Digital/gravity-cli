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
- `gravity init` — scaffolds a rich, commented `.gravity.yaml` (connection,
  product/multi-repo identity, spaces, example source/document mappings);
  interactive prompts with `--yes` for non-interactive flag/env use, `--migrate`
  to upgrade a legacy file in place. Refuses to overwrite unless `--force`.
- `gravity auth login --token [--api-url]` — stores the token in
  `~/.config/gravity/config.yaml` at mode `0600`; URL defaults to the platform.
- `gravity doctor` — loads + validates `.gravity.yaml`, prints the resolved
  configuration with the source of each value, then calls `/api/v1/whoami` +
  `/api/llm/v1/config`; reports token validity, org, model, tone, hasKey; exit
  `2` on config/auth/network failure.
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
- `gravity sync` — authors the `sources` + `documents` mappings from
  `.gravity.yaml`: OpenAPI specs → machine-owned `api` blocks (identity keys,
  whole-file sha256 binding, satisfy `check api` by construction); Markdown →
  native heading/table/code blocks with the remainder preserved verbatim as
  prose (`as: page`), or a versioned release (`as: release`). `--only api|docs`,
  `--page`, `--space`, `--output proposal|stdout`, `--dry-run`, `--ci`. Writes
  draft + proposal; exit `0/2` only.
- `gravity capture` (preview, greenfield) — triggers the platform agent runner to
  navigate/screenshot an app and attach artifacts as a draft + proposal; `status
  <runId>` polls. Feature-gated: unavailable → exit 0 + notice (`--require` →
  exit 2); when live, succeeded=0/partial=1/failed=2. Contract defined in
  `internal/api/capture.go`.
- `gravity nucleus query|sync` (preview, greenfield) — `query` retrieves memory
  atoms; `sync` distills atoms from code changes (agent loop + `submit_atoms`) and
  contributes them. Feature-gated; `sync` pre-checks availability before spending
  LLM calls. Atom retrieval also augments release-notes/check-docs best-effort
  (`enrichKickoff`). Contract in `internal/api/nucleus.go`.
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
- **Feature gate** (`internal/api`, `internal/cli/feature.go`) —
  `(*APIError).IsUnavailable()` (404/501 or `not_implemented`/`feature_disabled`/
  `unknown_route`) plus `WhoAmI.Features`; `skippableFeature` turns "endpoint not
  live yet" into a notice + skip (or a hard error with `--require`), and `doctor`
  reports each preview feature's availability.
- **Config** (`internal/config`) — typed precedence (flags > env > project file >
  user file) with no viper dependency; empty values never clobber lower-precedence
  ones. A typed `.gravity.yaml` manifest (multi-repo product identity, spaces,
  source/document mappings, knowledge namespace) is loaded and validated, the
  token is rejected if committed to the project file, and the API URL has a single
  built-in default (`DefaultAPIURL`).
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
- `internal/docs` — OpenAPI→api blocks satisfy `check api` (binding verified,
  zero drift); Markdown→native heading/table/code blocks with verbatim prose;
  deterministic/idempotent re-authoring.
- `internal/config` — env beats file; flags beat everything; project beats user;
  committed token rejected; default API URL applied; manifest defaults,
  multi-repo `PageTarget` namespacing, and validation errors.
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
