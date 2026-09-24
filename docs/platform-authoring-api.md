# Platform contract: the API `gravity` speaks (v2)

This is the CLI-side view of the platform endpoints the `gravity` binary calls to
author **customer** documentation, register itself, and report coverage. The
canonical, shipped contract lives in the `gravity` repo
(`docs/cli-api-contract.md`); this file mirrors it and adds the CLI's side of each
exchange (which command sends what, and how it degrades).

**Audience:** CLI contributors and the Gravity platform team.
**Consumer:** `gravity sync`, `docs generate`, `coverage`, `repos`, `ping`,
`capture`, `check api`, `check docs`, `release-notes`, `nucleus`.

## Conventions (already assumed by `internal/api`)

- Base + auth: `https://<host>/api/v1/...`, `Authorization: Bearer sk_live_…`,
  scoped to the key's org. `/app` is the dashboard SPA, not the API.
- Error envelope: non-2xx returns `{"error":{"code":"...","message":"..."}}`.
  `(*APIError).IsUnavailable()` treats `not_implemented` / `feature_disabled` /
  `unknown_route` (and 404/501/412 on a preview route) as "skip with a notice".
- Governance: machine **content** writes never publish — they create a
  **draft + open proposal** and return
  `{ pageId, pageSlug, proposalId, status, reviewUrl }`. Repo registration,
  inventory, attribution and translation requests are **metadata** writes and land
  directly.
- Exit codes: `0` pass · `1` findings · `2` operational error · `3` license refusal (403 `module_disabled` / `seat_limit`).

## Endpoint map

| Method | Path | Used by | Feature flag |
|---|---|---|---|
| `GET` | `/api/v1/whoami` | `doctor`, every gated command | — |
| `POST` | `/api/v1/setup/ping` | `ping`, `repos` | — (v2 fields degrade) |
| `GET` | `/api/v1/sites` | `sites`, `init` | — |
| `GET` | `/api/v1/sites/:site` | `sync`, `docs generate`, `nucleus` | — |
| `POST` | `/api/v1/sites/:site/spaces` | `sync`, `spaces`, `init` | `space-hierarchy` for `parent`, `space-metadata` for `type`/`visibility` |
| `PATCH` | `/api/v1/sites/:site/spaces/:space` | `sync` | `space-hierarchy` |
| `GET` | `/api/v1/sites/:site/pages` | `sync`, `docs generate`, `check docs`, `coverage` | — (`repo=`/`languages=1`/`include=draft` are v2) |
| `GET` | `/api/v1/sites/:site/spaces/:space/pages/:page` | `check docs` | — |
| `GET` | `/api/v1/sites/:site/api-blocks` | `check api` | — |
| `POST` | `/api/v1/sites/:site/pages` | `sync`, `docs generate` | `repos` / `page-languages` for the v2 fields |
| `POST` | `/api/v1/sites/:site/release-notes` | `release-notes`, `sync` (`as: release`) | same |
| `POST` | `/api/v1/sites/:site/inventory` | `docs generate` | `inventory` |
| `GET` | `/api/v1/sites/:site/coverage` | `coverage` | `coverage` |
| `POST` | `/api/v1/sites/:site/doc-agent/runs` | `capture` | `doc-agent-runs` |
| `GET` | `/api/v1/sites/:site/doc-agent/runs/:runId` | `capture`, `capture status` | `doc-agent-runs` |
| `POST` | `/api/v1/nucleus/recall` · `/api/v1/sites/:site/nucleus/recall` | `nucleus query`, agent kickoffs | `memory` module |
| `POST` | `/api/v1/nucleus/memories` | `nucleus sync` | `memory` module |
| `POST` | `/api/llm/v1/messages` · `GET /api/llm/v1/config` · `GET /api/llm/v1/prompts/:name` | every AI command | `prompt-endpoint` |

## `whoami.features`

