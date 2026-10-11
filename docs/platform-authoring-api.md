# Platform contract: the API `gravity` 1.x speaks

This is the CLI-side view of the platform endpoints the `gravity` 1.x binary
calls. The canonical contract is the platform repository's
`docs/pipelines/spec.md`; this file mirrors the parts the CLI uses and adds the
CLI's side of each exchange: which command sends what, and what it does with
the answer. The typed requests and responses live in `internal/api`.

**Audience:** CLI contributors and the Gravity platform team.
**Consumers:** `login`, `logout`, `whoami`, `org`, `setup`, `show`,
`validate`, `structure`, `run`, `review`, `runs`, `approve`, `ci`, `status`,
`check` and `explain`. CLI 1.1 has no stubs for removed commands: an unknown
command is cobra's usage error.

## Conventions

- **Base and auth.** Every call goes to `<apiUrl>/api/v1/...` (the LLM gateway
  to `<apiUrl>/api/llm/v1/...`) with `Authorization: Bearer <token>`. A token is
  a repository token (`gr_repo_…`), a user token (`gr_user_…`) or an
  organization key (`sk_live_…`), read from `--token`, `GRAVITY_REPO_TOKEN`,
  `GRAVITY_TOKEN` or a profile, in that order (`auth.Resolve`). Variable
  values are trimmed, and a value that is an unexpanded reference such as
  `$(GRAVITY_REPO_TOKEN)` is skipped for the next source. When only such
  references are set, CI fails with `token_unresolved`; a local run uses the
  profile token, if there is one, with the warning `token_unresolved_ignored`.
  `whoami` and `status` name the source actually used. A repository token names its
  repository; a
  user or organization principal names it with `?repo=<remoteKey>` on the
  `repos/self` and `runs` routes (`repoParam` in `internal/cli/app.go`), and
  ingest then names the product with `?product=<slug>`.
- **User agent and version.** `gravity-cli/<version> (<os>; <arch>)`. The
  version also travels as `cli.version` on connect (what `status` shows as the
  last CLI) and on `POST /runs`. Release builds are stamped by GoReleaser; an
  unstamped build reports `dev+<commit>`.
