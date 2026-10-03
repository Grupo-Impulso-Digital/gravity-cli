# Changelog

## v1.0.4 — Unreleased

### Fixed

- The guides pass no longer warns that a page "already has an open change"
  when the only pending change on it comes from this repository and pass,
  which the platform supersedes. It reads the open change request's
  `pending` list (repository, pass, run): another repository or pass is named
  in the warning, this pass's own earlier change gets a quiet "replaces the
  pending change" note, and an empty list says nothing. Against servers
  without the list, the previous behaviour stays.

## v1.0.3 — 2026-10-03

### Changed

- The GitHub action takes the repository token as `repo-token` and a user or
  organization token as `token`, and exports them as `GRAVITY_REPO_TOKEN` and
  `GRAVITY_TOKEN`; the template passes `secrets.GRAVITY_REPO_TOKEN` and
  `secrets.GRAVITY_TOKEN` separately, so `whoami` and `status` name the
  secret that was used. Both inputs are optional.
- When every token variable holds an unexpanded reference, a local run falls
  back to the profile with a warning; CI still fails with `token_unresolved`.
- The GitLab template also runs on `web` and `api` pipelines of any branch.
- This repository's `cli-guides` pass targets the `guides` collection of the
  CLI space, for task-oriented guides beside the curated reference pages it
  used to edit. It stays disabled until that collection exists and the target
  is approved in the app.

### Fixed

- `gravity status` names the token actually used: an environment variable
  that won over a stored profile is no longer reported as the profile.
- `gravity preview` in a Dependabot job is skipped with
  `dependabot_no_token` and exit `0`, like `run` and `check`.
- `gravity logout` trims token variables and skips unexpanded references, like
  every other command.
- The `--token` help, the `token_host_mismatch` error and the manifest
  `token:` error name `GRAVITY_REPO_TOKEN` and `GRAVITY_TOKEN` in resolution
  order.
- The "no pass documents it" and drift details describe the rule the check
  applies (a reference pass that runs on push, or on this branch).
- A pending change from an earlier run of the same pass and repository is
  reported as replaced (the platform supersedes it), not as competing.
- `.markdown` files count as documentation, not code, in claim review.
- ci/README describes manual starts per provider and how each template passes
  the token, as the templates do.
- `gravity status` flags gravity CI files written by 1.0.0 to 1.0.2 that pass
  only `GRAVITY_TOKEN` (or both secrets through one input), with the lines to
  change; re-running `gravity init` rewrites such a file when it is unedited.
  See "Upgrading CI files from 1.0.0-1.0.2" in the README.

## v1.0.2 — 2026-10-03

### Changed

- Repository tokens live in `GRAVITY_REPO_TOKEN`. The CLI reads `--token`,
  then `GRAVITY_REPO_TOKEN`, then `GRAVITY_TOKEN` (user and organization
  tokens, and the gravity 0.x name), then the profile; a variable that holds
  an unexpanded reference is skipped. `whoami` and `status` name the variable.
- `gravity init` stores the minted token as `GRAVITY_REPO_TOKEN` and never
  touches `GRAVITY_TOKEN`, so a gravity 0.x pipeline keeps working until it is
  removed; init only names such pipelines. `--replace-secret` is now a hidden
  no-op that warns.
- The CI templates pass `GRAVITY_REPO_TOKEN` with the `GRAVITY_TOKEN`
  fallback (GitHub `secrets.GRAVITY_REPO_TOKEN || secrets.GRAVITY_TOKEN`, both
  variables on Azure, credential `gravity-repo-token` on Jenkins); the action
  documents the same expression.
- The Bitbucket template adds a `gravity-manual` custom pipeline, and the
  CircleCI snippet passes `pipeline.trigger_source`.

### Fixed

- The manual-run summary names the passes that were eligible, which wrote,
  which ran without changes, which were skipped and why, and which failed,
  instead of calling every eligible pass "ran".
- `gravity run --pass <check pass>` no longer reports operations as
  undocumented when an unselected reference pass documents them: coverage and
  drift count every enabled pass that would document the unit.
- GitHub runs started by Dependabot (no Actions secrets) are skipped with a
  notice and exit `0`, like fork pull requests, instead of exiting `4`.
