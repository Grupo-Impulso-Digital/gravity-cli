# gravity-cli

`gravity` keeps a product's documentation in step with its code. A repository
declares **code facts** in a small `.gravity.yaml`; the Gravity app holds the
**editorial intent**: which sites and spaces the repository feeds, what each
**pass** writes, for whom and in which voice. `gravity run` in CI detects what
changed since each pass last ran and lets every pass update its target; the
changes of one run are reviewed in the app as one bundle.

> **1.0 is a clean break.** The v0.x commands (`sync`, `docs`, `release-notes`,
> `check api|docs`, `coverage`, `capture`, `nucleus`, `auth`, `doctor`, `ping`,
> `repos`, `spaces`) and the v1 manifest are gone. Invoking a removed command
> prints its replacement and exits `2`. Keep using gravity v0.3 until your
> pipeline is migrated.

> **This build is milestone C3 (setup UX).** `login`, `logout`, `whoami`,
> `init` (detection, at most three questions, CI wiring, token and secret),
> `status`, `passes`, `explain`, `version`, `run`, `preview` and `check` work
> end to end. The 1.0 GitHub action and per-provider PR comments (C4) are still
> to come.

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
gravity init            # detect, suggest passes, write .gravity.yaml + the CI file, install the token
gravity preview         # what every pass would write for your working tree
gravity status          # auth, product, passes, targets, watermarks, runs, health, capability warnings
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
| `gravity login` | Device flow: shows a code, opens the approval page, waits for approval, stores the token in a profile named after the organization. `--org`, `--profile`, `--no-browser`; headless: `--token <token>` or `--with-token` (reads it from stdin). |
| `gravity logout` | Revokes the current user token and removes its profile. `--all`. |
| `gravity whoami` | Principal, organization, other organizations, token kind, scopes, expiry, API URL, profile. |
| `gravity init` | Connects the repository in at most three questions (see below). `--yes`, `--product`, `--passes-as-code`, `--app-passes`, `--ci github\|gitlab\|bitbucket\|azure\|jenkins\|circleci\|none`, `--no-secret`, `--dry-run`, `--repo <id>`. Never commits or pushes. |
| `gravity status` | One view of the repository: auth and profile, connection, manifest, passes with targets, locked flags, watermarks and last runs, recent runs, open bundles, tokens, health and capability warnings (expiring token, missing module, missing scopes, no AI provider). `--runs N`, `--check` (exit `1` when health is not `live`). |
| `gravity passes` | `list` (default), `show <name>`, `edit <name>`. `--trigger`, `--branch`. |
| `gravity explain <page>` | Provenance of every block of a page (page id, `site/space/page` or viewer URL). `--block <key>`. |
| `gravity run` | The pipeline: plan, ranges, zero-cost scope skips, one leased run, passes, finish. `--pass`, `--trigger`, `--branch`, `--from`, `--to`, `--note`, `--dry-run`, `--lease-timeout`, `--parallel`, `--no-comment`, `--strict`. |
| `gravity preview` | Every pass as a dry run over your working tree (or `--committed`): the pages that would change, the instructions the app composes, the cost. `--format text\|diff\|json`, `--open`. Never writes. |
| `gravity check` | The pull request gate: check passes (or built-in drift and coverage) plus every pass's doc impact. Step summary, GitHub annotations and the PR comment (when `GITHUB_TOKEN` is set or `--comment`). `--fail-on`, `--annotate`. |
| `gravity version` | Version, commit, build date, Go version, platform (also `--version`). |

### `gravity init`

```
$ gravity init
✓ Signed in as dave@acme.io · Acme
✓ github.com/acme/billing-api · TypeScript · OpenAPI 3.1.0 (42 operations) · Next.js UI (18 routes) · 31 Markdown docs · GitHub Actions
? Product › Acme Platform (gateway connected)                                   [1]
? What should this repository keep up to date?   site: Developer Portal          [2]
  ✓ Developer Portal › API        reference  ← OpenAPI api/openapi.yaml (42 operations)
  ✓ Developer Portal › Guides     guides     ← Next.js routes in app (18)
  ✓ Developer Portal › Changelog  changelog  ← tags v* (12 releases)  [new space]
    Developer Portal › Handbook   verbatim   ← docs/handbook (9 files)  [new space]
  ✓ Nucleus memory                nucleus
    Change site… (now Developer Portal)
Preview   (every file with its full content, passes, new spaces, token scopes, secret)
? Write these and wire CI? › Write + set the secret (gh) · Write files only · Cancel   [3]
✓ Connected billing-api with 4 passes        Try it now:  gravity preview
```