- **Retries and deadlines.** A `429` or `5xx` is tried up to three times in all, waiting
  `Retry-After` (or the envelope's `retryAfter`) when given, else 1 s doubling,
  capped at 60 s. A network error is retried for `GET` and for model calls (`POST /api/llm/v1/messages`); other writes fail on the first one.
  Every call has a 120 s deadline except model calls: each attempt of a model
  call may take 15 min (`api.DefaultLLMTimeout`), and its `429`, `5xx` and
  network errors are retried, honoring `Retry-After`, until the next wait would
  take the call past 30 min in total (`api.DefaultLLMRetryCap`).
- **Error envelope.** A non-2xx body is
  `{"error":{"code","message","details":[{path,code,message}],"module","retryAfter","holder"}}`,
  decoded into `*api.APIError`. `details` may instead be an object (the model
  errors send `{reason, param, requested}`); it is kept in `APIError.Fields`
  and read with `Reason()`, `Param()` and `Requested()`. A body that is not the envelope (an HTML gateway
  page, plain text) is summarized to its status and page title or first line.
  `errors.Is` matches the sentinels `ErrLeaseLost`, `ErrRunNotRunning`,
  `ErrLeaseHeld`, `ErrPlanStale`, `ErrNotConnected` (by envelope code) and
  `ErrUnauthorized` (any `401`).
- **Licence refusals.** A `403` with `module_disabled` (and `module`) or
  `seat_limit` becomes `*api.ModuleDisabledError` / `*api.SeatLimitError`; they
  are not auth errors, and the command exits `3`.
- **404 means "not on this server" only for the 1.1 endpoints.** A `404` whose
  envelope code is empty or `not_found` (or a `405`) on `repos/self/validate`,
  `repos/self/approvals`, `repos/self/runs`, `runs/{id}/cancel`, `structure` or
  `structure/apply` is an older server (`api.IsUnsupported`) and the command
  degrades as listed below. Everywhere else a `404` is an error;
  `repo_not_connected` adds the hint to run `gravity setup`.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| `0` | Success, or nothing to do; a fork or Dependabot pull request without a token. |
| `1` | Findings: `validate` errors (also before a real run and after a dry run), `check` findings in `failOn`, structure conflicts, refused approvals, `ci check`, `status --check` on an unhealthy repository. |
| `2` | Operational error: network, bad input, invalid manifest, missing target, failed pass, unknown or inapplicable `--pass`, lease timeout, a stale or dirty branch, a recording that no longer matches. |
| `3` | Licence refusal (`module_disabled`, `seat_limit`). |
| `130` | Interrupted: the run was finished with `status: cancelled` (or cancelled through `POST /runs/{id}/cancel`). |
| `4` | No usable credentials: no token (`token_missing`), only unexpanded CI variables such as a literal `$(GRAVITY_REPO_TOKEN)` in CI, or locally with no profile token (`token_unresolved`), or any `401` (`unauthorized`), which also stops a run without its finish call. |

`CodeFor` in `internal/cli/exit.go` applies them: a licence refusal anywhere in
the error chain wins, then a `401`, then the command's own `*ExitError`.

## Endpoint map

| Method | Path | Used by |
| ------ | ---- | ------- |
| `POST` | `/api/v1/auth/device/start`, `/api/v1/auth/device/poll` | `login` (device flow) |
| `POST` | `/api/v1/auth/logout` | `logout` (user tokens only) |
| `GET` | `/api/v1/whoami` | every command that needs a token |
| `GET` | `/api/v1/products` | `setup` (product question) |
| `GET` | `/api/v1/sites`, `/api/v1/sites/{site}` | `setup` (site question, its spaces); `structure` fallback tree |
| `POST` | `/api/v1/repos/connect` | `setup` (dry, then real to register), `show`, `validate`, `ci setup`, `status`, and the start of `run`, `check` |
| `POST` | `/api/v1/repos/{repoId}/tokens` | `ci setup` (repository token) |
| `GET` | `/api/v1/repos/self/plan` | `show` (estimates, target status, model and language of AI passes; `--model`), `approve` fallback, `status`, `run` (`--model`), `check` |
| `POST` | `/api/v1/repos/self/validate` | `validate`, `show`, `setup`, and every local `run` before it starts (P2) |
| `GET`, `POST` | `/api/v1/repos/self/approvals` | `approve` (P3) |
| `GET` | `/api/v1/repos/self/runs` | `runs`, `review latest`, `runs show latest` (P4) |
| `GET` | `/api/v1/repos/self/status` | `status`; `runs` fallback |
| `GET` | `/api/v1/structure?site=` | `show`, `validate`, `setup`, `structure plan`, `structure show` (P7) |
| `POST` | `/api/v1/structure/apply` | `structure apply`, `setup` (P7) |
| `POST` | `/api/v1/runs` | `run`, `run --send`, `check` (start, lease); write runs send `manifestHash` (P1) |
| `GET` | `/api/v1/runs/{runId}` | `runs show`, `review`, the lease holder line, capture passes |
| `POST` | `/api/v1/runs/{runId}/cancel` | `runs cancel`; a local run interrupted when finish refuses `cancelled` (P4) |
| `POST` | `/api/v1/runs/{runId}/heartbeat` | every started run |
| `POST` | `/api/v1/runs/{runId}/passes/{runPassId}` | pass reports |
| `POST` | `/api/v1/runs/{runId}/changes` | `reference`, `guides`, `changelog` writes |
| `POST` | `/api/v1/runs/{runId}/verbatim`, `/api/v1/runs/{runId}/verbatim/delete` | `verbatim` imports and deletions |
| `POST` | `/api/v1/runs/{runId}/assets` | images of `verbatim` pages |
| `POST` | `/api/v1/runs/{runId}/hints` | cross-repository hints (`guides`, `check`) |
| `POST` | `/api/v1/runs/{runId}/finish` | the end of every started run |
| `GET`, `POST` | `/api/v1/products/self/inventory` | doc tools read it; write runs ingest units |
| `GET` | `/api/v1/content/pages/{pageId}`, `/api/v1/content/pages?spaceId=&slug=` | passes, `explain` |
| `GET` | `/api/v1/content/spaces/{spaceId}/tree` | `check`, `guides`, `verbatim`, doc tools |
| `POST` | `/api/v1/content/search` | `guides` impact, doc tools |
| `GET` | `/api/v1/content/resolve`, `/api/v1/content/pages/{pageId}/provenance` | `explain` |
| `POST` | `/api/v1/nucleus/recall`, `/api/v1/nucleus/memories` | `nucleus` passes, doc tools |
| `POST` | `/api/llm/v1/messages` | every AI pass |
| `GET` | `/api/llm/v1/instructions?runPassId=&model=&maxOutput=` | `run`: the instructions bundle of every run pass that calls a model, fetched once right after `POST /runs` (`api.Client.InstructionsBundle`); `model` from `--model`, `maxOutput` from the hidden `--max-output` |

## Guided runs (CLI 1.1)

| Contract | What the CLI sends and does | On an older server |
| -------- | --------------------------- | ------------------ |
| P1 local manifest on write runs | `POST /runs` with `mode: write` carries `manifestHash` (after connect sent the manifest); the plan is asked with the same hash | a `400` naming `manifestHash` is retried once without it, with the warning `manifest_snapshot_unsupported` (stored passes only) |
| P2 validate | `{manifestHash, branch, verbatim:[{pass, slug, title, collectionPath, language, sourcePath, empty}], structure}`; issues are merged with the local ones (`source: server`), `i18n` feeds `show` and `validate` | local checks only, warning `server_validate_unsupported` |
| P3 approvals | `GET` lists pending grants per site/space (`target`, `passes`, `reasons`, `why`, `mayApprove`) and `granted`; `POST {spaces}`, `{passes}` or `{all: true}` with a user token | the plan's `approveUrl` per pending target |
| P4 cancel, progress, lease | `finish(status: cancelled)` on SIGINT/SIGTERM, then `POST /runs/{id}/cancel` if finish refuses; `runs show` renders `progress` and `lease`; the lease-holder line reads `GET /runs/{holder}` | `runs cancel` exits `2` (`server_unsupported`); without `progress` the step column is empty |
| P5 estimate | `run` sends `stats=` with the plan request; the plan view and `show` render `estimate` (`approxCostUsd`, `firstRun`, `commits`) and sum the cost of the AI passes that run | `-` in the estimate column |
| P6 CI first runs | a `first_run_manual` skip is labelled "first run is local: gravity run --dry-run" | — |
| P7 structure | `GET /structure?site=` (pages included) marks what exists; `POST /structure/apply {structure, dryRun}` renders created / updated / deferred / extra / conflicts | `structure show` and `plan` read `/sites/{site}` (no pages); `structure apply` exits `2` |

Dry runs are recorded in `.gravity/runs/<runId>.json` (`.gravity/` carries
its own `.gitignore`): the recorded requests of each pass with its range, the
head SHA and the manifest hash. `gravity run --send <runId>` refuses when
either changed, then starts a write run with the recorded passes and ranges,
uploads the recorded images from that commit, replays the recorded writes
through the normal endpoints and finishes the run; no model is called.
Each recorded pass that called a model also keeps `generation
{instructionsVersion, model, provider}` and `usage {turns, inputTokens,
outputTokens}` (`run.RecordedPass`, recording format 1 unchanged); `--send`
forwards `generation` in that pass's report, so the server can fill the run
pass's model and instructions version, and refuses `--model` and
`--max-output`.

## Capabilities

Server features come from `GET /whoami` (`features`, `modules`) and from the
plan (`capabilities.features`, `capabilities.modules`, `capabilities.llm`,
`capabilities.limits`). `POST /repos/connect` also returns `serverFeatures`;
the CLI decodes it but gates on whoami and the plan.

| Key | Where | Effect when absent |
| --- | ----- | ------------------ |
| `pipelines` | whoami, plan | CLI 1.x refuses the server (`pipelines_unsupported`, exit `2`); use gravity 0.x. |
| `machine-tokens` | whoami | `ci setup` mints no repository token and says to create one in the app. |
| `product-inventory` | plan | Write runs skip the inventory ingest. |
| `cross-repo-hints` | plan | Hints are not sent; the pass report carries a warning. |
| `verbatim-lock` | whoami or plan | `status` warns that verbatim passes are not supported. |
| `memory`, `agent` modules | whoami, plan | `status` warns; the platform skips `nucleus` and `capture` passes with `module_disabled`. |

`capabilities.limits` carries `heartbeatSeconds` (fallback for the run's own
value), `leaseTtlSeconds`, `maxChangesPerRun`, `maxBlocksPerChange`,
`maxAssetBytes`, `maxCostUsdPerRun` and `surveyMaxCommits`, the repository's
survey depth for passes without a watermark (a pass's `surveyCommits` option
wins; 50 without either).

## Repository identity

`internal/git.NormalizeRemoteKey` reduces the `origin` remote to the key the
platform stores (`github.com/acme/billing-api`): scheme, credentials, default
ports and `.git` are dropped and only the host is lowercased. A repository
without a git remote cannot connect; the CLI says to add one. The web URL and
provider (GitHub, GitLab, Bitbucket, Azure DevOps) are derived from the key,
with Azure SSH remotes (`ssh.dev.azure.com/v3/…`) mapped to
`https://dev.azure.com/<org>/<project>/_git/<repo>`.

## Connect, plan and status

- `POST /repos/connect` sends the CLI build, the repository facts (remote,
  name, provider, web URL, default branch, branch, commit), the context
  (`trigger` and `origin`), the manifest as JSON plus its raw YAML and
  canonical hash, and `dryRun`. `setup` and `ci setup` send `dryRun: false` to
  register the repository.
  The answer is the registered repository, the manifest outcome (`accepted`,
  `persisted`, `reason`, the authoritative branch, warnings), the effective
  passes, created targets and siblings.
- `GET /repos/self/plan?trigger=&branch=&pass=&mode=&manifestHash=&repo=&stats=&model=` returns
  the passes with `applies` and `skipReason`, targets, watermarks, instruction
  layers, the product inventory and the capabilities. Dry plans send the
  manifest hash so the branch's manifest is overlaid; so do write plans of
  local runs (P1). `--pass` becomes repeated `pass=` parameters. Before the
  plan view, `run` measures the change set of each pass that will run and
  fetches the plan again with one `stats=<pass>:<commits>:<files>[:<bytes>]`
  parameter per pass, so the server can size the estimate (P5); bytes are the
  commit messages plus about 40 bytes per changed line of text files.
  `model=` (from `--model`) asks the server to resolve that model for every AI
  pass; each AI pass then carries `model {id, label, quality, limits,
  fallback, unavailable?: {reason}}` and `language {code, name}`
  (`Plan.UnavailableModels` lists the passes whose model cannot run).
- `GET /repos/self/status?runs=N` feeds `gravity status`.

## A run

1. Connect, then plan. A `--pass` the plan does not list fails with
   `pass_unknown`; on a manual run a named pass the plan still skips for
   `disabled`, `trigger_mismatch` or `branch_mismatch` fails with
   `pass_not_applicable` before any run starts.
2. The CLI resolves each pass's range and ChangeSet and skips unchanged scopes
   at no cost. A run is created only when a pass will work or, in write mode, a
   skip must advance a watermark.
3. `POST /runs` carries the trigger, mode, origin, branch, head and base, the
   pull request or release, the CI context, the plan hash, the manifest hash
   (dry runs, and write runs on servers that accept it), the `model` asked
   with `--model` (omitted otherwise) and one entry per pass with its range kind, base, the watermark it
   saw and its skip reason. `409 lease_held` is retried after its `retryAfter`
   (clamped to 5-60 s) until `--lease-timeout`, and every wait is announced with
   the holder (`GET /runs/{holder}` for its trigger, branch and start);
   `409 plan_stale` re-plans, up to three times. Before the start, any pass
   that will run and call a model whose plan `model.unavailable` is set stops
   the run with `model_unavailable` (`run.ModelUnavailableError`, exit `2`)
   before the plan view; then a local run shows the plan view and, on a
   terminal, asks before AI passes.
4. Heartbeats run every `heartbeatSeconds`. `lease_lost`, `run_not_running`
   or a `401` (on a heartbeat or any other call of the run, `api.StopsRun`)
   stops the run without a finish call.
5. Authoritative write runs and release runs ingest the inventory when the
   plan advertises `product-inventory`: units with roles, in one call, or in
   batches of 2,000 followed by a closing `entries` call. A complete ingest
   deactivates this repository's contributions it no longer lists.
6. Each pass reports `running`, then `succeeded`, `skipped` or `failed` with its
   report, plus `generation {instructionsVersion, model, provider}` when it ran
   with an instructions bundle; writes go through `changes`, `verbatim`, `assets` and `hints`. Asset
   uploads name their pass run with `X-Gravity-Run-Pass-Id`. Dry runs record the
   would-be requests instead of sending them.
7. `POST /runs/{id}/finish` returns the advanced watermarks and the bundle to
   review. An interrupted run finishes with `status: cancelled`.

## Unit keys

API units are keyed `api:<method>:<path>` with the method lowercased and path
parameters written `:name` (`api:post:/v1/refunds/:id/cancel`,
`normalize.APIUnitKey`). Api blocks are keyed `api:<METHOD>:<path>`, with the
method uppercased and the path as written in the spec. Mapped units must match `agent.UnitKeyPattern`,
`^[a-z0-9][a-z0-9._:/-]{0,127}$`, and carry a kind among `feature`, `service`,
`system`, `api` and `capability`.

## Blocks the CLI writes

Changes carry blocks `{ key, type, ownership, content, sourceBinding?,
audiences?, after?, units?, rationale? }`.

- `reference` writes `api` blocks with `ownership: machine` and an `endpoint`
  binding whose hash is the canonical JSON of the operation; `check` compares
  that hash with the pull request's base and head.
- AI passes write the bundle's `blocks.types` (`heading`, `prose`, `list`,
  `code`, `table`, `callout`, `quote`, `divider`) as `hybrid` with an `ai`
  binding and a rationale (a summary, and the commits and files behind the
  edit). Content per type: prose and quote `{text}`, heading `{text, level}`,
  code `{text, language}`, callout `{text, variant}`, list `{text, variant}`
  (one item per block, `bulleted` or `numbered`; `task` maps to `bulleted`),
  table `{rows, header}` (header defaults to true), divider `{}`.