- Manual starts are detected on Jenkins (`BUILD_CAUSE`/`ROOT_BUILD_CAUSE`
  user and timer causes, `BUILD_USER_ID`) and CircleCI
  (`CIRCLE_PIPELINE_TRIGGER_SOURCE` `api` or `scheduled_pipeline`);
  `GRAVITY_TRIGGER=manual` remains the override everywhere.
- A workflow calling the shared `gravity-docs.yml` is no longer treated as a
  0.x pipeline: init keeps it instead of adding `gravity.yml`.
- A `401` inside a pass stops the run without its finish call and exits `4`,
  like every other credential failure.
- A rejected token adds "run `gravity login` to sign in again" locally with a
  profile token, and names the variable when the token came from one.
- The pull request comment's "N notes." line no longer renders as a table
  row when there are no findings.
- Claim review takes locked verbatim pages whose file the range changes
  first, and reviews them even when only Markdown changed.
- A check pass's `annotate: false` keeps its findings out of the CI
  annotations.
- An unstamped build reports `dev+<commit>` instead of `dev`, the action's
  source build stamps `git describe`, and `status` shows this CLI's version
  next to the last one the platform saw.

## v1.0.1 — 2026-10-03

Hardening of 1.0 after a documentation audit and the first production runs.

### Fixed

- A local `gravity run` (and a GitHub `workflow_dispatch`) no longer ends
  silently with nothing written. With the matching platform release a manual
  run runs the passes triggered on `manual`, `push` or `schedule`, and
  `gravity run --pass <name>` runs that pass whatever its triggers; the CLI
  names the passes a manual run left out, and fails with `pass_not_applicable`
  and a hint when an older server still refuses a named pass. An unknown
  `--pass` fails with `pass_unknown` instead of skipping everything.
- `gravity run` warns about uncommitted changes: a run reads committed history
  only (`gravity preview` reads the working tree), and its help now says so.
- `gravity check` no longer fails every pull request that changes a documented
  API operation. Drift compares api blocks with the pull request's base and
  head: a change that a push-triggered reference pass will apply on merge is a
  note, drift that predates the pull request is a warning, and only a change
  nothing will follow fails (`drift` stays in the default `failOn`).
- The built-in check now covers coverage, as documented: new operations that no
  pass will document are reported (warnings, or failures with
  `--fail-on coverage`), with the share of documented units as a note. Help,
  README and this changelog say exactly what the built-in check covers.
- Missing credentials are an error, not a pass: an empty `GRAVITY_TOKEN` in CI,
  a signed-out local `gravity check`, a literal `$(GRAVITY_TOKEN)` (an undefined
  Azure variable) or another unexpanded reference, and a token the server
  rejects (`401`) exit `4` (`token_missing`, `token_unresolved`,
  `unauthorized`). Only a fork pull request, detected per provider, skips with
  exit `0` and a notice.
- `code.include` is honoured: passes without `scope.paths` react to changes
  under it (plus `code.openapi`), and symbols are read only there.
- The survey depth set on the repository in the app
  (`capabilities.limits.surveyMaxCommits` in the plan) is used for passes
  without a watermark, unless a pass sets `surveyCommits`.
- `gravity logout` names the right screen for repository and organization
  tokens: Settings › CLI & machines, with its link; app links derived from the
  API host now also work for hosts like `api.gravity.<domain>`.
- `gravity passes --trigger manual` shows what a manual write run does, instead
  of the preview rule that applies every pass.
- `gravity passes edit` links the manifest on the repository's own host
  (GitHub, GitLab, Bitbucket, Azure DevOps, including Azure SSH remotes) and
  prints the file and branch for other hosts instead of a GitHub-style link.
- `gravity init` no longer replaces a `GRAVITY_TOKEN` that a gravity 0.x
  pipeline still uses (0.x refuses repository tokens): it detects 0.x CI files
  and v1 manifests, keeps the secret and prints the new token to set when the
  migration is merged. `--replace-secret` replaces it anyway; without a terminal
  init mints nothing it could not show.