```jsonc
{
  // v1
  "block-audience":  true,  // blocks accept + return `audiences`
  "prompt-endpoint": true,  // GET /api/llm/v1/prompts/:name
  "docs-generate":   true,  // the server side of `gravity docs generate`
  "space-hierarchy": true,  // subspaces, home pages, page-upsert collections
  "space-metadata":  true,  // space-ensure `type` + `visibility`
  // v2
  "repos":          true,   // connected_repo: ping registration + `repo` on writes
  "inventory":      true,   // POST /api/v1/sites/:site/inventory
  "coverage":       true,   // GET  /api/v1/sites/:site/coverage
  "doc-agent-runs": true,   // POST/GET /api/v1/sites/:site/doc-agent/runs[/:runId]
  "page-languages": true    // page upsert `languages` + page read `languages[]`
}
```

The constants live in `internal/cli/feature.go`. `gravity doctor` prints one line
per flag. The same map is echoed as `serverFeatures` on the ping response, so
`gravity ping` needs one round-trip.

Degradation per flag:

| Flag absent | CLI behavior |
|---|---|
| `repos` | `repo` is stripped from every write; `myRemoteKey` stays empty, so orphan/duplicate detection reverts to the pre-v2 site-wide form. One notice. |
| `inventory` | `docs generate` prints one notice and skips the inventory POST; authoring is unaffected. |
| `coverage` | `gravity coverage` reports unavailability and exits `0` (`--require` ⇒ exit `2`). |
| `doc-agent-runs` | `gravity capture` reports unavailability and exits `0` (`--require` ⇒ exit `2`); a `412 feature_disabled` from the route behaves the same. |
| `page-languages` | `languages` is stripped from every write. One notice. |
| `space-hierarchy` | `collection` stripped, no `parent`/`homePage`. One notice. Shared-space slugs stay prefixed either way. |
| `space-metadata` | declared spaces are still ensured, with `type`/`visibility` stripped. One notice. |
| `block-audience` | blocks are authored with no `audiences`, so they render to everyone. |

## Repo identity — `remoteKey`

`internal/git.NormalizeRemoteKey` reduces a git remote to the stable identity the
platform keys `connected_repo` on. It is **idempotent**, so a value an older CLI
already normalized re-normalizes unchanged server-side. Only the host is
lowercased — some self-hosted forges are case-sensitive, where `acme/Orbit` is
not `acme/orbit`.

```
https://GitHub.com/Acme/orbit-api.git   ┐
git@github.com:Acme/orbit-api.git       ├─→ github.com/Acme/orbit-api
ssh://git@github.com:22/Acme/orbit-api  │
https://u:p@github.com/Acme/orbit-api/  ┘
```

With no git remote (fresh `git init`, tarball checkout, vendored subtree) the CLI
falls back to `product.slug + "/" + product.repo` from the resolved manifest and
reports `repo.remoteKeySource: "config"`. With neither, the repo has **no
identity**: writes go out unattributed and every repo-scoped behavior degrades to
its pre-v2 form. `internal/cli.localRepoRef` is the one place that resolves this.

## `POST /api/v1/setup/ping` — handshake + repo registration

Sent by `gravity ping` and `gravity repos` (running `repos` therefore also
refreshes the registration). It carries the CLI build, the connection config, git
facts, the **fully-resolved manifest as JSON** (`configFull`), the **raw manifest
text** (`configYaml`), and a counts-only `docSources` summary for the web wizard.
Neither config field can carry a token: `config.Project` has no token field and a
committed `token:` is rejected at load — and the server redacts defensively
anyway.

Response adds `serverFeatures`, plus `repo: { id, firstSeenAt }` and `siblings[]`
when the platform could derive an identity. `firstSeenAt` is the **first ping
ever** for this repo, not this one. Both `repo` and `siblings` are omitted on a
v1 platform or when no identity exists — `gravity repos` renders that as "this
platform does not register repos yet". The ping never fails on a registry
problem.

`gravity ping` prints org / key / default site / registration / features /
siblings; `--json` emits `{request, response}`.

## Write attribution

Every authoring write carries `repo: { remoteKey, name? }` when
`features["repos"]` is set. Server-side, after the content write succeeds:
the repo row is **created if unknown** (a repo that writes before it pings is
registered by the write), `page.repoId` is set last-writer-wins,
`connected_repo.lastWriteAt` is touched, and every inventory unit claiming the
written slug gets `documentedHash = sourceHash`. All of it is best-effort — it can
never turn a successful write into an error.