- Human blocks and locked pages are never changed by AI passes.

## Page drafts (CLI 1.2)

The write phases (`guides.write`, `reach.write`, `reference.prose`) build a
page in an in-memory draft (`agent.Draft`) through tools, then map it to one
change at a successful `submit_page`.

- Seeding: update pages start from `GET /api/v1/content/pages/{pageId}?state=draft&format=json`;
  new pages start empty; reference prose starts from the existing page merged
  with the run's computed `api` blocks. A block is editable only when its
  ownership is not `human`, its type is in `blocks.types`, its
  `sourceBinding.kind` is not `api`, `endpoint` or `verbatim`, a `hybrid`
  block carries `clean: true`, and, for unit reach, it is bound to a reached
  unit. Every other block is locked (the draft names the reason).
- Tools: `draft_view`, `draft_append {after?: key|"^", rationale?, blocks}`,
  `draft_replace {key, block}`, `draft_delete {keys}`, `check_page`,
  `raise_hint {kind, claim, unitKey?, blockKey?, detail, forRepos?}` and the
  terminal `submit_page {summary, uncovered?: [{token, reason}]}`. Unit reach
  gets `draft_view`, `draft_replace`, `check_page`, `raise_hint` and
  `submit_page` only. A `Block` is flat: `{type, text?, level?, language?,
  variant?, rows?, header?, audiences?, units?, rationale?}`; audiences are
  the pass audience keys. Invalid blocks are refused with the reason per
  block, and nothing of the call is applied.
