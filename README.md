# gravity-cli

`gravity` is a CI/pipeline companion for the [Gravity](https://gravity.dev) docs
platform. It keeps an enterprise's **customer-facing** docs in lockstep with the
code. In CI it:

1. **generates release notes** from git history,
2. **authors machine-owned, code-derived doc blocks** (`sync`) — API reference
   from OpenAPI, and Markdown documents — that change only when the code changes,
3. **checks API-doc drift and docs completeness** against the code, and reports
   **documentation coverage** against the repo's own feature inventory,
4. works hand-in-hand with Gravity to **trigger a Doc Agent run** against a
   connected app and to feed the **nucleus** memory service.

Product documentation itself — navigating and documenting a live app — is
Gravity's own agent runner's job; this CLI is the CI companion that triggers it
and keeps the source-derived surfaces honest.

The AI harness — a tool-using agent loop — runs **inside the CLI** (the repo is
local in CI). Every LLM call is proxied through the Gravity platform gateway, so
tenant provider keys never touch CI: the CLI only sends an `sk_live_…` bearer
token.

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
# 1. Store your token (written to ~/.config/gravity/config.yaml, 0600).
#    --api-url defaults to the platform; pass it to target another host.
gravity auth login --token sk_live_xxx

# 2. Configure ./.gravity.yaml for this repo with the interactive wizard
#    (detects OpenAPI specs + Markdown docs and offers to map them).
#    --yes detects and maps them without asking; --dry-run previews the file.
gravity init

# 3. Verify config, token, and gateway model
gravity doctor
```

## Configuration

Connection values are resolved with this precedence (**highest first**):

1. **flags** — `--token`, `--api-url`, `--site`, `--space`, ...
   (`--space` and `GRAVITY_SPACE` override `spaces.default`)
2. **environment** — `GRAVITY_TOKEN`, `GRAVITY_API_URL`, `GRAVITY_SITE`,
   `GRAVITY_SPACE`, `GRAVITY_KNOWLEDGE_NAMESPACE`
3. **project file** — `./.gravity.yaml`
4. **user file** — `~/.config/gravity/config.yaml`

CI sets environment variables, so env always wins over committed files. In CI
the only required input is the `GRAVITY_TOKEN` secret — everything else can live
in `.gravity.yaml`. The API URL falls back to a built-in default when nothing
sets it: `https://api.gravitydocs.io`, the hosted API. `https://app.gravitydocs.io`
(the web app) still proxies the API for older configs; `https://gravitydocs.io`
is the marketing site and serves no API.

**The token is a secret and lives only in the environment or the user file.**
A `token:` committed to `.gravity.yaml` is rejected with an error — never
silently honored.

`.gravity.yaml` (project, safe to commit — scaffold it with `gravity init`):

```yaml
version: 1
site: docs
product:
  slug: acme-platform   # a product can span several repos sharing one site
  repo: billing-api     # this repo's unique name within the product
spaces:
  default: billing-api
sources:                # OpenAPI specs -> machine-owned, drift-locked api blocks
  - source: openapi/openapi.yaml
    kind: openapi
    page: api-reference
documents:              # Markdown files -> pages or releases
  - file: README.md
    page: overview
releaseNotes:           # optional; these are the defaults
  space: changelog
  changelog: CHANGELOG.md
# knowledge.namespace — nucleus memory namespace shared across the product's repos
```

The file is parsed **strictly**: an unknown key is an error that names its line
and suggests the key you probably meant (`unknown key "relaseNotes" — did you
mean "releaseNotes"?`). Keys removed in v0.3 (`sources[].generator`,
`knowledge.scope`) fail with a clear message; `gravity init --migrate` drops them
for you. `apiUrl` is only needed for a self-hosted platform.

#### Multi-repo products: subspaces, shared spaces, home pages

A large product can organize one site as a top-level **space** per product with
a **subspace** per module, and connect several micro-service repos to one
"mother" subspace. Each repo declares where it publishes:

```yaml
# e.g. the fundamentum-device-api repo, one of several feeding "connect"
product:
  slug: fundamentum
  repo: device-api
spaces:
  default: connect      # this repo's mother subspace
  parent: fundamentum   # connect is a subspace of the fundamentum space
  home: overview        # pin the overview page as the space's home page
  shared: [connect]     # sibling repos also publish into connect
documents:
  - file: README.md
    page: overview
```

- **`parent`** nests the default space one level under a top-level space.
  `gravity sync` (and the `init` wizard) create/reparent it idempotently.
- **`shared`** marks spaces co-fed by sibling repos. Each repo's pages keep a
  `repo/` slug prefix (page identity on the platform is `(space, slug)`, so
  same-named pages from different repos never collide) **and** are grouped into
  a per-repo **collection** — a page folder named after `product.repo` — so the
  sidebar shows one tidy group per repo. Unshared spaces stay flat: the common
  one-repo-one-space setup needs none of this.
- **`home`** pins a page as the space's landing page. It stays flat and
  unprefixed (one landing page per space) — declare it from exactly one repo.
- **`collection:`** on a `sources`/`documents` mapping files that page under an
  explicit folder, overriding the per-repo default.

All of this requires the platform's `space-hierarchy` capability (`gravity
doctor` reports it). Older platforms sync flat with a notice — nothing breaks.
Use `gravity spaces` to see the resulting hierarchy and what feeds it.

#### Declaring spaces and who they serve

`spaces.declare` states the spaces the site should have and the audience each
one serves. `gravity sync` and `gravity docs generate` ensure them first
(parents before children) and route every authored page to the space whose
audiences cover it:

```yaml
spaces:
  default: product
  declare:
    - slug: product
      name: Product docs
      type: product-docs      # product-docs|api-reference|release-notes|knowledge-base|handbook|general
      visibility: public      # public|unlisted|private|inherit
      audiences: [public, users]
    - slug: developers
      name: Developer docs
      parent: product         # one level only; must be another declared slug
      type: api-reference
      visibility: unlisted
      audiences: [developers]
```

- **Routing.** A planned page goes to the declared space whose `audiences`
  cover the page's audiences; the narrowest match wins (a subspace beats its
  parent). An explicit `space:` on the page, or `--space`, still wins outright.
- **Degradation.** `type`/`visibility` need the platform's `space-metadata`
  capability; without it the spaces are still created, minus those two fields,
  with one notice. `parent` follows `space-hierarchy` as above.

`~/.config/gravity/config.yaml` (user, `0600`, holds the token):

```yaml
token: sk_live_xxx
apiUrl: https://api.gravitydocs.io
```

## Commands

### `gravity release-notes`

Resolve a git commit range, run the release-notes agent over the commits and
diffs, and emit structured, user-facing notes grouped into sections
(Added/Changed/Fixed/Removed/Security/Breaking).

```bash
# Default: latest tag..HEAD, post a draft + proposal into releaseNotes.space
gravity release-notes

# Explicit range, just print markdown
gravity release-notes --from v1.2.0 --to HEAD --output stdout

# Prepend to releaseNotes.changelog (default CHANGELOG.md)
gravity release-notes --output file

# Preview the structured notes without writing or posting
gravity release-notes --dry-run
```

Key flags: `--site`, `--space` (default: `releaseNotes.space`, else
`changelog` — never `spaces.default`), `--from`, `--to`,
`--output proposal|file|stdout` (default `proposal`), `--changelog` (default:
`releaseNotes.changelog`, else `CHANGELOG.md`), `--dry-run`, `--title`, `--json`.
An empty commit range prints `no commits in range …; nothing to do` and exits `0`
without spending an LLM call.

The model finishes by calling the `submit_release_notes` tool; `--output
proposal` POSTs to the platform and prints the review URL (it creates a **draft
+ proposal**, it does not publish).

### `gravity check api`

Pull the documented API blocks for the site and compare them to the source.

```bash
# Diff every OpenAPI spec mapped under sources[] in .gravity.yaml
gravity check api --format github

# Diff one spec of your choice instead
gravity check api --openapi openapi.yaml

# No spec mapped: verify each block's source-binding hash against the repo file
gravity check api
```

- **With specs** (the `sources[]` OpenAPI mappings, or `--openapi`): builds the
  set of spec operations (method+path) and diffs them against the documented
  blocks of that spec's page, producing `undocumented` (in spec, not in docs),
  `orphaned` (in docs, not in spec), and `changed` (summary differs) findings.
- **Without any spec**: for each block whose `sourceBinding` has a `hash` and a
  `ref` that resolves to a file in the repo, recompute the file's sha256 and
  flag `stale` on mismatch. Blocks without a verifiable binding are **skipped
  and counted** (never silently).
- **Scope**: only this repo's blocks — the pages the platform attributes to it,
  or, before any attribution exists, the spaces `.gravity.yaml` declares
  (`--space` overrides). Another repo's API blocks are never reported as orphans.

Flags: `--site`, `--space`, `--openapi <path>`, `--format text|json|github`, `--json`.

### `gravity check docs`

Pull this repo's page snapshots and verify machine/hybrid blocks whose source
binding has a `hash` + repo-resident `ref`, flagging stale mismatches. With
`--ai`, additionally run the docs-gap agent over the `from..to` diff plus the
(bounded) text of this repo's pages; deterministic and AI findings are merged.

```bash
gravity check docs
gravity check docs --ai --from v1.2.0 --to HEAD
```

Only this repo's pages are checked: the ones the platform attributes to it, or,
before any attribution exists, the spaces `.gravity.yaml` declares (`--space`
overrides). It never falls back to the whole site; with neither attribution nor
a manifest it exits `2` and says so. An empty `--ai` range skips the AI pass.

Flags: `--site`, `--space`, `--from`, `--to`, `--ai`, `--format text|json|github`, `--json`.

### `gravity spaces`

Read-only view of the site's content hierarchy: top-level spaces, their
subspaces, each space's collections (page folders) with page counts, the pinned
home pages, and which space this repo's manifest publishes into. For multi-repo
products this is the "who feeds what" inventory — no server-side registry, just
the site tree plus the local `.gravity.yaml` annotations.

```bash
gravity spaces
gravity spaces --json
```

```text
dimonoff — Dimonoff Docs

fundamentum  Fundamentum
└─ connect  Fundamentum Connect   [⌂ overview, ← this repo, shared]
      · device-api  (collection, 12 pages)
      · fleet-svc  (collection, 8 pages)
      1 page
changelog  Changelog   [release notes]
```

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
- **Spaces & hierarchy** — target spaces are ensured idempotently, parent
  first: with `spaces.parent` the default space is created as (or reparented
  into) a subspace, shared-space pages are filed into per-repo collections, and
  `spaces.home` is pinned as the space's home page after authoring. On a
  platform without the `space-hierarchy` capability all of that degrades to
  today's flat sync with a single notice.

Like every write path, `sync` creates a **draft + open proposal** and never
publishes. Authoring exits `0` (ok) or `2` (error) — findings (`1`) stay
exclusive to `check`.

`--space <slug>` (or `GRAVITY_SPACE`) overrides `spaces.default`: every mapping
that names no space of its own lands there; mappings with an explicit `space:`
keep it. `--json` prints the plan and results as JSON (human progress goes to
stderr); with `--dry-run` it prints the payloads that would be posted.

Flags: `--only api|docs`, `--page <slug>`, `--space <slug>`,
`--output proposal|stdout` (default `proposal`), `--dry-run`, `--json`.

### `gravity docs generate` (preview)

Scan the codebase with the AI harness and author documentation for three
audiences — **public**, **users**, and **developers** — as draft proposals.
Where `sync` pushes Markdown you wrote, `docs generate` *writes* the docs from
the code.

```bash
gravity docs generate                      # plan + author all three audiences
gravity docs generate --audiences public   # one audience
gravity docs generate --since $LAST_SHA --ci   # CI: only what changed since <ref>
gravity docs generate --units service,api  # only backend units
gravity docs generate --page overview --dry-run
gravity docs generate --output stdout      # preview the block payloads
gravity docs generate --from .gravity/generated/docs.json  # replay a saved run
```

It runs in two phases: a **plan** pass inventories the repo's documentable
**units** — `feature`, `service`, `system`, `api`, `capability` — and proposes a
page for each (no page cap — coverage is the goal), then a per-page **author**
pass writes that page's blocks. The default unit kind comes from
`product.role` (`api`/`service` → `service`, everything else → `feature`), so a
backend of dozens of services is documented as services with API-first pages,
not as marketing features. After a successful plan the unit inventory is
published to the platform, which is what makes `gravity coverage` measurable;
`--no-inventory` skips that.

The plan pass is given the site's **existing pages** and the repo's configured
**spaces**, so a re-run reuses a page's slug when it still covers that unit —
updating it in place rather than minting a near-synonym and forking the docs —
and files each page in a space that actually exists. A developers-only page is
routed to the repo's dev/api space when it declares one. Pages the plan does not
cover are left published and reported, never silently retired (a page attributed
to a sibling repo is not reported — it is not this repo's to maintain).
Audience is a **per-block** attribute,
so one page can carry public, user, and developer blocks and the platform renders
the ones matching the viewer. Existing pages are read first, so a re-run updates
blocks in place (reusing keys) and proposes removing ones that are gone, rather
than duplicating. Every write is a draft + open proposal.

**The authored set is never lost to a late error.** Before syncing, the whole
block set is saved to `.gravity/generated/docs.json` (override with `--save`), and
each page is authored independently — one page the server rejects doesn't discard
the rest. If a sync fails after the AI has done its (expensive) work, fix the
cause and replay the saved set with `--from <file>`, which skips the AI phases
entirely and just re-runs the sync.

This is a preview gated on platform support (`docs-generate`, `block-audience`)
and server-hosted prompts. Until those ship it reports unavailability and
**exits 0** (`--require` to fail). When block audiences aren't supported yet,
blocks are authored without audience tags and render to everyone.

**`--since <ref>` is the CI mode.** It derives the changed file set from
`<ref>..HEAD`, limits the survey to it, and re-authors only the pages whose units
or bound sources changed — every other page is skipped at **zero** LLM cost. An
empty change set prints `no changes since <ref>` and exits `0`, so a no-op run
never fails a pipeline. Use the previous **successful** run's commit, never
`HEAD~1`, or a failed run turns into a permanent documentation gap. `--since` and
`--from` are mutually exclusive, and a `--since` run publishes its inventory as a
merge (it only surveyed a slice of the repo, so it never deletes units it did not
look at).

Flags: `--audiences <list>` (default all three), `--since <ref>`,
`--units <kinds>`, `--no-inventory`, `--page <slug>`, `--space`, `--site`,
`--output proposal|stdout`, `--dry-run`, `--require`, `--save <file>`,
`--from <file>` (replay a saved set with no AI cost), `--json`.

The author pass sees the current text of each page it rewrites (bounded per
block and per page), the plan pass has a 16k-token output budget, and every
agent is forced to call its submit tool on its final turn instead of running
out of turns empty-handed.

### `gravity coverage`

Report how much of what this repo says it contains is actually documented.
`docs generate` publishes a **feature inventory** — the units its plan pass found
— and `coverage` compares that inventory against the pages the site publishes.

```bash
gravity coverage                      # this repo, text report
gravity coverage --min 0.8            # fail CI below 80%
gravity coverage --all --format json  # every repo publishing to the site
gravity coverage --kind service       # only backend services
gravity coverage --repo github.com/Acme/orbit-web   # a sibling repo
```

Each unit is **documented** (a live page covers it), **stale** (documented, but
the sources it is bound to changed since that page was written), or
**undocumented** (nothing covers it). Stale units still count as documented — they
never fail the bar; drift is `check docs`'s job. A repo that has declared no units
reports 100% (it claims nothing, so nothing is missing). The report also lists
**unclaimed pages**: pages this repo wrote that its inventory no longer mentions.

By default only this repo is reported — a sibling repo publishing to the same
site is its own problem. The bar comes from `coverage.min` in `.gravity.yaml`
unless `--min` overrides it, and every slug in `coverage.require` must exist on
the site. Below the bar is a warning finding, a missing required page is an error
finding; both exit `1`. Exit `0` at or above the bar, `2` on auth/network/config.
Gated on the platform's `coverage` capability as advertised by `/whoami`: absent,
it reports that and exits `0` (`--require` to fail). A 404 is never read as a
missing feature — a mistyped site exits `2` with `site '<slug>' not found` and
the slugs you can use.

Flags: `--site <slug>`, `--repo <remoteKey>`, `--kind feature|service|system|api|capability`,
`--min <0..1>`, `--all`, `--format text|json|github`, `--json`, `--require`.

### `gravity capture`

Trigger the platform's **Doc Agent** to navigate a connected application and
write what it finds back as a draft + open proposal — e.g. after a release, to
refresh the documented UI. The target comes from the space's platform-stored
connection and the run's brief; credentials never leave the platform.

```bash
gravity capture --connection staging --brief "Focus on the new billing screens."
gravity capture --connection staging --brief-file docs/capture-brief.md --async
gravity capture status dar_abc
```

Where the platform has no Doc Agent configured, `capture` reports it and
**exits 0** (safe to add to release CI today); pass `--require` to make absence a
hard error. A run that fails or is cancelled exits `1`.

The flags of the never-built capture contract — `--url`, `--path`, `--capture`,
`--max-pages`, `--auth-secret`, `--attach`, `--release-proposal` — are removed;
passing one exits `2` naming its replacement. `--label` is deprecated and sent as
`--connection`.

### `gravity nucleus`

Nucleus is the Gravity memory service: small titled facts the AI recalls without
re-reading whole documents. A product's repos share one `knowledge.namespace`,
composing a single memory; it travels as an `ns:<namespace>` tag on write and as
a soft ranking hint on recall.

```bash
gravity nucleus query "how do webhooks retry?"   # recall the most relevant memories
gravity nucleus sync                             # distill memories from code changes and contribute them
```

Memories are **idempotent by title** at their scope: contributing the same title
revises that memory instead of creating a duplicate. Where the Nucleus module is
disabled for the organization these commands degrade gracefully (exit 0,
`--require` to fail). Recall also augments `release-notes` and `check docs --ai`
generation, strictly best-effort — it can never break those commands.

### Other commands

- `gravity version` — print the version (the release stamp, or the module
  version for a `go install` build). The same version travels in every request's
  `User-Agent` (`gravity-cli/<version> (<os>/<arch>)`).
- `gravity init` — write `.gravity.yaml` for this repo. Runs an interactive
  wizard (site, product/multi-repo identity and role, default space) that
  **detects OpenAPI specs and documentation Markdown and offers to map them**, so
  `gravity sync` has real `sources`/`documents` to author: `README.md` and
  `docs/**` are preselected, other Markdown is offered unticked, and
  `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING*`, `CODE_OF_CONDUCT*`, `LICENSE*`,
  `SECURITY*`, `CHANGELOG*`, `.github/**`, `node_modules`, `vendor` and `dist`
  are never offered. When you're signed in it **lists your sites and the chosen
  site's spaces to pick from**; offline it falls back to free-text entry. It
  never creates anything on the platform — `gravity sync` creates the spaces.
  `--yes` (or `--ci`, or a non-interactive shell) skips the questions and maps
  what detection found (no network calls); `--dry-run` prints the file instead of
  writing it; `--migrate` upgrades an existing file in place **losslessly**
  (every section survives; keys removed in v0.3 are dropped and reported). The
  API URL is recorded only when you pass `--api-url` (self-hosted). Refuses to
  clobber an existing file unless `--force`.
- `gravity auth login` — store the token in `~/.config/gravity/config.yaml`
  (`0600`). Pass `--token <sk>` (or set `GRAVITY_TOKEN`), or run it bare on a
  terminal to be prompted with **hidden input** so the secret never lands in your
  shell history. Re-run it any time to **change** the stored token (it confirms
  before replacing one and preserves a custom `--api-url`); after saving it
  verifies the token against `/whoami`.
- `gravity auth status` — show whether a token is configured, where it resolved
  from (flag / env / user file), and verify it live (`/whoami`). Exits `2` when a
  configured token is rejected or the gateway is unreachable.
- `gravity auth logout` — remove the stored credentials file (clear or rotate the
  token); warns if `GRAVITY_TOKEN` is still set in the environment.
- `gravity doctor` — validate `.gravity.yaml`, print the resolved configuration
  (and where each value came from, or `not set — <how to set it>`), then check
  token/org/model/provider-key via `/whoami` + `/llm/v1/config`. Exits `2` on a
  config, auth, or network failure.
- `gravity ping` — send a one-shot setup handshake to `/api/v1/setup/ping` so the
  Gravity web app (after guiding you through install + `gravity init`) can confirm
  the token works, **and register this repo against the site it publishes to**. It
  reports the CLI version/platform, the `.gravity.yaml` connection config (API
  URL, site, space), the repo's name/remote/branch/commit, the fully-resolved
  manifest and its raw text (neither can carry a token — `.gravity.yaml`
  structurally cannot hold one), and a counts-only summary of what this repo
  documents. No documentation is written; the server echoes back your
  organization, the key hint, the default site, this repo's registration, the
  sibling repos on the same site, and the platform's capability flags. Add
  `--json` for the full request + response. Exits `2` on an auth or network
  failure.
- `gravity repos` — list the repos publishing to this site: this one (with its
  registration id, first-seen date and branch) plus its **siblings** — the other
  repos in the organization whose docs land on the same site, each with the spaces
  it declares, the collections its pages are filed under, when it last pinged and
  last wrote, and its CLI version. It runs the same handshake `ping` does, so it
  also refreshes this repo's registration. `--json` for the machine shape. On a
  platform that predates the repo registry — or in a checkout with no git remote
  **and** no `product.slug`/`product.repo` — it says so and lists no siblings.
  Exits `2` on an auth or network failure.

## Exit codes

| Code | Meaning  |
| ---- | -------- |
| `0`  | success / no findings |
| `1`  | findings (drift, gaps, stale bindings, coverage below the bar) |
| `2`  | error (auth, network, bad input, unknown site or space) |
| `3`  | license refusal — the workspace's licence doesn't include the module this command needs (`module_disabled`, e.g. the `cli` module) or its seats are used up (`seat_limit`). The message names the module; ask a workspace administrator. |

Errors read as sentences, not dumps: a non-JSON error page is summarized to its
status and title or first line, git failures are humanized (`not a git
repository — run inside your repo`, `unknown git ref "v9" — … fetch full
history`), and an empty commit range is a no-op that exits `0`.

## CI

See [`ci/README.md`](ci/README.md) for ready-to-use GitHub Actions, GitLab CI,
and Bitbucket Pipelines snippets. `GRAVITY_TOKEN` is the secret; everything else
comes from `.gravity.yaml`.

`--ci` is a global flag every command accepts, and it turns on by itself when
`CI=true` (every major CI sets it): no prompts, plain ASCII output with no emoji
or tree glyphs. `--json` is accepted by every reporting command (`ping`,
`repos`, `spaces`, `release-notes`, `sync`, `docs generate`, `check`, `coverage`,
`capture`, `nucleus query`).

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
internal/pathsafe      repo-root path validation shared by the above (leaf, stdlib-only)
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
