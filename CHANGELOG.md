# Changelog

## v1.0.0 — Unreleased

Clean break (R12): the v0.x command set and the v1 manifest are removed. This
entry tracks milestone C1 (CLI core); `run`, `preview` and `check` land with
the pass engine.

### Added

- New command tree: `login` (device flow, `--with-token`), `logout`, `whoami`,
  `init` (connect + minimal `version: 2` manifest), `status`, `passes`
  (`list`, `show`, `edit`), `explain`, `version`. `run`, `preview` and `check`
  are registered and exit `2` until the pass engine ships.
- Global flags `--profile`, `--api-url`, `--token`, `--manifest`, `-C`,
  `--json` (one envelope on stdout), `--no-color`, `-q`, `-v`.
- Profiles in `~/.config/gravity/profiles.yaml`; the v0.x `config.yaml` token
  is copied once into profile `default` and the old file is never modified.
- Manifest v2: strict parsing, embedded JSON Schema, did-you-mean for unknown
  keys, `kind` required on every pass, v1 detection.
- REST client for the CLI 1.0 contract (auth, repos, plan, status, runs,
  content, bundle writes, inventory, hints, Nucleus, gateway run context),
  error envelope with details, retries with backoff on `429`/`5xx`, versioned
  `User-Agent` (`gravity-cli/<v> (<os>; <arch>)`).
- CI provider detection (GitHub, GitLab, Bitbucket, Azure, Jenkins, CircleCI,
  generic) with `GRAVITY_*` overrides.
- Range resolution per trigger (watermarks, stale head, survey, release tags,
  shallow-clone deepening) and the ChangeSet (commits, files, renames, unit
  mapping through source refs, deterministic OpenAPI diff, removed/renamed
  symbols).

### Security

- A profile token is only sent to the host that issued it. When `--api-url`,
  `GRAVITY_API_URL` or a manifest `apiUrl` points elsewhere, the command exits
  `2` (`token_host_mismatch`) before any request; a cloned repository can no
  longer redirect a stored token. `--token`/`GRAVITY_TOKEN` may target any host.
- `gravity init` refuses to start the browser sign-in against a `.gravity.yaml`
  `apiUrl` other than the default host (`manifest_api_url`); name the host
  yourself with `gravity login --api-url <url>`. `logout` revokes a token only
  on the host that issued it, whatever `--api-url` says.
- API URLs must be https; plain http is accepted for loopback hosts only.
- A `token_to_manifest_host` warning fires when `--token`/`GRAVITY_TOKEN` is
  sent to a manifest `apiUrl` that is not the default host.

### Removed

- `auth *`, `doctor`, `ping`, `repos`, `spaces`, `sync`, `docs *`,
  `release-notes`, `check api|docs`, `coverage`, `capture`, `nucleus *`:
  invoking one prints its replacement and exits `2`.
- The global `--ci` and `--site` flags; CI mode follows `CI=true`.
- The v1 manifest (`site`, `spaces`, `sources`, `documents`, ...): CLI 1.x
  refuses it outside `gravity init`.

## v0.3.0 — Unreleased

Stop-the-bleeding release for current v0.2 users: no new architecture, every
fix below is backward compatible with a valid v0.2 `.gravity.yaml` except the
two removed keys (run `gravity init --migrate` to drop them).

### Fixed

- `release-notes` posts into `releaseNotes.space` (default `changelog`) instead
  of `spaces.default`; `--space` overrides it (`GRAVITY_SPACE` does not apply).
  Before the agent runs, a missing target space is created as a
  `release-notes` space, or the command fails early with a clear message when
  the token cannot create it. `--output file` writes `releaseNotes.changelog`
  (default `CHANGELOG.md`) unless `--changelog` is given.
- `sync` honours `--space` and `GRAVITY_SPACE` as an override of
  `spaces.default`; mappings with their own `space:` keep it.
- A mistyped site is no longer "not yet available; skipping" with exit `0`: a
  404 reports `site '<slug>' not found` (or `space '<slug>' not found`) with the
  slugs you can use, and exits `2`. Feature availability comes only from the
  features `/whoami` advertises.
- Non-JSON error bodies (HTML gateway pages, plain text) are summarized to the
  status and the page title or first line instead of being dumped.
- Raw git stderr is humanized: `not a git repository — run inside your repo`,
  `unknown git ref "<ref>" — … fetch full history`, `this repository has no
  commits yet`.
- An empty commit range is a clean no-op (`no commits in range …; nothing to
  do`, exit `0`) for `release-notes`, `nucleus sync` and `check docs --ai`, and
  spends no LLM call.