- Checks (`check_page`, and `submit_page`, which refuses any error) run on
  new and changed blocks only: E1 another supported language (en, fr, es,
  de, pt) clearly dominates the site language over 150 words or more (a
  warning below that or when unclear); E2 a fenced block, a JSON value of 20
  characters or more, or three indented lines in prose, list, callout or
  quote text; E3 a markdown heading or a bold-only text (a short line without
  a period is a warning); E4 planning voice ("this section should describe",
  "the planner", French equivalents); E5 a `checks.outputMarkers` marker; E7 a
  planner `mustCover` token missing from the page text (case and backticks
  ignored, `{param}` matches `:param`) and not declared in `uncovered`. W1 a
  new page under 120 words; W2 two identical adjacent blocks.
- Mapping: blocks whose normalized type, content, audiences or units differ
  from the seed, and new blocks, are upserts whose `after` is the key of the
  block before them in the draft (`"^"` for the first); deleted editable seed
  keys go to `removeBlockKeys`. A new page is `op: create` with `{spaceId,
  slug, collectionPath}` and its title; an update targets `{pageId}` and keeps
  the title. An empty diff sends nothing (the page counts as unchanged). A
  page task that fails sends nothing for that page.
- New keys are `guide:<slug>:<n>` (`ref:<slug>:<n>` for reference prose),
  `n` continuing after the highest such key on the seed page, deleted keys
  included; the slug part is cut so a key stays within 128 characters.
