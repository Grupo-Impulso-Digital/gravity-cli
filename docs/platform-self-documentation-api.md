# Platform request: self-documentation API for the gravity CLI

> **Status: delivered.** Both requested endpoints — `POST .../spaces` and
> `POST .../pages` — are live. The page upsert lands a **draft page + open
> proposal** with `status: "proposed"` (not `"draft"`), matches incoming blocks
> to existing ones by stable block `key` first, then by `sourceBinding.ref`, and
> machine blocks carry `sourceBinding.kind: "cli"`. One deviation from the spec
> below: there is **no `markdown` block type** — prose/code/table genres are used
> instead. The canonical, shipped contract is
> `gravity/docs/cli-api-contract.md`; the notes inline below annotate where
> delivery differs from this original request. The CLI consumes it via
> `gravity selfdoc`.

**Audience:** the Gravity platform agent (the Workers app behind
`https://gravity.dave-vermette-1.workers.dev`).
**Requested by:** the `gravity` CLI (this repo), so it can maintain a dedicated
space that documents itself.

## Goal

The `gravity` CLI should be able to keep a dedicated space (e.g. `cli`)
up to date on each run:

- **machine blocks** — structured, source-bound content the CLI generates
  (command reference, exit codes, flag tables) that must be verifiable for drift.
- **simple docs** — prose pages (overview, getting started) the CLI seeds and
  humans may then edit.
- **per-run updates** — "depending on the run we do": a `release-notes` run
  updates the changelog section, a `check api`/`check docs` run can record its
  last result, etc.

Today the CLI can **read** everything it needs but can only **write** via
`POST /api/v1/sites/:siteSlug/release-notes`. There is no way to create a space
or author arbitrary pages/blocks. This document specifies the two endpoints that
unblock the full flow. **Minimum to unblock = endpoints 1 + 2.**

## Match these existing conventions

The CLI's HTTP client (`internal/api`) already assumes all of the following — new
endpoints must follow the same conventions so no client rework is needed:

- **Base + auth:** `https://<host>/api/v1/...`, `Authorization: Bearer sk_live_…`,
  scoped to the key's organization. `/app` is the dashboard SPA, **not** the API.
- **Error envelope:** non-2xx returns `{"error":{"code":"...","message":"..."}}`.
- **Governance model:** machine writes do **not** publish directly. The existing
  `release-notes` endpoint creates a **draft page + a proposal** and returns a
  `reviewUrl`. New authoring endpoints should mirror this — write a draft +
  proposal, return `{ pageId, pageSlug, proposalId, status, reviewUrl }`.
- **Existing shapes the client already models** (`internal/api/rest.go`) — reuse
  them verbatim in responses:

  ```jsonc
  Space:        { "id", "slug", "name" }
  SourceBinding:{ "kind", "ref", "hash", "generator" }
  ContentBlock: { "id", "type", "ownership", "content", "sourceBinding", "position" }
  Page:         { "id", "slug", "title", "spaceSlug", "version", "releasedAt", "blocks": [ContentBlock] }
  ```

  `ownership` is one of `machine | hybrid | human`. `check docs` / `check api`
  already verify `sourceBinding.hash` against repo files, so every machine block
  must carry a `sourceBinding`.

---

## Endpoint 1 — Ensure a space (idempotent create)

> **Delivered as specified.** Idempotent on `slug` (200 if it exists, 201 if
> created), returns the `Space` shape.

```
POST /api/v1/sites/:siteSlug/spaces
```

Request:

```json
{
  "slug": "cli",
  "name": "Gravity CLI",
  "description": "Self-documenting space maintained by the gravity CLI."
}
```

Behavior:

- If a space with `slug` already exists in the site, return it (`200`); otherwise
  create it (`201`). **Idempotent on `slug`** so the CLI can call it on every run.
- Scope to the API key's org/site; reject cross-site slugs with the standard
  error envelope.

Response (`Space` shape):

```json
{ "id": "spc_…", "slug": "cli", "name": "Gravity CLI" }
```