- `check api` diffs the OpenAPI specs mapped under `sources[]` when `--openapi`
  is absent, and both `check api` and `check docs` are scoped to this repo's
  pages: `--space` when given, else the pages the platform attributes to this
  repo, else the spaces `.gravity.yaml` declares (pages another repo writes are
  left out). `check docs` never falls back to the whole site.
- The docs-gap agent and the docs author receive the actual (bounded) text of
  the pages they judge or rewrite, not just titles and block types.
- The agent loop forces the terminal submit tool on its final turn, after the
  model ends a turn without submitting, and after the tool-call budget runs out,
  so a run no longer ends empty-handed at its cap. A model that rejects a forced
  `tool_choice` is retried once with an explicit instruction instead.
- The docs-plan phase gets a 16k-token output budget (was 4k, which truncated
  large inventories).
- `read_file` reads files at the end of the range under review (`--to`) instead
  of always at `HEAD`.
- `doctor` and `auth status` no longer print `(unset) (unset)`; unset values say
  `not set` and how to set them.
- `init --migrate` is lossless: `spaces.declare`, `discovery`, `i18n`,
  `coverage`, `knowledge`, collections and document versions all survive. It
  converts the legacy top-level `space:` and reports every key it drops. The
  file is re-rendered, so YAML comments are not kept (the command says so).
- `init` and `init --migrate` no longer set `releaseNotes.space` to the docs
  space, and a legacy top-level `space:` no longer retargets release notes.
- The default release-notes range skips the `docs-synced` CI marker tag: it
  starts at the previous `v*` release tag (any other tag only when there is
  none).
- `--ci` plain output applies to human-readable text only; `--json`,
  `--format json|github` and `--output stdout` payloads are written unchanged.
- `.gravity.yaml` YAML merge keys (`<<: *anchor`) are accepted by the strict
  parser.
- `docs generate --json` still prints its report when every planned page
  fails.

### Changed

- A licence refusal (`module_disabled`, `seat_limit`) exits `3` with a message
  that names the module and says to ask an administrator, distinct from an auth
  or network error (`2`).
- `.gravity.yaml` is parsed strictly: an unknown key fails with its line and a
  "did you mean" suggestion.
- Removed manifest keys `sources[].generator` and `knowledge.scope` (neither was
  ever used); they now fail with a clear message. `kind: code` is no longer
  advertised by the scaffold.
- `init --yes` detects OpenAPI specs and documentation Markdown (`README.md`,
  `docs/**`) and maps them, skipping `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING*`,
  `CODE_OF_CONDUCT*`, `LICENSE*`, `SECURITY*`, `CHANGELOG*`, `.github/**`,
  `node_modules`, `vendor` and `dist`. The wizard no longer asks for the API URL
  (pass `--api-url` for a self-hosted platform), explains what `role` does,
  preselects only `README.md` and `docs/**`, and no longer creates spaces on the
  platform — `sync` does. New `init --dry-run` prints the file without writing.
- `--ci` is one global flag on every command, also switched on by `CI=true`:
  never prompt, plain ASCII output without emoji or tree glyphs. The per-command
  `--ci` flags are gone.
- `--json` is accepted by every reporting command: new on `release-notes`,
  `sync` and `docs generate`; a shorthand for `--format json` on `check`,
  `coverage`, `capture` and `nucleus query`.
- One version variable (`internal/version`), stamped at build time with a
  `debug.ReadBuildInfo` fallback for `go install` builds, and sent in a
  versioned `User-Agent` (`gravity-cli/<version> (<os>/<arch>)`).

### CI templates

- The GitHub composite action downloads the prebuilt release binary through
  `install.sh` (`version` input, default `latest`, cached per release) instead of
  installing Go and building on every run. `--format` is appended only for
  `check api`, `check docs` and `coverage`; `api-url` and `site` default to empty
  so `.gravity.yaml` wins. `version: source` builds from the action checkout.
  Windows runners install through `install.ps1`.
- The GitLab and Bitbucket templates stop hardcoding `GRAVITY_SITE`,
  `GRAVITY_API_URL` and `--space changelog`, install the release binary, and
  move the `docs-synced` marker after a successful generate so later runs stay
  incremental. GitLab pushes the marker with `-o ci.skip` and runs
  `release-notes` only for `v*` tags.

## v0.2.2

- Reject the marketing site as the API URL.
- Default to the API host, `api.gravitydocs.io`.
