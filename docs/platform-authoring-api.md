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
| `POST` | `/api/v1/sites/:site/spaces` | idempotent ensure-space (on slug); `parent` nests/reparents a subspace* |
| `PATCH` | `/api/v1/sites/:site/spaces/:space` | partial update; `homePage` pins the space's overview page, `parent` reparents* |
| `POST` | `/api/v1/sites/:site/pages` | upsert a page's blocks → draft + proposal; `collection` files it under a page folder (get-or-create)* |
| `POST` | `/api/v1/sites/:site/release-notes` | versioned release page (used by `documents.as=release`, via `bodyMarkdown`) |

\* Hierarchy fields require `whoami.features["space-hierarchy"]` (see below);
the CLI omits them entirely when the flag is absent.

Read side (`check api`/`check docs`): `GET /api/v1/sites/:site`,
`…/pages?space=`, `…/api-blocks` — unchanged in shape, except the site tree now
returns each space's `parentSpaceId`/`overviewPageId`, each collection's
`spaceId`/`spaceSlug`, and each page's `collectionId` (all nullable).

## Space hierarchy (`space-hierarchy` feature)

Since the platform's hierarchy inversion (migration 0050) the content tree is:

```
Site → Space (+ one level of Subspace via parentSpaceId)
     → Collection (page folder INSIDE a space, self-nesting)
     → Page → Block
```

The CLI consumes it as follows, all gated on `whoami.features["space-hierarchy"]`:

- **`spaces.parent`** (manifest) → `POST /spaces` with `parent` for the default
  space (parent ensured first). On the existing-space path a differing `parent`
  REPARENTS the space — declarative, so re-running `gravity sync` converges.
  One level only; the server 400s deeper nesting.
- **Shared spaces** (`spaces.shared`) → each page upsert carries
  `collection: <product.repo>` (explicit mapping `collection:` wins), grouping
  the repo's pages into a folder the server get-or-creates. The `repo/` slug
  prefix is KEPT — page identity is `(space, slug)` server-side, so the prefix
  is what stops sibling repos' same-named pages from upserting onto each other;
  the collection is presentation.
- **`spaces.home`** → after authoring, `PATCH /spaces/:space {homePage: <slug>}`
  pins the page (by its post-reconcile slug) as the space's overview page. The
  home page must sit flat in the space — the server rejects a collection-filed
  overview page — so the CLI never prefixes or collects it.
- **Degradation**: without the feature flag the CLI strips `collection` from
  upserts, sends no `parent`/`homePage`, and prints one notice. Shared-space
  slugs stay prefixed either way, so content identity is identical on old and
  new platforms.

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
  stale only when its source file changes. **`kind` must be one of the server's
  `CODE_SOURCE_KINDS`** (`route|struct|endpoint|schema|config|cli`) for the block
  to stay machine/hybrid; the CLI emits **`kind:"cli"`** for every repo-file-bound
  block (it pins a repo-relative file + sha256). A `machine`/`hybrid` block whose
  binding kind is unrecognized is **downgraded to `human` and its binding dropped**
  on write (`src/server/doc-agent-content.ts`), which silently disables drift
  checking and re-author updates for it.

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
"nucleus":true,"docs-generate":true,"block-audience":true,
"space-hierarchy":true}`).

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
  blocks carry `audiences` (above) and `ownership`. **The CLI binds code only:** a
  `machine` block (an API block or verbatim/code mirror) is pinned to its source
  file's sha256 under **`kind:"cli"`** — the only binding kind the server accepts —
  so `check docs` verifies it; the AI narrative prose around it is `hybrid`/`human`
  and **carries no binding**, so the team can edit the text freely. Idempotent: the CLI reads existing pages
  first and reuses block `key`s to update / omits them to propose removal —
  relying on the merge governance above. **This requires the page read model
  (`GET …/pages`) to return each block's author `key`** (the CLI keys updates off
  it); without it, re-runs can duplicate instead of update.
- **Whole-set durability:** the CLI persists the authored block set to
  `.gravity/generated/docs.json` **before** syncing and authors targets
  independently, so a per-block `400` on one page no longer discards the others,
  and a saved run replays with `gravity docs generate --from <file>` at no AI
  cost. See "Resilience contract" below for what the server owes here.
- **Validated against the server (2026-06-27):** `audiences` (write+read), the
  prompt endpoint, the message gateway (accepts `max_tokens:8192`, no temperature),
  and `whoami.features` (`docs-generate`, `block-audience`, `prompt-endpoint` all
  true) are PRESENT and matched. Remaining gaps:
  1. **Block `key` is not returned on page reads** (`GET …/pages`, `GET …/pages/:slug`):
     the released `ContentBlock` snapshot has no `key`, so re-author idempotency
     falls back to matching by `sourceBinding` `kind:ref`; keyless/bindingless prose
     churns. Server fix: project `key` in the released snapshot + both GET endpoints.
  2. **RESOLVED — AI prose is unbound; the CLI binds code only.** The server
     **hard-rejects** a machine/hybrid block whose binding kind is outside
     `CODE_SOURCE_KINDS` with a `400 bad_request` (`Invalid source binding: Invalid
     option: expected one …`) — it no longer silently downgrades. The CLI used to
     emit `kind:"ai"` on hybrid AI prose, which 400'd the whole page. Fixed: only
     `machine` blocks (API blocks, verbatim/code mirrors) bind — to a single source's
     sha256 under `kind:"cli"`. Narrative prose (`hybrid`/`human`) carries **no
     binding at all**, so the team can freely edit the text and it never drift-locks.
- The server-hosted prompt registry serves `release-notes`, `docs-gap`, `nucleus`,
  `docs-plan`, `docs-author` (the CLI was aligned from `nucleus-distill` → `nucleus`).

## Resilience contract (server-side hardening the CLI wants)

The CLI now survives a partial failure locally (independent per-target authoring +
a saved, replayable block set). These server changes would make the whole flow
robust rather than merely recoverable:

1. **Actionable validation errors.** `Invalid source binding: Invalid option:
   expected one …` truncates before listing the allowed values and omits which
   field failed. Return the field path and the accepted set (e.g.
   `sourceBinding.kind must be one of route|struct|endpoint|schema|config|cli`).
   The CLI surfaces `error.message` verbatim, so a precise message is self-service.
2. **Validate the whole block set up front, report all offenders.** A page upsert
   should 400 with *every* invalid block (index + reason), not just `Block 1`, so
   one round-trip fixes the page instead of N.
3. **Atomic or explicitly partial page upsert.** State whether a rejected upsert
   leaves the page untouched (atomic) or half-applied. The CLI assumes atomic (a
   failed page = no change); confirm it, or return which blocks landed.
4. **Return block `key` on page reads** (gap #1) — without it, re-author
   idempotency degrades to `sourceBinding` matching and keyless prose churns.
5. **Idempotent proposals.** Re-syncing an unchanged page (e.g. on `--from`
   replay after a partial failure) should reuse the open proposal, not mint a new
   one — otherwise replay-to-recover creates duplicate change requests.

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
