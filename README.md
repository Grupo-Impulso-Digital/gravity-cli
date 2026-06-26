# gravity-cli

`gravity` is a CI/pipeline companion for the [Gravity](https://gravity.dev) docs
platform. It keeps an enterprise's **customer-facing** docs in lockstep with the
code. In CI it:

1. **generates release notes** from git history,
2. **authors machine-owned, code-derived doc blocks** (`sync`) — API reference
   from OpenAPI, and Markdown documents — that change only when the code changes,
3. **checks API-doc drift and docs completeness** against the code, and
4. works hand-in-hand with Gravity to **capture the running app** (screenshots)
   and feed the **nucleus** knowledge service (both preview).

Product documentation itself — navigating and documenting a live app — is
Gravity's own agent runner's job; this CLI is the CI companion that triggers it
and keeps the source-derived surfaces honest.

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
# 1. Store your token (written to ~/.config/gravity/config.yaml, 0600).
#    --api-url defaults to the platform; pass it to target another host.
gravity auth login --token sk_live_xxx

# 2. Scaffold ./.gravity.yaml for this repo (site, product identity, mappings)
gravity init --site docs --yes

# 3. Verify config, token, and gateway model
gravity doctor
```

## Configuration

Connection values are resolved with this precedence (**highest first**):

1. **flags** — `--token`, `--api-url`, `--site`, `--space`, ...
2. **environment** — `GRAVITY_TOKEN`, `GRAVITY_API_URL`, `GRAVITY_SITE`,
   `GRAVITY_SPACE`, `GRAVITY_KNOWLEDGE_NAMESPACE`
3. **project file** — `./.gravity.yaml`
4. **user file** — `~/.config/gravity/config.yaml`

CI sets environment variables, so env always wins over committed files. In CI
the only required input is the `GRAVITY_TOKEN` secret — everything else can live
in `.gravity.yaml`. The API URL falls back to a built-in default when nothing
sets it.

**The token is a secret and lives only in the environment or the user file.**
A `token:` committed to `.gravity.yaml` is rejected with an error — never
silently honored.

`.gravity.yaml` (project, safe to commit — scaffold it with `gravity init`):

```yaml
version: 1
site: docs
apiUrl: https://gravity.dave-vermette-1.workers.dev
product:
  slug: acme-platform   # a product can span several repos sharing one site
  repo: billing-api     # this repo's unique name within the product
spaces:
  default: billing-api
# sources:   — OpenAPI/code → machine-owned, drift-locked doc blocks
# documents: — Markdown files → pages or releases
# knowledge: — nucleus memory namespace shared across the product's repos
```

`~/.config/gravity/config.yaml` (user, `0600`, holds the token):

```yaml
token: sk_live_xxx
apiUrl: https://gravity.dave-vermette-1.workers.dev
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

### `gravity sync`

Author the doc mappings declared in `.gravity.yaml` onto the platform. This is
the authoring counterpart to `check` (which verifies): `sync` writes, `check`
reads.

```bash
# Author everything in .gravity.yaml (sources + documents)
gravity sync

# Only the OpenAPI → api blocks, or only the Markdown documents
gravity sync --only api
gravity sync --only docs

# One mapping by its page slug; preview without posting
gravity sync --page api-reference --dry-run
gravity sync --output stdout
```

- **`sources`** — each OpenAPI spec becomes machine-owned `api` blocks (one per
  operation, keyed `api:<METHOD>:<path>`), bound to the spec file by sha256.
  They satisfy `gravity check api` immediately and go stale only when the spec
  changes — there is no other way to change them.
- **`documents`** — each Markdown file is decomposed into Gravity's **native**
  blocks (headings, tables, code) with anything else preserved verbatim as
  prose. `as: page` upserts a page; `as: release` posts a versioned release.
  `ownership` (`machine`/`hybrid`/`human`) controls whether humans may edit the
  result in Gravity.

Like every write path, `sync` creates a **draft + open proposal** and never
publishes. Authoring exits `0` (ok) or `2` (error) — findings (`1`) stay
exclusive to `check`.

Flags: `--only api|docs`, `--page <slug>`, `--space <slug>`,
`--output proposal|stdout` (default `proposal`), `--dry-run`, `--ci`.

### `gravity capture` (preview)

Trigger the Gravity platform's agent runner to navigate an app, capture
pages/screenshots, and attach them to the docs site as a draft + proposal — e.g.
after a release, to capture the new UI.

```bash
gravity capture --url https://staging.app --label v1.4.0
gravity capture --url https://staging.app --release-proposal prop_123   # attach to a release
gravity capture status run_abc
```

The platform runner endpoint is not live yet. Until it ships, `capture` reports
that the feature is unavailable and **exits 0** (so it is safe to add to release
CI today); pass `--require` to make absence a hard error. When live, a partial
run (navigation errors) exits `1`, a failed run exits `2`.

### `gravity nucleus` (preview)

Nucleus is the Gravity memory service: small "atoms" of knowledge that link to
other atoms, so the AI can recall product context without re-reading whole docs.
A product's repos share one `knowledge.namespace`, composing a single memory.

```bash
gravity nucleus query "how do webhooks retry?"   # retrieve relevant atoms
gravity nucleus sync                             # distill atoms from code changes and contribute them
```

The nucleus API is not live yet; these commands degrade gracefully (exit 0,
`--require` to fail). Atom retrieval also augments `release-notes` and
`check docs --ai` generation, strictly best-effort — it can never break those
commands.

### Other commands

- `gravity version` — print the version.
- `gravity init` — scaffold a rich, commented `.gravity.yaml` (connection,
  product/multi-repo identity, spaces, and example source/document mappings).
  Interactive, or `--yes` for flags/env; `--migrate` upgrades a legacy file in
  place. Refuses to clobber an existing file unless `--force`.
- `gravity auth login --token <sk>` — store the token (`0600`); `--api-url`
  defaults to the platform.
- `gravity doctor` — validate `.gravity.yaml`, print the resolved configuration
  (and where each value came from), then check token/org/model/provider-key via
  `/whoami` + `/llm/v1/config`. Exits `2` on a config, auth, or network failure.

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
internal/config        flag/env/file precedence + typed .gravity.yaml manifest
internal/api           HTTP client: REST endpoints + LLM gateway (Messages subset)
internal/agent         tool-using loop + sandboxed read-only git tools + submit tools
internal/git           thin wrapper over the system `git` binary (os/exec)
internal/checks        OpenAPI operation diff + source-binding hash verification (+ shared hasher)
internal/docs          block authoring: OpenAPI→api blocks, Markdown→native blocks
internal/prompts       system prompts for the release-notes, docs-gap, and nucleus agents
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
