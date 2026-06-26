# gravity-cli

`gravity` is a CI/pipeline companion for the [Gravity](https://gravity.dev) docs
platform. In CI it:

1. **generates release notes** from git history,
2. **checks API-doc drift** between your OpenAPI spec / source and the
   documented API blocks, and
3. **checks docs completeness** against the code.

The AI harness — a tool-using agent loop — runs **inside the CLI** (the repo is
local in CI). Every LLM call is proxied through the Gravity platform gateway, so
tenant provider keys never touch CI: the CLI only sends an `sk_live_…` bearer
token.

## Install

Requires Go 1.25+.

```bash
# From source
go install github.com/impulso/gravity-cli/cmd/gravity@latest

# Or build locally
make build      # -> ./bin/gravity
```

## Quick start

```bash
# 1. Store your token + API URL (written to ~/.config/gravity/config.yaml, 0600)
gravity auth login --token sk_live_xxx --api-url https://app.gravity.dev

# 2. Record the site for this repo (writes ./.gravity.yaml)
gravity init --site docs --yes

# 3. Verify connectivity, token, and gateway model
gravity doctor
```

## Configuration

Configuration is resolved with this precedence (**highest first**):

1. **flags** — `--token`, `--api-url`, `--site`, `--space`, ...
2. **environment** — `GRAVITY_TOKEN`, `GRAVITY_API_URL`, `GRAVITY_SITE`,
   `GRAVITY_SPACE`
3. **project file** — `./.gravity.yaml`
4. **user file** — `~/.config/gravity/config.yaml`

CI sets environment variables, so env always wins over committed files.

`.gravity.yaml` (project, safe to commit — no token):

```yaml
site: docs
apiUrl: https://app.gravity.dev
```

`~/.config/gravity/config.yaml` (user, `0600`, holds the token):

```yaml
token: sk_live_xxx
apiUrl: https://app.gravity.dev
```

## Commands

### `gravity release-notes`

Resolve a git commit range, run the release-notes agent over the commits and
diffs, and emit structured, user-facing notes grouped into sections
(Added/Changed/Fixed/Removed/Security/Breaking).

```bash
# Default: latest tag..HEAD, post a draft + proposal, print the review URL
gravity release-notes --site docs --space changelog

# Explicit range, just print markdown
gravity release-notes --from v1.2.0 --to HEAD --output stdout

# Prepend to CHANGELOG.md
gravity release-notes --output file

# Preview the structured notes without writing or posting
gravity release-notes --dry-run
```

Key flags: `--site`, `--space` (default `changelog`), `--from`, `--to`,
`--output proposal|file|stdout` (default `proposal`), `--dry-run`, `--title`,
`--ci`.

The model finishes by calling the `submit_release_notes` tool; `--output
proposal` POSTs to the platform and prints the review URL (it creates a **draft
+ proposal**, it does not publish).

### `gravity check api`

Pull the documented API blocks for the site and compare them to the source.

```bash
# Diff against an OpenAPI spec
gravity check api --site docs --openapi openapi.yaml --format github

# No spec: verify each block's source-binding hash against the repo file
gravity check api --site docs
```

- **With `--openapi`**: builds the set of spec operations (method+path) and
  diffs them against the documented blocks, producing `undocumented` (in spec,
  not in docs), `orphaned` (in docs, not in spec), and `changed` (summary
  differs) findings.
- **Without `--openapi`**: for each block whose `sourceBinding` has a `hash` and
  a `ref` that resolves to a file in the repo, recompute the file's sha256 and
  flag `stale` on mismatch. Blocks without a verifiable binding are **skipped
  and counted** (never silently).

Flags: `--site`, `--openapi <path>`, `--ci`, `--format text|json|github`.

### `gravity check docs`

Pull published page snapshots and verify machine/hybrid blocks whose source
binding has a `hash` + repo-resident `ref`, flagging stale mismatches. With
`--ai`, additionally run the docs-gap agent over the `from..to` diff plus a
digest of the current docs; deterministic and AI findings are merged.

```bash
gravity check docs --site docs
gravity check docs --site docs --ai --from v1.2.0 --to HEAD
```

Flags: `--site`, `--from`, `--to`, `--ai`, `--ci`, `--format text|json|github`.

### `gravity selfdoc`

Keep the CLI's own documentation current. `selfdoc` walks the cobra command tree
and emits a documentation space describing every command, its flags, and the
exit-code contract — **deterministically, with no AI/agent loop**. It produces
two pages in the target space (default `cli`):

- **command-reference** — machine blocks (command table, per-command usage +
  flag tables, global flags, exit codes). Each block is bound to its real source
  file via `sourceBinding` (kind `cli`, sha256 hash), so `gravity check docs`
  can later flag drift against the binary.
- **overview** — a single `hybrid` prose block humans may freely edit; the CLI
  never overwrites human-authored fields on re-run.

```bash
# Ensure the space and upsert both pages as drafts + open proposals (default)
gravity selfdoc --site docs --space cli

# Print the generated blocks without posting
gravity selfdoc --output stdout

# Show the exact JSON that would be posted (EnsureSpace + each page) without calling the API
gravity selfdoc --dry-run

# Override the reference page's title
gravity selfdoc --title "gravity CLI reference"
```

Flags: `--space` (default `cli`; honours `GRAVITY_SPACE` / `.gravity.yaml` when
unset), `--output proposal|stdout` (default `proposal`), `--dry-run`, `--title`.
Like `release-notes` and the page-upsert path, `selfdoc` writes a **draft +
proposal** (`status: "proposed"`) and never publishes — a human reviews and
merges in-app. Must run inside the CLI's own git repo so block hashes use the
same git toplevel the drift checker uses.

### Other commands

- `gravity version` — print the version.
- `gravity init` — write `.gravity.yaml` (site, apiUrl, optional default space;
  interactive, or `--yes` for flags/env). Refuses to clobber an existing
  `.gravity.yaml` unless `--force` is passed.
- `gravity auth login --token <sk> --api-url <url>` — store credentials (`0600`).
- `gravity doctor` — check token validity, org, model, tone, and provider key
  via `/whoami` + `/llm/v1/config`. Exits `2` on auth/network failure.

## Exit codes

| Code | Meaning  |
| ---- | -------- |
| `0`  | success / no findings |
| `1`  | findings (drift, gaps, stale bindings) |
| `2`  | error (auth, network, bad input) |

## CI

See [`ci/README.md`](ci/README.md) for ready-to-use GitHub Actions, GitLab CI,
and Bitbucket Pipelines snippets. `GRAVITY_TOKEN` is the secret.

## Development

```bash
make build   # build ./bin/gravity
make test    # go test ./...
make vet     # go vet ./...
make fmt     # gofmt -w .
make lint    # vet + gofmt check
make ci      # lint + test + build
```

## Architecture

```
cmd/gravity            entrypoint (signal-aware context, version wiring)
internal/cli           cobra command tree, config resolution, exit codes
internal/config        flag/env/file precedence (viper)
internal/api           HTTP client: REST endpoints + LLM gateway (Messages subset)
internal/agent         tool-using loop + sandboxed read-only git tools + submit tools
internal/git           thin wrapper over the system `git` binary (os/exec)
internal/checks        OpenAPI operation diff + source-binding hash verification
internal/prompts       system prompts for the release-notes and docs-gap agents
internal/output        text / json / github findings formatters
```

The agent loop (`internal/agent`) calls `POST /api/llm/v1/messages` (an
Anthropic Messages API subset) through the gateway. Requests carry an optional
`{ site, space }` context so the gateway can scope its server-side
RAG/recall to the right site; an absent or unknown slug falls back to
org/default-site scope. The model is given
read-only git tools (`git_log`, `git_diff`, `git_show`, `list_files`,
`read_file`, `grep`) — all paths sandboxed to the repo root — plus a terminal
"submit" tool (`submit_release_notes` or `report_findings`) that ends the loop
and returns its structured input. The loop is capped on iterations and tool
calls and returns a partial result with a warning if a cap is hit.