- AI-authored list blocks with a variant the platform does not know
  (`unordered`, `ordered`, ...) are normalized to `bulleted`, `numbered` or
  `task`, and heading levels are clamped, so guides pages are no longer refused
  with `Invalid content for list block`. The tool schema names the variants.
- Nucleus atoms with an unknown kind are written as `other` instead of being
  refused.
- Cross-repository hints that no repository can receive are dropped with a
  warning instead of failing the whole batch (`has no recipient repository`);
  `forRepos` is limited to the product's repositories, and a refused batch is
  retried hint by hint.
- `gravity check` reviews a locked verbatim page whose file the pull request
  changes against the file as it will be imported, not the stale page, so a
  pull request that fixes a contradiction no longer fails on it.
- The unit mapper retires units whose surface the repository no longer
  provides (commands kept only to print a removal notice are not units).
- `docs/platform-authoring-api.md` describes the 1.x client instead of 0.x.
- The v0.3.0 entry below said the action's `version` input defaulted to
  `latest`; it defaults to `0`.

### Changed

- New exit code `4` for missing, unresolved or rejected credentials; the
  action's `exit-code` output documents it.

## v1.0.0 — 2026-10-02

Clean break: the v0.x command set and the v1 manifest are removed, and every CI
provider runs one step, `gravity run`. This entry covers milestones C1 (CLI
core), C2 (passes), C3 (setup UX) and C4 (multi-repository intelligence and
CI). It is published only after the major-pinned v0.3.x release, so v0.3
pipelines never pick it up by accident.

### Upgrading from 0.x

- Pipelines on `ci/github@v0`, `GRAVITY_VERSION=0` or the 0.x templates keep
  installing the newest 0.x release.
- Run `gravity init` in the repository: it converts `.gravity.yaml` to version
  2 once (the original is kept as `.gravity.v1.yaml.bak`), registers the
  passes, mints a repository token with the right scopes, writes the
  single-step CI file and installs the secret.
- Prefer the repository token (`gr_repo_…`) that init mints over a v0.x
  organization key in CI: it carries only the scopes its passes need.

### Added

- Multi-repository ownership (C4). Units are product-wide and any pass whose
  change touches a unit may update the blocks bound to it, whichever repository
  wrote them last: guides now reach pages outside their target through units
  the repository declares or implements (updates of bound blocks only). Every
  authored block carries provenance (commits and source refs), and repositories
  that only document a unit are recorded with the `documents` role.
- Claim verdicts are settled against the product inventory, block writers and
  Nucleus: `true-elsewhere` (with the owning repository as evidence),
  `unverifiable` (a soft note), `contradicted-here` (a finding); a
  contradiction about behaviour another repository implements becomes a
  cross-repository hint for that repository.