- The writer kickoff is data only: `<task mode="create|update|deprecate|reach|reference-prose">`
  (page, target, editable keys), `<must_cover>`, `<planner_notes>`, `<units>`
  and `<change_summary>`. The page itself is read with `draft_view`.
- `read_file {path, ref?, offset?, limit?}`, `git_diff {from?, to?, path?,
  offset?, limit?}` and `read_page {pageId|slug, state?, offset?, limit?}`
  return line ranges; a partial reply ends with `[lines a–b of N; call again
  with offset=<b+1>]`. The doc tools are `read_page`, `search_docs`, `recall`,
  `inventory` and `list_target`.
- With `gravity run --debug`, each draft is kept at
  `.gravity/runs/<runId>/<runPassId>/<pageId|new-<slug>>.json`.
- Before updating a page whose open change request carries pipeline changes,
  guides reads `openProposal.pending` (`repoId`, `remoteKey`, `pass`, `runId`)
  from the space tree: a change from another repository or pass is reported
  as competing, one from this repository and pass is reported as replaced
  (the platform supersedes it on the next write). Without the list, the CLI
  falls back to the block provenance of the open change.

## List phases (CLI 1.2)

The other phases build a list through an accumulating tool and end with a
`finish_*` terminal. Every item is validated on its own: valid items are kept,
and the invalid ones come back in one tool error, each with its reason.