Read side: `GET …/pages`, the site tree's `pages[]`, and the single-page detail
each project `repoId` and `repoRemoteKey` (both nullable). `GET …/pages?repo=<remoteKey>`
filters to one repo — an unknown key is `200` with an empty list, not `404`.

Two call sites consume the attribution, and they key off different fields:

- **`gravity sync`** — `reconcilePageTargets` and the duplicate-pruning gate use
  **`repoRemoteKey`**, because the CLI knows its own remote key locally without a
  round-trip.
- **`gravity docs generate`** — `reportOrphans` uses **`repoId`**, recovered by
  `repoIDFor` from the already-read page list (the first page this repo wrote
  carries the id). A repo that has never written resolves to `""` and gets the
  site-wide behavior.

Either way the rule is the same:

| Page's attribution | Behavior |
|---|---|
| unattributed (human-authored / pre-v2) | eligible for matching and orphan reporting, as in v1; a duplicate is **reported, not pruned** |
| this repo's | eligible, as in v1; a duplicate may be auto-proposed for deletion |
| a sibling repo's | **excluded entirely** — never matched, never orphaned, never pruned |

An empty local identity (no remote and no manifest, or `features["repos"]`
absent) restores byte-identical v1 behavior. `--keep-duplicates` still suppresses
all pruning.

## Block model the CLI authors

Blocks: `{ key, type, ownership, audiences?, content, sourceBinding?, position }`.

- **type** — `heading | prose | code | table | api`. (A `markdown` type is
  expected "soon"; until then `sync` decomposes Markdown into the native types
  above and falls back to verbatim `prose` for the rest.)
- **audiences** — optional `string[]` over `public | users | developers`. Empty
  or absent ⇒ the block renders to **every** viewer. Emitted only when
  `features["block-audience"]` is set.
- **ownership** — `machine | hybrid | human`.
  - `machine`: content is a pure function of `sourceBinding.ref`'s bytes; humans
    cannot edit it in Gravity. It changes **iff** the source changes.
  - `hybrid`: machine fields updated, human edits preserved around them.
  - `human`: seeded once, never overwritten.
- **key** — stable, identity-derived (`api:<METHOD>:<path>`,
  `doc:<file>:<section>[:n]`), never positional, so re-authoring produces clean
  diffs across insertions and reorderings. **`key` is now returned on every page
  read** (`GET …/pages`, `GET …/spaces/:space/pages/:page`, `GET …/api-blocks`) as
  an explicit `null` when absent, which closes the v1 idempotency gap. The
  re-author matcher is **`key` first, then `sourceBinding` `kind:ref`, then
  create** — the binding fallback stays forever for human-authored and legacy
  blocks.
- **sourceBinding** — `{ kind, ref, hash:"sha256:<whole-file>", generator }`.
  The CLI hashes `ref` with the exact hasher the drift checker recomputes, so an
  authored machine block passes `check api`/`check docs` immediately and goes
  stale only when its source file changes. **`kind` must be one of the server's
  `CODE_SOURCE_KINDS`** (`route|struct|endpoint|schema|config|cli`) for the block
  to stay machine/hybrid; the CLI emits **`kind:"cli"`** for every repo-file-bound
  block. AI narrative prose is `hybrid`/`human` and carries **no binding at all**,
  so the team can edit it freely and it never drift-locks.

### Merge governance the CLI depends on (server-side, shipped)

On upsert the CLI sends **only the blocks it owns**. The platform must:
match by `key` then `sourceBinding.ref`; replace `machine` blocks from the
payload; never overwrite `human` blocks; update `hybrid` machine-fields while
preserving human edits; and **propose removal** (never silently delete) for
machine blocks absent from the payload. `audiences` is a machine field, so a
`hybrid` re-author updates the audience set while preserving human body edits.

## Space hierarchy (`space-hierarchy` feature)

Since the platform's hierarchy inversion (migration 0050) the content tree is:

