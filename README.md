# gravity-cli

`gravity` keeps a product's documentation in step with its code. A repository
declares **code facts** in a small `.gravity.yaml`; the Gravity app holds the
**editorial intent**: which sites and spaces the repository feeds, what each
**pass** writes, for whom and in which voice. In CI, `gravity run` detects what
changed since the last successful run and lets every pass update its target.

> **1.0 is a clean break.** The v0.x commands (`sync`, `docs`, `release-notes`,
> `check api|docs`, `coverage`, `capture`, `nucleus`, `auth`, `doctor`, `ping`,
> `repos`, `spaces`) and the v1 manifest are gone. Invoking a removed command
> prints its replacement and exits `2`. Keep using gravity v0.3 until your
> pipeline is migrated.

> **This build is milestone C1 (CLI core).** `login`, `logout`, `whoami`,
> `init` (minimal), `status`, `passes`, `explain` and `version` work end to
> end. `run`, `preview` and `check` are registered and exit `2` until the pass
> engine lands.

## Install

`gravity` ships as a single static binary — no runtime, no dependencies.

**macOS / Linux — one line:**

```bash
curl -fsSL https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.sh | sh
```

The script auto-detects your OS/arch, downloads the matching binary from the
latest [release](https://github.com/Grupo-Impulso-Digital/gravity-cli/releases), verifies its
checksum, and installs it to `/usr/local/bin` (or `~/.local/bin`). Override the
target with `GRAVITY_INSTALL_DIR=...`, or pin a version with `GRAVITY_VERSION=v0.1.0`.

**Homebrew (macOS / Linux):**

```bash
brew install Grupo-Impulso-Digital/tap/gravity
```

**Scoop (Windows):**

```powershell
scoop bucket add impulso https://github.com/Grupo-Impulso-Digital/scoop-bucket
scoop install gravity
```

**Direct download:** grab the archive for your platform from the
[releases page](https://github.com/Grupo-Impulso-Digital/gravity-cli/releases), unpack it, and
put `gravity` on your `PATH`. Every release ships a `checksums.txt`.

**From source** (requires Go 1.25+):

```bash
go install github.com/Grupo-Impulso-Digital/gravity-cli/cmd/gravity@latest

# Or build locally from a checkout
make build      # -> ./bin/gravity
```

## Quick start

```bash
gravity login           # browser device flow; stores a profile in ~/.config/gravity/profiles.yaml
gravity init            # connect this repository and write a minimal .gravity.yaml
gravity status          # auth, product, passes, targets, watermarks, runs, health
gravity passes          # which passes apply to this branch and trigger
gravity passes show developer-api
```

## The manifest (`.gravity.yaml`, version 2)

The smallest valid manifest is one line:

```yaml
version: 2
```

Identity comes from the git remote; passes come from the app. Everything else is
optional: `product`, `apiUrl`, `appPasses` (`allow` | `ignore`), `code`
(`openapi`, `entrypoints`, `include`, `exclude`, `units`), `docs` and
`passes` (passes-as-code, shown locked as "managed in repo" in the app). Every
pass needs a `name` and a `kind` (`guides`, `reference`, `verbatim`,
`changelog`, `nucleus`, `check`, `capture`); passes that write pages also need
a `target` of the form `<site>/<space>[/<collection>...]`. The key is
`triggers`, not `on`.

The manifest is validated against the embedded JSON Schema
(`internal/config/schema/gravity.schema.json`). Unknown keys fail with a
suggestion (`passes[0].trigers: unknown key (did you mean "triggers"?)`), a
`token:` anywhere is refused, and paths must stay inside the repository. A v1
manifest is detected and refused outside `gravity init`.

Only the repository's authoritative branch (the default branch unless the app
says otherwise) stores the manifest's passes in Gravity. Other branches show
their local passes as an overlay (`gravity passes` marks them `repo (local)`).

## Commands

| Command | Purpose |
| ------- | ------- |
| `gravity login` | Device flow: shows a code, opens the approval page, stores the token. `--org`, `--no-browser`, `--with-token` (reads a token from stdin). |
| `gravity logout` | Revokes the current user token and removes its profile. `--all`. |
| `gravity whoami` | Principal, organization, other organizations, token kind, scopes, expiry, API URL, profile. |
| `gravity init` | Connects the repository (`POST /api/v1/repos/connect`) and writes `version: 2` when there is no manifest. `--product`, `--dry-run`. Never commits or pushes. |
| `gravity status` | One view of the repository. `--runs N`, `--check` (exit `1` when health is not `live`). |
| `gravity passes` | `list` (default), `show <name>`, `edit <name>`. `--trigger`, `--branch`. |
| `gravity explain <page>` | Provenance of every block of a page (page id, `site/space/page` or viewer URL). `--block <key>`. |
| `gravity run`, `preview`, `check` | The pipeline, the local preview and the PR gate (not available in this build). |
| `gravity version` | Version, commit, build date, Go version, platform (also `--version`). |

### Global flags

`--profile` (env `GRAVITY_PROFILE`), `--api-url` (env `GRAVITY_API_URL`),
`--token` (env `GRAVITY_TOKEN`), `--manifest` (env `GRAVITY_MANIFEST`),
`-C <dir>`, `--json`, `--no-color` (also `NO_COLOR`), `-q/--quiet`,
`-v/--verbose`.

- Credentials: `--token` > `GRAVITY_TOKEN` > profile (`--profile` >
  `GRAVITY_PROFILE` > the current profile). Tokens are never read from
  `.gravity.yaml`.
- API URL: `--api-url` > `GRAVITY_API_URL` > manifest `apiUrl` > profile
  `apiUrl` > `https://api.gravitydocs.io`. A profile token is only ever sent to
  the host that issued it: when the resolved API URL is another host, the
  command exits `2` (`token_host_mismatch`) before any request. Pair another
  host with `--token` or `GRAVITY_TOKEN`, or sign in to it with
  `gravity login --api-url <url> --profile <name>`.
- `--json` prints exactly one JSON document on stdout
  (`{ ok, command, version, data, warnings, error }`); progress and messages go
  to stderr.
- With `CI=true`, or when stdin/stderr is not a terminal, output is plain ASCII
  and nothing prompts.

### Profiles

CLI 1.x reads and writes `~/.config/gravity/profiles.yaml` (mode `0600`,
honors `XDG_CONFIG_HOME`). On first use, a token found in the v0.x
`~/.config/gravity/config.yaml` is copied into the profile `default`; the v0.x
file is never modified, so a v0.3 binary on the same machine keeps working.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| `0`  | success |
| `1`  | findings (`status --check` on an unhealthy repository; check findings once `check` ships) |
| `2`  | operational error: auth, network, bad input, invalid manifest, missing target, removed command |
| `3`  | license refusal (`module_disabled`, `seat_limit`); ask a workspace administrator |

A `404` is never treated as "feature unavailable": the CLI reads server
capabilities from `/whoami` and refuses servers without CLI 1.0 pipelines.
`429` and `5xx` answers are retried three times with exponential backoff from
one second, honoring `Retry-After`.

## Development

```bash
make build   # build ./bin/gravity
make test    # go test ./...
make vet     # go vet ./...
make fmt     # go tool golangci-lint fmt  (gofumpt + goimports)
make lint    # go tool golangci-lint run  (govet + staticcheck + curated set)
make ci      # lint + test + build
```

`golangci-lint` is pinned via the go.mod `tool` directive, so `make lint` needs
no separate install. Contributor standards and architecture conventions live in
[AGENTS.md](AGENTS.md).

## Architecture

```
cmd/gravity            entrypoint (signal-aware context)
internal/cli           cobra command tree, global flags, output modes, exit codes
internal/ui            output for terminals, CI logs and the --json envelope
internal/auth          profiles.yaml, credential precedence, device login
internal/config        manifest v2: strict parsing, embedded JSON Schema, did-you-mean, v1 detection
internal/config/legacy v1 manifest model (input of the v1 conversion)
internal/api           REST client for the CLI 1.0 contract + LLM gateway, error envelope, retries
internal/ci            CI provider detection (GitHub, GitLab, Bitbucket, Azure, Jenkins, CircleCI, generic)
internal/plan          plan fetch, manifest overlay (the repository wins on declared passes), skip decisions
internal/changeset     range resolution per trigger, ChangeSet, OpenAPI diff, symbols, unit mapping
internal/normalize     product slugs, API unit keys, canonical JSON (shared golden fixtures)
internal/git           wrapper over the system git binary
internal/glob          doublestar path matching
internal/agent         tool-using agent loop over the LLM gateway
internal/docs          OpenAPI -> api blocks, Markdown -> native blocks
internal/checks        OpenAPI parsing (libopenapi)
internal/pathsafe      repo-root path validation (leaf, stdlib-only)
internal/version       version and build info
```