- `guides.plan`: `add_pages {pages: [{action: create|update|deprecate,
  pageId?, slug, title, collectionPath?, units, mustCover: [{token?, text}],
  why}]}` and `finish_plan {summary}`. A create needs a kebab-case slug and a
  title; an update or deprecation needs the page id or slug; `why` is
  required; a page already in the plan is refused; past the pass option
  `maxPages` (default 25) every item is refused with `page budget reached
  (<n>)`. The kept pages are then filtered as before (unknown, locked,
  out-of-target and taken slugs are skipped with a warning). `why` goes to
  the writer as `<planner_notes>` and to the change request summary
  (`Why: …`); `mustCover` becomes `<must_cover>` and the E7 check. With
  `deprecate: true` a deprecation runs the writer in `deprecate` mode; with
  `deprecate: false` the page is deleted without a model call. A pull
  request in `prPreview: impact` mode stops after the plan.
- `changelog.write`: `add_release_items {items: [{section, text, commits?,
  units?}]}` (sections `Added`, `Changed`, `Fixed`, `Deprecated`, `Removed`,
  `Security`, any case) and `finish_release_notes {summary}`. The CLI groups
  the items by section in that order.
- `nucleus.distill`: `add_atoms {atoms: [{title, body, kind?, tags?,
  confidence?, commits?, units?}]}` and `finish_atoms {}`. The pass option
  `maxAtoms` (default 40) holds across calls, and a kind outside the pass
  option `kinds` is refused.
- `check.review`: `add_findings {findings: [ClaimFinding]}` (a verdict and a
  title or claim) and `finish_review {summary}`.
- `units.map`: `add_units {units: [{key, kind, title, summary?, sourceRefs,
  aliases?, audiences?}]}`, where a later unit with the same key replaces the
  earlier one, and `finish_units {complete}`. The mapped units are ingested,
  with `complete: true`, only after an unforced `finish_units {complete:
  true}` (a truncated turn never counts); otherwise none is ingested and the
  ingest is not complete. Map-units runs on the bundle of the first AI pass's
  run pass.