```
Site → Space (+ one level of Subspace via parentSpaceId)
     → Collection (page folder INSIDE a space, self-nesting)
     → Page → Block
```

- **`spaces.parent`** → `POST /spaces` with `parent` for the default space
  (parent ensured first). On the existing-space path a differing `parent`
  REPARENTS the space — declarative, so re-running `gravity sync` converges. One
  level only; the server 400s deeper nesting.
- **Shared spaces** (`spaces.shared`) → each page upsert carries
  `collection: <product.repo>` (an explicit mapping `collection:` wins). The
  `repo/` slug prefix is KEPT — page identity is `(space, slug)` server-side, so
  the prefix is what stops sibling repos' same-named pages from upserting onto
  each other; the collection is presentation. (With `repos` shipped, attribution
  is the *second* line of defense: a sibling's page is now excluded from
  reconciliation outright.)
- **`spaces.home`** → after authoring, `PATCH /spaces/:space {homePage: <slug>}`.
  The home page must sit flat in the space, so the CLI never prefixes or collects
  it.
- **`spaces.declare`** → every declared space is ensured BEFORE the mapping
  targets, parents first, with `name`/`parent` plus `type`/`visibility` when the
  `space-metadata` flag is on (stripped, with one notice, when it is off). The
  declaration's `audiences` never reach the server: they route planned pages
  client-side (narrowest covering space wins; an explicit page `space` or
  `--space` wins over both).

## Draft-aware page reads (`include=draft`)

`GET /api/v1/sites/:site/pages?include=draft` additionally returns pages that
exist only as a draft/open proposal, and every row carries
`status: "released" | "draft"`. `gravity sync` and `gravity docs generate` both
request it: the planner sees a draft page as a page to EDIT (the existing-pages
digest marks it `[draft]`), and reconciliation upserts onto the open proposal
instead of minting a duplicate slug. An older server ignores the query param and
omits `status`; the CLI treats a missing `status` as `released`.

## Feature inventory + coverage

### `POST /api/v1/sites/:site/inventory` (`docs generate`)

After the plan phase and **before authoring** — so a run that dies mid-authoring
still records what the survey found — `gravity docs generate` publishes the units
the planner enumerated. Types live in `internal/api/inventory.go`.

```jsonc
{
  "repo":  { "remoteKey": "github.com/Acme/orbit-api", "name": "orbit-api" },
  "generatedAt": "2026-07-27T14:03:11Z",   // sent; the server ignores it
  "replace": true,
  "units": [{
    "key": "svc.billing.invoicing",   // ^[a-z0-9][a-z0-9._-]{0,127}$, stable across runs
    "kind": "service",                // feature|service|system|api|capability
    "title": "Invoicing service",
    "summary": "…",
    "sourceRefs": ["src/billing/invoice.ts"],
    "sourceHash": "sha256:…",         // docs.UnitSourceHash over sourceRefs
    "audiences": ["developers"],
    "pageSlugs": ["orbit-api/invoicing"]
  }]
}
```

- `replace: true` is sent by a **full** run and is declarative — units absent
  from the body are deleted. `replace: false` is sent by `--page` and `--since`
  runs, which only surveyed a slice of the repo and must not delete what they
  never looked at.
- A unit whose `key` fails `agent.UnitKeyPattern` is dropped client-side. A unit
  with no `kind` inherits `Project.ResolveUnitKind()`.
- `pageSlugs` are namespaced through the manifest's own page targeting, so they
  match the slug the platform will actually see in a shared space.
- Every failure degrades to a notice — a lost inventory must never cost the
  pages. No repo identity, or a plan with zero units, skips the call entirely.
  `--no-inventory` skips it unconditionally.

Response: `{ repoId, units: {received, created, updated, unchanged, removed}, coverageUrl }`.

### `GET /api/v1/sites/:site/coverage` (`gravity coverage`)

Params: `repo=<remoteKey>`, `kind=<one of the five>`. The CLI always requests the
full projection and shapes output locally; `format=summary` exists server-side
but the CLI does not use it.

Unit states, as the platform resolves them:

