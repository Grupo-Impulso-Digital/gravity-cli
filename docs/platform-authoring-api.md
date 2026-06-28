# Platform contract: authoring API consumed by `gravity sync`

This documents the platform endpoints and block semantics the CLI relies on to
author **customer** documentation (not the CLI's own docs — `selfdoc` is gone).
The canonical, shipped contract lives in the `gravity` repo
(`docs/cli-api-contract.md`); this file is the CLI-side view.

**Audience:** the Gravity platform (Workers app).
**Consumer:** `gravity sync` (and `gravity release-notes`).

## Conventions (already assumed by `internal/api`)

- Base + auth: `https://<host>/api/v1/...`, `Authorization: Bearer sk_live_…`,
  scoped to the key's org. `/app` is the dashboard SPA, not the API.
- Error envelope: non-2xx returns `{"error":{"code":"...","message":"..."}}`.
- Governance: machine writes never publish — they create a **draft + open
  proposal** and return `{ pageId, pageSlug, proposalId, status, reviewUrl }`.

## Endpoints `gravity sync` uses (all shipped)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/v1/sites/:site/spaces` | idempotent ensure-space (on slug) |
| `POST` | `/api/v1/sites/:site/pages` | upsert a page's blocks → draft + proposal |
| `POST` | `/api/v1/sites/:site/release-notes` | versioned release page (used by `documents.as=release`, via `bodyMarkdown`) |

Read side (`check api`/`check docs`): `GET /api/v1/sites/:site`,
`…/pages?space=`, `…/api-blocks` — unchanged.

## Block model the CLI authors

Blocks: `{ key, type, ownership, audiences?, content, sourceBinding?, position }`.

- **type** — `heading | prose | code | table | api`. (A `markdown` type is
  expected "soon"; until then `sync` decomposes Markdown into the native types
  above and falls back to verbatim `prose` for the rest.)
- **audiences** — optional `string[]` over `public | users | developers`. Empty
  or absent ⇒ the block renders to **every** viewer; otherwise it renders only to
  viewers in a listed audience. This lets one page mix per-audience blocks (the
  `gravity docs generate` model). It is **greenfield** (see below): the CLI emits
  it only when the platform advertises `whoami.features["block-audience"]`, and
  omits it otherwise so blocks render to everyone. Backward-compatible — existing
  blocks have no `audiences`.
- **ownership** — `machine | hybrid | human`.
  - `machine`: content is a pure function of `sourceBinding.ref`'s bytes; humans
    cannot edit it in Gravity. The CLI re-authors it deterministically; it
    changes **iff** the source changes.
  - `hybrid`: machine fields updated, human edits preserved around them.
  - `human`: seeded once, never overwritten.
- **key** — stable, identity-derived (`api:<METHOD>:<path>`,
  `doc:<file>:<section>[:n]`), never positional, so re-authoring produces clean
  diffs across insertions/reorderings.
- **sourceBinding** — `{ kind, ref, hash:"sha256:<whole-file>", generator }`.
  The CLI hashes `ref` with the exact hasher the drift checker recomputes, so an
  authored machine block passes `check api`/`check docs` immediately and goes
  stale only when its source file changes.

### Merge governance the CLI depends on (server-side, shipped)

On upsert the CLI sends **only the blocks it owns**. The platform must:
match by `key` then `sourceBinding.ref`; replace `machine` blocks from the
payload; never overwrite `human` blocks; update `hybrid` machine-fields while
preserving human edits; and **propose removal** (never silently delete) for
machine blocks absent from the payload. Treat `audiences` as a machine field, so
a `hybrid` re-author updates the audience set while preserving human body edits.

## Open questions for the platform team

1. **`markdown` block type** — confirm the name/shape so `sync` can target it for
   the verbatim branch instead of `prose`.
2. **`position` vs human-inserted blocks** — confirm identity (`key`) wins and
   human blocks keep their slot when the CLI re-sends machine positions `0..N`.
3. **`check api` multi-spec scoping** — `…/api-blocks` is site-wide; a per-page
   or per-spec scope is needed if one site documents several specs.

---

# Greenfield contracts (not yet implemented)

The CLI already speaks these; it degrades gracefully (404/501 or a
`not_implemented`/`feature_disabled`/`unknown_route` code → skip + notice, or a
hard error with `--require`) until the platform ships them. The platform should
also advertise readiness via `whoami.features` (e.g. `{"captures":true,
"nucleus":true,"docs-generate":true,"block-audience":true}`).

## Server-hosted prompts (`gravity` agents)

- `GET /api/llm/v1/prompts/:name` → `{ name, text, version? }`. Returns the
  current system prompt for an agent (`release-notes`, `docs-gap`,
  `nucleus-distill`, `docs-plan`, `docs-author`). The CLI fetches this at the
  start of each agent command and **falls back to a baked-in default** on any
  error, so prompts can be tuned server-side without a CLI release. A 404 /
  `unknown_route` is the expected pre-launch response and is handled silently.

## Documentation generation (`gravity docs generate`)

- Gated on `whoami.features["docs-generate"]`; absent ⇒ notice + exit 0 (or hard
  error with `--require`), like capture/nucleus.
- Produces, per planned page, a `POST /api/v1/sites/:site/pages` upsert whose
  blocks carry `audiences` (above) and `ownership` (AI prose is `hybrid` with a
  hash-less `sourceBinding{kind:"ai"}` so `check docs` reports it skipped, not
  stale). Idempotent: the CLI reads existing pages first and reuses block `key`s
  to update / omits them to propose removal — relying on the merge governance
  above. **This requires the page read model (`GET …/pages`) to return each
  block's author `key`** (the CLI keys updates off it); without it, re-runs can
  duplicate instead of update.
- Open questions: confirm the `audiences` field name/shape (`audiences[]` set vs a
  single `audience` enum — the CLI assumes the set with "absent = all"), and that
  the page read model returns block `key`s for idempotent re-authoring.

## Runner / capture (`gravity capture`)

- `POST /api/v1/sites/:site/captures` — launch a run. Body: `CaptureRequest`
  (`{ spaceSlug, label, target:{url,paths,viewport,authSecretRef}, capture[],
  scope, attach:{mode,releaseProposalId}, async }`). `authSecretRef` is a
  platform-stored login recipe — never raw credentials in CI.
- `GET /api/v1/sites/:site/captures/:runId` — poll status. Returns `CaptureRun`
  (`{ runId, status(queued|running|succeeded|partial|failed), statusUrl, stats,
  artifacts[], proposalId, reviewUrl, error }`).
- Artifacts attach as `machine` `screenshot` blocks with
  `sourceBinding{kind:"capture"}` as a draft + proposal (governance as above).
- Open questions: app-auth model, how "new" UI is determined (explicit paths vs
  baseline diff), run durability/retention, whether `releaseProposalId` can
  append to an existing open proposal, and that launch returns promptly (queued)
  rather than blocking past the client's 120s timeout.

## Nucleus memory (`gravity nucleus`)

- `POST /api/v1/knowledge/:namespace/atoms/query` — `{query,tags,site,space,
  limit}` → `{atoms:[Atom]}`. Used best-effort to enrich agent kickoffs.
- `POST /api/v1/knowledge/:namespace/atoms` — upsert one `Atom`
  (`{id,content,links[],tags[],source,scope:{namespace,site,space}}`), idempotent
  on `id` or `source.ref+hash`; carries `links`, so no separate link endpoint.
- `namespace` is the product-level key shared by all of a product's repos;
  `site`/`space` are query filters. Atom retrieval augments (does not replace) the
  existing `{site,space}` gateway RAG; `MessagesContext.Namespace` lets the
  gateway do nucleus-aware RAG server-side when ready.
- Open questions: atom write governance (direct vs proposed), namespace authz for
  a site-scoped token, retrieval semantics (vector/tag/graph-walk; link
  expansion), and whether the gateway honors `MessagesContext.Namespace` vs the
  CLI fetching+injecting.