Kickoffs carry data only, in tagged sections: `<task phase="…">` (repository,
pass, audiences, product and the phase's settings such as the page budget,
the namespace and atom budget, or the release and its readers),
`<candidate_pages>`, `<reach_pages>`, `<undocumented_units>`, `<open_hints>`,
`<known_atoms>`, `<pages>` (`<page id slug category="claims|verbatim"
source?>`), `<existing_units>`, `<entrypoint_files>`, `<units>` and
`<change_summary>`.

## Run launch and failures (CLI 1.2)

- `gravity run --model <id>` sends `model=` on every plan request, `model` on
  `POST /runs` and `model=` on every bundle request; without it the server
  resolves the model configured for each pass's scope. The hidden
  `--max-output <n>` (development, model evaluation) sends `maxOutput=` on
  bundle requests only; the server can only lower the output limit with it
  and ignores it for repository tokens.
- The plan view lists, under the pass table, one line per AI pass that runs:
  `<pass> model: <label> · <quality> · <language> (<code>)`. A fallback model
  adds the warning `model_fallback`, and a `not_recommended` model the warning
  `model_not_recommended` (`--yes` still proceeds). Without a plan view (CI),
  the `not_recommended` warning is printed at launch instead, with the
  bundle's `qualityNote`.
- `gravity show` renders the same model line per AI pass from the plan
  (`--model <id>` previews another model). The tenant procedure
  `passes.render`, which renders a run pass's instructions, is tRPC only and
  needs a run pass; the CLI has no REST path to it, so `show` shows the
  plan-level model, quality and language only.
- Right after `POST /runs` and before the inventory ingest, the CLI fetches
  the bundle of every run pass that calls a model (guides, changelog,
  nucleus, check with `claims`, reference with `prose`) and prints `<pass>:
  <model label> · <quality> · <language> (<code>) · instructions
  <version8>`, plus the fallback and the bundle warnings. Any error (a `409
  model_unavailable` or `model_conflict`, a server too old) finishes the run
  as failed before any pass starts. With `--debug` (`run.Options.Debug`) the
  bundle is kept at `.gravity/runs/<runId>/<runPassId>.instructions.json` and
  each page draft under `.gravity/runs/<runId>/<runPassId>/`.
- Pass reports carry `generation {instructionsVersion, model, provider}` from
  the bundle; so does each pass of `gravity run --json` (`passes[].generation`).
- A page task that fails (`output_limit`, `no_submit`, `refusal`,
  `cap_reached`, a rejected forced submit) sends nothing for that page; the
  other pages go on and the pass fails with the per-page list. Reference
  prose failures fail the pass the same way, after its `api` blocks are
  written.
- `budget_exceeded` and `no_provider_key` halt the run: the pass in flight
  ends, no other pass starts, in-flight passes are canceled, pages already
  sent stay, and the run is finished as failed with the reason
  (`run.ErrHalted`).

## LLM gateway

`POST /api/llm/v1/messages` takes a Messages-style body plus
`context { runId, runPassId, purpose, instructionsVersion, phase }`, with
`purpose` among `plan`, `author`, `check`, `distill`, `impact` and
`map-units`. The CLI has no prompts of its own: every system prompt and tool
description comes from the run pass's instructions bundle (below), and the
gateway adds no layers to a call that carries `instructionsVersion`.
`402 no_provider_key` means the organization has no LLM key; it halts the
run. The CLI never calls `GET /api/llm/v1/prompts/{name}` (kept by
the server for CLI 1.1).

### Agent loop (CLI 1.2)

`internal/agent` runs one conversation per phase task (`agent.Harness`,
`agent.Runner`):

- `system` is `rendered.system[<phase>]`, sent as one cached text block, with
  one cache breakpoint on the last part of the last message. Tool
  descriptions are the bundle's `tools` entries.
- `max_tokens` is `min(limits.maxOutputTokens, contextWindow − estimate −
  2000)`. The estimate is the last response's prompt tokens (input plus cache
  reads and writes) plus a third of the bytes appended since; the first call
  estimates a third of the bytes of the system prompt, the kickoff and the
  tool schemas. Unknown limits default to 8192 / 128000.
- Above 60% of the context window, the oldest tool results (never the latest
  `draft_view` result nor the kickoff) are replaced by `[elided: <tool> <args>;
  call it again if needed]` in one batch until the estimate is below 40%. The
  same happens first when `max_tokens` would fall below `harness.stepTokens`,
  and once on `413 context_too_large` before a retry; a second `413` fails the
  task with `context_limit`.