- **`documented`** — some listed `pageSlug` resolves to a page in the site whose
  `repoId` is null or this repo's.
- **`stale`** — documented, and the unit's `documentedHash` (stamped by the last
  page write) differs from its current `sourceHash`.
- **`undocumented`** — everything else.

`totals.documented` **includes** stale units; `totals.stale` is a subset of it;
`totals.undocumented = units - documented`; `ratio = documented / units`, and a
repo with zero units reports `ratio: 1`. `byKind` always lists all five kinds.
`uncoveredPages` is the reverse view — pages this repo wrote that its inventory
no longer claims.

CLI behavior: `gravity coverage` defaults to **this repo** (scoped by the locally
derived `remoteKey`), `--all` covers the site, `--repo` names another. The gate
is `--min`, defaulting to `coverage.min` from `.gravity.yaml`; the ratio is
recomputed locally from the totals so server-side rounding can never skew it. A
repo below the bar is a `warn` finding, a missing `coverage.require` page is an
`error` finding, and both exit `1`. Staleness alone never fails the bar — that is
`check docs`'s job. Required pages are asserted against the site's published page
list, not the inventory, because a required page may be human-authored.

## Doc Agent runs (`gravity capture`)

`gravity capture` is retargeted, not removed. The never-built
`POST /api/v1/sites/:site/captures` contract is gone; the command now drives the
platform's Doc Agent, whose target comes from the space's stored
`tenant_connection` and the run brief. Credentials never leave the platform.

- `POST /api/v1/sites/:site/doc-agent/runs` →
  `202 { runId, status, statusUrl }`. Body:
  `{ spaceSlug, connectionLabel?, brief?, async, repo? }`.
- `GET /api/v1/sites/:site/doc-agent/runs/:runId` → `DocAgentRun`
  (`{ runId, status, statusUrl, spaceSlug, connectionLabel, trigger, proposalId,
  reviewUrl, error, createdAt, startedAt, finishedAt, stats, artifacts[] }`).