---

## Endpoint 2 — Upsert a page + its blocks (draft + proposal)

> **Delivered, with two deviations from the spec below.** (1) The returned
> `status` is **`"proposed"`**, not `"draft"` — repeated upserts of the same page
> reuse the same open proposal (keyed `cli-upsert:<pageId>`) rather than
> accruing duplicates. (2) Block matching is by stable `key` **first**, then by
> `sourceBinding.ref` — exactly the merge governance shipped. There is **no
> `markdown` block type**; the CLI emits `prose`/`code`/`table` genres instead.
> Machine blocks use `sourceBinding.kind: "cli"`.

```
POST /api/v1/sites/:siteSlug/pages
```

Request:

```json
{
  "spaceSlug": "cli",
  "slug": "command-reference",
  "title": "Command Reference",
  "summary": "Auto-generated reference for the gravity CLI.",
  "blocks": [
    {
      "key": "commands",
      "type": "api",
      "ownership": "machine",
      "content": { "...": "block-type-specific payload" },
      "sourceBinding": {
        "kind": "cli-source",
        "ref": "internal/cli",
        "hash": "sha256:…",
        "generator": "gravity selfdoc v0.1.0"
      },
      "position": 0
    },
    {
      "key": "overview",
      "type": "markdown",
      "ownership": "hybrid",
      "content": { "markdown": "## Overview\n…" },
      "position": 1
    }
  ]
}
```

Behavior:

- **Upsert by `(spaceSlug, slug)`** — create the page if absent, otherwise update.
- Mirror `release-notes`: produce a **draft + proposal**, do not publish. Return
  the same shape as `ReleaseNotesResponse`.
- **Block identity & merge:** match incoming blocks to existing ones by `key`
  (a stable client-supplied id) or by `sourceBinding.ref`. Then:
  - `machine` blocks → replace/update from the payload (this is the CLI's to own).
  - `human` blocks → never overwrite.
  - `hybrid` blocks → update machine-managed fields, preserve human edits (or
    surface the conflict in the proposal for review).
  - machine blocks present on the page but absent from the payload → mark stale /
    propose removal (don't silently delete).
- Persist `sourceBinding` as-is so `gravity check docs` can later flag drift.

Response (`ReleaseNotesResponse` shape):

```json
{
  "pageId": "pg_…",
  "pageSlug": "command-reference",
  "proposalId": "prop_…",
  "status": "draft",
  "reviewUrl": "https://…/app/…/proposals/prop_…"
}
```

---

## Read endpoints — already sufficient, no change

`GET /api/v1/sites/:siteSlug`, `…/pages`, `…/api-blocks` already return the tree,
page snapshots, and API blocks the CLI uses to verify bindings. Nothing new
needed for the read/verify side.

## Acceptance criteria

1. `POST …/spaces` is idempotent on `slug` and returns the `Space` shape.
2. `POST …/pages` accepts the block array, creates a **draft + proposal**, and
   returns `{ pageId, pageSlug, proposalId, status, reviewUrl }`.
3. Re-running with identical content does not duplicate pages or blocks.
4. Machine blocks round-trip their `sourceBinding` (verifiable by `check docs`).
5. Human/hybrid content survives a machine re-publish.
6. All errors use the `{"error":{"code","message"}}` envelope; auth failures are
   `401`/`403`.

## What the CLI will do with these (consumer contract)

A new `gravity selfdoc` command (this repo) will, per run:

1. `POST …/spaces { slug: "cli" }` — ensure the dedicated space exists.
2. Generate a **command-reference** page from the cobra tree (machine blocks,
   `sourceBinding` → CLI source) + an **overview** page (hybrid prose), and
   `POST …/pages` for each.
3. Optionally append a per-run note (which command ran, range, findings) so the
   space reflects what was actually executed.

Until endpoints 1–2 exist, the CLI's first staging run uses the existing
`release-notes` path pointed at the `cli` space (proves connectivity + the
draft/proposal write path) — see this repo's README/setup.