- `tool_use`: every call runs in order, the terminal tool last and only when
  every earlier call succeeded; all results go back in one user message.
  `max_tokens`: tool calls are dropped and the model is told to work in steps
  of about `stepTokens`; three in a row fail the task with `output_limit`.
  `end_turn` without the terminal: `Continue. Call <terminal> when the work is
  complete.`, up to `maxContinueNudges`, then `no_submit`. `refusal` fails the
  task with the refusal text. An empty assistant turn is sent back as
  `(no output)` or `(output truncated)`.
- Caps come from `harness`: from `maxTurns − 3` turns on, or once
  `maxToolCalls` is spent, only the terminal tool may be called
  (`tool_choice {type: tool}` when `model.features.forcedToolChoice`, else a
  user message). A forced terminal whose input is rejected fails the task;
  a forced list-phase terminal (`finish_plan`, `finish_atoms`, `finish_units`,
  `finish_review`, `finish_release_notes`) is partial (`finish_units` counts as
  `complete: false`) and the pass report warns `partial (forced at cap)`.
  Running out of turns fails with `cap_reached`.
- There is no CLI token budget; the server's run and month budgets apply
  (`402 budget_exceeded`). Failures are `agent.TaskError` values naming the
  reason and the model. Pass reports count model calls and prompt and output
  tokens (`agent.Usage`).

### Harness v2 surface (CLI 1.2)

- `GET /api/llm/v1/instructions?runPassId=<id>[&model=<id>][&maxOutput=<n>]`
  returns the `gravity.instructions/1` bundle (`api.InstructionsBundle`):
  `version`, `toolContract`, `model {provider, id, label, requested,
  fallback, limits {maxOutputTokens, contextWindow, known}, features
  {forcedToolChoice}, quality, qualityNote}`, `language`, `audiences`,
  `harness {maxTurns, maxToolCalls, stepTokens, maxContinueNudges}`, `blocks`,
  `checks`, `phases`, `rendered.system[<phase>]`, `tools` and `warnings`. A
  `404` without an envelope code (or a `405`), or a schema whose major is not
  `1`, is `api.ErrServerTooOld`: "this Gravity server is too old for gravity
  1.2 (no instructions bundle); upgrade the server or use gravity 1.1". A tool
  contract whose major is not `gravity.tools/1` is `api.ErrToolContract`. A
  `404 not_found` is a missing run pass.
- A v2 model call sends `context.instructionsVersion` and `context.phase`,
  the top-level `model`, `system` as text blocks (`MessagesRequest.SystemBlocks`,
  `api.CachedSystem` marks it `cache_control: {type: "ephemeral"}`) and one
  cache breakpoint on the last content part of the last message
  (`api.WithCacheBreakpoint`). Without `SystemBlocks`, `system` stays a string.
- Responses carry `usage.cache_read_input_tokens` and
  `usage.cache_creation_input_tokens` (`Usage.PromptTokens` sums them with
  `input_tokens`) and the headers `x-gravity-model`, `x-gravity-provider` and
  `x-gravity-fallback: 0|1` (`MessagesResponse.Attribution`). Stop reasons add
  `max_tokens` and `refusal`.
- Error codes: `409 model_unavailable` (`details.reason`: `not_enabled`,
  `no_key` or `catalog_unavailable`; `details.requested`), `409
  model_conflict`, `409 bundle_required`, `413 context_too_large`, `400
  provider_bad_request` (`details.param`), `429 rate_limited`, `402
  budget_exceeded` and `402 no_provider_key`. `api.HaltsRun` is true for
  `budget_exceeded` and `no_provider_key`: the run halts and is finished as
  failed. `api.StopsRun` stays reserved for errors where the server already
  ended the run.

## Nucleus

`POST /nucleus/recall` (`query`, `limit`, `spaceId`, `kinds`, `tags`,
`minConfidence`, `namespace`, `includeShared`, `repo`) serves the
`recall` doc tool and the `nucleus` pass. `POST /nucleus/memories`
writes atoms with `namespace`, `runId`, the kind (unknown kinds are sent as
`other`) and `sources` that reference the repository, commits and units.