Statuses: `queued | running | succeeded | failed | cancelled`. `succeeded` exits
`0`; `failed` and `cancelled` exit `1` (the run's `error` is printed). There is no
`partial` — that was capture-only. `artifacts[]` is capped at the newest 200,
while `stats.artifacts` counts them all.

Flag mapping:

| v1 flag | v2 |
|---|---|
| `--space` | → `spaceSlug` |
| `--label` | deprecated; sent as `connectionLabel`. Prefer `--connection`. |
| `--async`, `--timeout`, `--poll-interval`, `--require`, `--ci`, `--format`, `--dry-run` | unchanged |
| `--url`, `--path`, `--capture`, `--max-pages`, `--auth-secret`, `--attach`, `--release-proposal` | **removed** — hidden flags that hard-error (exit `2`) naming their replacement |

New: `--connection <label>`, `--brief <text>`, `--brief-file <path>` (repo-relative,
sandboxed through `pathsafe` — a brief is model input and must not become a way to
read arbitrary CI files). `--brief` and `--brief-file` are mutually exclusive.

Unknown `connectionLabel` is `404` (deliberately not `403` — the label space is
not an existence oracle). No agent transport configured is
`412 feature_disabled`, which `IsUnavailable()` already treats as a skip.

## i18n — `languages` on writes, `languages[]` on reads

`i18n.languages` from the manifest is sent on every page upsert (and release-notes
create) when `features["page-languages"]` is set. Server-side it becomes
`page.translationLanguages`; on publish the platform takes the **union** of the
site's own auto-translate targets and the page's request. `[]` clears the request;
omitting the field leaves it alone.

The read side (`languages[]`: `{ language, status: draft|live, outdated, updatedAt }`)
is typed in `internal/api` and available on the single-page detail read and on
`GET …/pages?languages=1`, but **no command requests it yet** and `check docs`
emits no translation findings.

## Nucleus memory (`gravity nucleus`)

The `/api/v1/knowledge/:namespace/atoms*` contract earlier drafts of this file
described **never existed**. The shipped surface is:

- `POST /api/v1/sites/:site/nucleus/recall` when a site is resolved, else
  `POST /api/v1/nucleus/recall`. Body
  `{ query, limit?, spaceId?, kinds?, tags?, minConfidence? }` → `{ scope, hits[] }`
  with `score` and `matchedBy`. `spaceId` is the space **id**, resolved from the
  slug via the site tree and cached for the process; an unresolvable slug drops it
  and recalls at site scope rather than erroring.
- `POST /api/v1/nucleus/memories` — `{ title, body, kind?, tags?, confidence?,
  sources?, scope?, siteSlug?, spaceId? }` → `{ memory, outcome }` with
  `outcome: created|revised|unchanged` (`201` on create, `200` otherwise).
  **Idempotent by normalized title at the scope**, which is why the CLI mints no
  ids and why the distill prompt insists on short, stable noun-phrase titles.

`knowledge.namespace` travels as an `ns:<namespace>` tag on write (plus
`repo:<product.repo>` when set); repo file provenance travels as a
`src:<repo-relative-path>` tag, because `sources[]` is a platform-object
reference (`refType: site|space|page|block`), not a repo path.

> **Not implemented server-side:** the CLI sends a `namespace` field on recall
> bodies as a soft ranking hint. The platform's recall schemas do not declare it,
> so zod strips it and it has **no effect** today. It is harmless (no 400) but it
> is not a contract — do not rely on namespace-boosted ranking.

Everything is best-effort: the `memory` module being disabled is a normal
`403 forbidden` ("The Nucleus module is not enabled.") that the existing skip path
handles, and a failed recall returns the original agent kickoff unchanged. There is
no `featureNucleus` flag.

## Server-hosted prompts

`GET /api/llm/v1/prompts/:name` → `{ name, text, version? }` for `release-notes`,
`docs-gap`, `nucleus`, `docs-plan`, `docs-author`. The CLI fetches at the start of
each agent command and **falls back to its baked-in default** on any error, so
prompts can be tuned server-side without a CLI release.

> **Operational coupling:** the hosted prompt wins over the baked-in one. A hosted
> `docs-plan` that predates the unit inventory will produce plans with no units,
> which the CLI reports as "the plan declared no units; skipping the inventory" and
> which leaves coverage silently at zero. Hosted prompts must be updated in lockstep
> with the inventory contract.

## Resilience contract (server-side hardening the CLI still wants)

The CLI survives a partial failure locally: `docs generate` persists the authored
block set to `.gravity/generated/docs.json` **before** syncing and authors each
target independently, so one page's `400` no longer discards the rest, and a saved
run replays with `--from <file>` at no AI cost. These server changes would make the
flow robust rather than merely recoverable:

1. **Actionable validation errors.** `Invalid source binding: Invalid option:
   expected one …` truncates before listing the allowed values and omits which
   field failed. Return the field path and the accepted set (e.g.
   `sourceBinding.kind must be one of route|struct|endpoint|schema|config|cli`).
   The CLI surfaces `error.message` verbatim, so a precise message is self-service.
   (The inventory route already does this — it prefixes the failing
   `units.<i>.<field>` path.)
2. **Validate the whole block set up front, report all offenders** — one
   round-trip per page instead of N.
3. **Atomic or explicitly partial page upsert.** The CLI assumes atomic (a failed
   page = no change); confirm it, or return which blocks landed.
4. **Idempotent proposals.** Re-syncing an unchanged page (e.g. a `--from` replay)
   reuses the open proposal keyed `cli-upsert:<pageId>` — keep it that way, or
   replay-to-recover mints duplicate change requests.

## Open questions for the platform team

1. **`markdown` block type** — confirm the name/shape so `sync` can target it for
   the verbatim branch instead of `prose`.
2. **`check api` multi-spec scoping** — `…/api-blocks` is site-wide; a per-page or
   per-spec scope is needed if one site documents several specs.
3. **`agent-pages` block `key`** — the other three read endpoints project it; this
   one does not (it has no `GET` handler today, so nothing depends on it yet).
4. **Recall `namespace`** — accept it as a soft ranking hint, or tell the CLI to
   stop sending it.
