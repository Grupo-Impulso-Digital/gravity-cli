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
- **Retries.** A `429` or `5xx` is tried up to three times in all, waiting
  `Retry-After` (or the envelope's `retryAfter`) when given, else 1 s doubling,
  capped at 60 s. A network error is retried for `GET` and for model calls (`POST /api/llm/v1/messages`); other writes fail on the first one.
- **Error envelope.** A non-2xx body is
  `{"error":{"code","message","details":[{path,code,message}],"module","retryAfter","holder"}}`,
  decoded into `*api.APIError`. A body that is not the envelope (an HTML gateway
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
| `GET` | `/api/v1/repos/self/plan` | `show` (estimates, target status), `approve` fallback, `status`, `run`, `check` |
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
| `GET` | `/api/llm/v1/prompts/{name}` | every AI pass (hosted prompt, with a baked fallback) |

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
- `GET /repos/self/plan?trigger=&branch=&pass=&mode=&manifestHash=&repo=&stats=` returns
  the passes with `applies` and `skipReason`, targets, watermarks, instruction
  layers, the product inventory and the capabilities. Dry plans send the
  manifest hash so the branch's manifest is overlaid; so do write plans of
  local runs (P1). `--pass` becomes repeated `pass=` parameters. Before the
  plan view, `run` measures the change set of each pass that will run and
  fetches the plan again with one `stats=<pass>:<commits>:<files>[:<bytes>]`
  parameter per pass, so the server can size the estimate (P5); bytes are the
  commit messages plus about 40 bytes per changed line of text files.
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
   (dry runs, and write runs on servers that accept it) and one entry per pass with its range kind, base, the watermark it
   saw and its skip reason. `409 lease_held` is retried after its `retryAfter`
   (clamped to 5-60 s) until `--lease-timeout`, and every wait is announced with
   the holder (`GET /runs/{holder}` for its trigger, branch and start);
   `409 plan_stale` re-plans, up to three times. Before the start, a local run
   shows the plan view and, on a terminal, asks before AI passes.
4. Heartbeats run every `heartbeatSeconds`. `lease_lost`, `run_not_running`
   or a `401` (on a heartbeat or any other call of the run, `api.StopsRun`)
   stops the run without a finish call.
5. Authoritative write runs and release runs ingest the inventory when the
   plan advertises `product-inventory`: units with roles, in one call, or in
   batches of 2,000 followed by a closing `entries` call. A complete ingest
   deactivates this repository's contributions it no longer lists.
6. Each pass reports `running`, then `succeeded`, `skipped` or `failed` with its
   report; writes go through `changes`, `verbatim`, `assets` and `hints`. Asset
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
- AI passes write `heading`, `prose`, `code`, `list`, `callout`, `table` and
  `quote` blocks as `hybrid` with an `ai` binding and a rationale (a summary,
  and the commits and files behind the edit). The CLI normalizes common model variations before sending: list
  variants map to `bulleted`, `numbered` or `task`, and heading levels are
  clamped to 1-3.
- Human blocks and locked pages are never changed by AI passes.
- Before updating a page whose open change request carries pipeline changes,
  guides reads `openProposal.pending` (`repoId`, `remoteKey`, `pass`, `runId`)
  from the space tree: a change from another repository or pass is reported
  as competing, one from this repository and pass is reported as replaced
  (the platform supersedes it on the next write). Without the list, the CLI
  falls back to the block provenance of the open change.

## LLM gateway

`POST /api/llm/v1/messages` takes a Messages-style body plus
`context { runId, runPassId, purpose }`, with `purpose` among `plan`,
`author`, `check`, `distill`, `impact` and `map-units`. The CLI sends only the
pass kind's prompt; the gateway composes the organization, site, space,
collection, pass and note layers. `402 no_provider_key` means the organization
has no LLM key; the AI pass fails with that error. Hosted prompts come from
`GET /api/llm/v1/prompts/{name}` and are cached per process
(`prompts.Resolver`). An error or an empty answer falls back to the prompt
baked into the binary, except a licence refusal, a run-stopping error
(`lease_lost`, `run_not_running` or a `401`) or a cancelled context, which fail
the pass.

## Nucleus

`POST /nucleus/recall` (`query`, `limit`, `spaceId`, `kinds`, `tags`,
`minConfidence`, `namespace`, `includeShared`, `repo`) serves the
`recall_nucleus` doc tool and the `nucleus` pass. `POST /nucleus/memories`
writes atoms with `namespace`, `runId`, the kind (unknown kinds are sent as
`other`) and `sources` that reference the repository, commits and units.