- Detection is local: the git remote, languages, OpenAPI/Swagger documents,
  UI routes (Next.js, TanStack, React Router, SvelteKit, Nuxt/Vue, Angular),
  server routes and cobra commands, Markdown folders, runbooks, release tags
  and `CHANGELOG.md`, the CI provider, and an existing `.gravity.yaml`.
- The product question is skipped when `--product`, the manifest or the
  existing registration decides it; with no product in the organization a new
  one is created silently. A repository that already has passes in the app
  (or `--repo <id>`) skips straight to the write question.
- Passes are registered in the app (editable there) unless you pass
  `--passes-as-code`, which declares them in `.gravity.yaml` instead (locked
  as "managed in repo" in the app). Targets the server flags are marked
  "(needs approval)".
- A v1 `.gravity.yaml` replaces the passes question with its conversion
  report; the v2 file is written in place (passes-as-code, or `--app-passes`)
  and the original is kept as `.gravity.v1.yaml.bak`. Converted verbatim
  passes adopt the pages v0.x already synced.
- The repository token carries exactly the scopes its passes need. It is
  installed with `gh secret set` / `glab variable set` (on stdin, never in
  argv) when that CLI is signed in to the remote's host, otherwise printed
  once on stderr with where to paste it. CI files: GitHub
  `.github/workflows/gravity.yml`, GitLab `.gitlab/gravity.yml` plus an
  `include:` in `.gitlab-ci.yml`, Bitbucket `bitbucket-pipelines.yml`, Azure
  `azure-pipelines.gravity.yml`; Jenkins and CircleCI snippets are printed.
  Existing files are never overwritten.
- `--yes` accepts every suggestion (required without a terminal);
  `--dry-run` stops after the preview. A developer without
  `docs.repos.manage` is told how to get the repository pre-registered instead
  of being asked questions.

On a terminal, prompts use `charmbracelet/huh` (`ACCESSIBLE=1` switches to its
line-based mode), and `init`, `run`, `preview` and `check` show live per-step
progress and a summary card with links. `--json`, `CI=true`, `NO_COLOR` and
non-terminals get plain output.

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
  `gravity login --api-url <url> --profile <name>`. `gravity init` never signs
  in to a host named only by `.gravity.yaml`: run that `gravity login --api-url`
  first. When `--token` or `GRAVITY_TOKEN` goes to a manifest `apiUrl` other
  than the default host, a `token_to_manifest_host` warning suggests pinning
  `GRAVITY_API_URL`. API URLs must use https; plain http is accepted for
  localhost only.
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
| `1`  | findings: `check` findings in `failOn`, or `status --check` on an unhealthy repository |
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
internal/ui            output for terminals, CI logs and the --json envelope; huh prompts, bubbletea progress, summary cards
internal/auth          profiles.yaml, credential precedence, device login
internal/config        manifest v2: strict parsing, embedded JSON Schema, did-you-mean, v1 detection and conversion, token scopes
internal/config/legacy v1 manifest model (input of the v1 conversion)
internal/api           REST client for the CLI 1.0 contract + LLM gateway, error envelope, retries
internal/ci            CI provider detection (GitHub, GitLab, Bitbucket, Azure, Jenkins, CircleCI, generic)
internal/detect        local repository detection for init
internal/setup         init suggestions: product ranking, target site, pass templates, spaces to create
internal/cisetup       CI file templates per provider, gh/glab secret installers
internal/plan          plan fetch, manifest overlay (the repository wins on declared passes), skip decisions
internal/changeset     range resolution per trigger, ChangeSet, OpenAPI diff, symbols, unit mapping
internal/normalize     product slugs, API unit keys, canonical JSON (shared golden fixtures)
internal/git           wrapper over the system git binary
internal/glob          doublestar path matching
internal/run           run orchestration: connect, plan, lease wait, heartbeat, ingest, passes, finish, exit code
internal/passes        Pass interface and the seven kinds: guides, reference, verbatim, changelog, nucleus, check, capture
internal/verbatim      Markdown/MDX -> native blocks with high fidelity; file -> page mapping
internal/agent         one agent harness: forced submit, token budgets, ref-aware git tools, doc tools
internal/prompts       baked fallbacks of the hosted pass prompts
internal/report        PR doc-impact comment, step summary, annotations, page diffs
internal/docs          OpenAPI -> api blocks
internal/checks        OpenAPI parsing (libopenapi)
internal/pathsafe      repo-root path validation (leaf, stdlib-only)
internal/version       version and build info
```