- Handoffs: pull requests predict them from the API units the change adds or
  removes; write runs report what the platform detected on ingest. Competing
  changes (another run's open proposal on the same blocks) are listed in the
  run output, the step summary and the pull request comment.
- `gravity explain <page>`: per block the last writer (repository, pass,
  commit, run), the earlier writers, the ownership, and the page's repository
  lock with a link to the file.
- Pull request comments on GitHub, GitLab (`GITLAB_TOKEN`), Bitbucket
  (`BITBUCKET_ACCESS_TOKEN`) and Azure DevOps (`SYSTEM_ACCESSTOKEN`), upserted
  by a per-repository marker and created only once a pull request has impact.
  Findings become GitHub workflow commands, Azure logging commands or a GitLab
  Code Quality report (`gl-code-quality-report.json`); `--annotate` on `run`
  and `check`.
- Every CI run writes a summary: GitHub step summary (pushes and releases too),
  Azure build summary, `gravity-report.md` elsewhere. The GitHub action
  receives `run-url` and `exit-code` outputs.
- The 1.0 GitHub action (`ci/github@v1`): one step that resolves the version,
  caches the prebuilt binary per release, installs it with `install.sh` (or
  `install.ps1` on Windows) and runs `gravity run`, `check` or `status`.
  Inputs `token`, `command`, `args`, `version` (default `1`),
  `working-directory`, `api-url`.
- Single-step templates for GitLab (with the Code Quality artifact), Bitbucket
  and Azure under `ci/`, identical to what `gravity init` writes.
- `install.sh` and `install.ps1` resolve `GRAVITY_VERSION=<major>` to the
  newest release of that major and default to `1`; `latest`, `v1.2.3` and
  `1.2.3` still work. `GRAVITY_RESOLVE_ONLY=1` prints the tag without
  installing.

- `gravity init` (C3): local detection (remote, languages, OpenAPI documents,
  UI and server routes, docs folders, runbooks, releases, CI provider), at most
  three questions (product, passes with the target site chosen inside the
  question, write), a preview of every file, app-first pass registration or
  `--passes-as-code`, missing spaces created through `createTargets`, a
  repository token minted with exactly the scopes of its passes, the CI file
  for GitHub, GitLab, Bitbucket or Azure (snippets for Jenkins and CircleCI),
  and the secret installed with `gh`/`glab` on stdin or printed once to paste.
  `--yes`, `--ci`, `--no-secret`, `--dry-run`, `--repo`, `--app-passes`.
  A developer without `docs.repos.manage` gets the pre-registration hint
  without being asked anything.
- One-shot v1 conversion inside `gravity init`: every live v1 key is mapped or
  reported, converted verbatim passes carry `adopt: true` and per-file
  slug/title overrides, the original is kept as `.gravity.v1.yaml.bak`, and
  converting the output again is a no-op.
- `gravity status` adds the profile and token expiry, locked (repo-managed)
  passes, and capability warnings: expiring or expired token, missing modules,
  missing token scopes, no AI provider, server features a pass needs.
- `gravity login --token <token>` stores a token headlessly; the device flow
  shows a spinner while it waits for approval.
- Terminal UI: `charmbracelet/huh` prompts (accessible mode with
  `ACCESSIBLE=1`), live per-step progress (`bubbletea`) for `init`, `run`,
  `preview` and `check`, and a summary card with links (`lipgloss`). Plain
  output for `--json`, `CI=true`, `NO_COLOR` and non-terminals.

- `gravity run`: connect, plan (dry runs overlay the branch's manifest by
  hash), per-pass ranges with `stale_head`, one ChangeSet per distinct base,
  zero-cost scope skips and the no-run rule, `POST /runs` with lease waiting
  (`--lease-timeout`) and re-planning on `plan_stale`, heartbeats, inventory
  ingest v2 with `roles[]` (authoritative and release runs; batches plus a
  closing `entries` call), passes in plan order (`--parallel N`, never two
  passes on one space at once), pass reports, finish. `lease_lost` stops the
  run without a finish call.
- Pass kinds: `guides` (impact from unit bindings, search and hints; an AI plan;
  block-level edits that cite commits, never touch human blocks or locked
  pages, deprecate instead of deleting; cross-repo hints), `reference`
  (deterministic api blocks with endpoint bindings and canonical unit keys,
  upgrades v0.3 pages in place, explicit removals, optional AI prose),
  `verbatim` (Markdown/MDX import with front matter, admonitions, GitHub
  alerts, MkDocs, details, MDX tabs, mermaid, tables, task lists, footnotes,
  images uploaded once per run, intra-repository links rewritten, folders to
  collections, whole-page re-import on hash change, deletion proposals,
  translated files such as `guide.fr.md` or `lang: fr` uploaded as the
  source page's language version after every source page),
  `changelog` (release pages and the Unreleased page), `nucleus` (namespaced,
  repository-tagged atoms), `check` (drift, coverage, claim verdicts, verbatim
  contradictions, PR notes) and `capture` (waits for the server Doc Agent run).
- One agent harness for every AI pass: forced submit on the last turn, a token
  budget per pass, `read_file` at the range head or any ref (the working tree
  for previews), and doc tools `read_page`, `search_docs`, `recall_nucleus`,
  `product_inventory`, `list_target`. Hosted pass prompts fall back to baked
  copies.
- `gravity preview`: every pass as a dry run over the working tree, with page
  diffs, the composed instruction layers and the cost.
- `gravity check`: the PR gate, with a built-in drift and coverage check when
  no check pass is declared, the doc-impact report, annotations and the
  upserted PR comment, and exit `1` on findings in `--fail-on`. Fork pull
  requests without a token exit `0` with a warning.

- New command tree: `login` (device flow, `--with-token`), `logout`, `whoami`,
  `init`, `status`, `passes`
  (`list`, `show`, `edit`), `explain`, `version`.
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

### Changed

- `.github/workflows/docs.yml` (this repository's own docs) is one step on
  pushes, tags and pull requests; this repository's `.gravity.yaml` is version
  2 with passes-as-code.
- The release workflow refuses a `v1.*` tag until a major-pinned v0.x release
  exists, and moves the `v1` tag the action is referenced by.
- `gravity logout` with `--token`/`GRAVITY_TOKEN` holding a user token revokes
  that token on the host it is used against and removes any profile holding
  it; repository and organization tokens are refused with a warning
  (`token_kind_unsupported`), since they are revoked in the app.
- Reference pages per OpenAPI tag are titled with the tag's `x-displayName`,
  else the tag name in title case (`payment_methods` → `Payment Methods`).
- `gravity init --yes` in an organization with exactly one product joins it
  when nothing points to another product, instead of creating a new one.
- On GitLab and Bitbucket, init's preview and closing summary name the comment
  token to create (`GITLAB_TOKEN`, `BITBUCKET_ACCESS_TOKEN`); both templates
  keep `gravity-report.md` as a job or step artifact.

### Removed

- The v0.x GitLab template and Bitbucket pipe (`ci/gitlab/.gitlab-ci.yml`,
  `ci/bitbucket/pipe`), and the action's `site`, `format`, `since` and
  `continue-on-findings` inputs.
- `auth *`, `doctor`, `ping`, `repos`, `spaces`, `sync`, `docs *`,
  `release-notes`, `check api|docs`, `coverage`, `capture`, `nucleus *`:
  invoking one prints its replacement and exits `2`.
- The global `--ci` and `--site` flags; CI mode follows `CI=true`.
- The v1 manifest (`site`, `spaces`, `sources`, `documents`, ...): CLI 1.x
  refuses it outside `gravity init`, which converts it.

## v0.3.0 — 2026-10-02

Stop-the-bleeding release for current v0.2 users: no new architecture, every
fix below is backward compatible with a valid v0.2 `.gravity.yaml` except the
two removed keys (run `gravity init --migrate` to drop them).

Pins every 0.x pipeline to major 0 before CLI 1.0 ships (milestone C0.1). CLI
1.0 refuses v0.x manifests and commands, so a pipeline that installs `latest`
would break the day 1.0 is published.

### Action required

- GitHub Actions: reference the action as
  `Grupo-Impulso-Digital/gravity-cli/ci/github@v0` (not `@main`, not a
  branch). `@v0` now moves with every 0.x release.
- GitLab and Bitbucket: copy the updated template, or set
  `GRAVITY_CLI_VERSION` to `0` (or a release such as `v0.3.0`) if you set it
  to `latest`.
- Scripts calling `install.sh` or `install.ps1` with `GRAVITY_VERSION=latest`
  should use `GRAVITY_VERSION=0`.

### Changed

- `install.sh` and `install.ps1` default to `GRAVITY_VERSION=0` and resolve a
  major version (`0`, `v0`) to the newest release of that major; `latest`,
  `v0.3.0` and `0.3.0` still work. `GRAVITY_RESOLVE_ONLY=1` prints the
  resolved tag without installing.
- The GitHub action's `version` input defaults to `0` and resolves it through
  `install.sh`. The GitLab template and the Bitbucket pipe default
  `GRAVITY_CLI_VERSION` to `0` and re-resolve a major version on every run.
- The release workflow moves the `v<major>` tag (`v0`) on every non-prerelease
  tag, so `ci/github@v0` exists and follows 0.x.
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

### Fixed

- A release range with no earlier tag starts at the root of history instead of
  the first commit: the first commit's changes are included, and a tag on the
  root commit no longer resolves to an empty `root..root` range.
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

### CI templates

- The GitHub composite action downloads the prebuilt release binary through
  `install.sh` (`version` input, default `0`, cached per release) instead of
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
