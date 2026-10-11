# Harness v2 contract: Gravity CLI ⇄ Gravity platform

Status: FINAL v4 (2026-10-11), after three review rounds (CLI and server reviewers: NO BLOCKERS). Normative.
Builds: gravity-cli (Go, release 1.2.0) and gravity platform (TS). Audits: scratchpad `audit-cli.md`, `audit-server.md`.

## 0. Goals and non-goals

Goals
- G1. The harness works like Claude Code: many turns, incremental page drafting through tools, within the model's real limits. No page has to fit in one reply; no list phase has to fit in one reply.
- G2. Harness and tools live in the CLI. Every instruction lives in Gravity, resolved per run pass from org/site/space/collection/pass/run, fetched by the CLI at launch, changed by a server deploy (no CLI release). The CLI's kickoff messages carry DATA only.
- G3. Gravity enforces real limits: the model's max output and context window (reported by the provider/catalog), and the repo's existing run/month budgets. Harness safety caps (turns, tool calls) are served by Gravity in the bundle, are generous, and a page that hits one fails loudly.
- G4. Claude, GPT and Gemini all work. Gemini is reached through Origin (Anthropic wire). The direct OpenAI adapter is fixed. Tool schemas are flat and portable (no anyOf/oneOf, no strict mode dependency).
- G5. Instructions are well written, sectioned, provider-neutral, and tell the model everything it needs: output language, audiences with meanings, block contract, exclusions, its limits and step size, mode.
- G6. Deterministic checks on authored blocks catch what instructions can't guarantee and make the model fix them before submit.
- G7. Model quality is measured with an eval fixture and shown as a label; the CLI warns before spending on a "not recommended" model.

Non-goals (1.2)
- Direct Gemini adapter; DB-editable prompts (prompts are code-owned, read-only viewer only); draft resume across processes; moving existing blocks; nested block types (tabs, expandable, codegroup) in AI authoring.

## 1. Compatibility

- Old CLIs (≤1.1.x) keep working. Calls to `/api/llm/v1/messages` WITHOUT `context.instructionsVersion` keep legacy semantics: the gateway composes layers, glossary, recall and server tools; errors keep `provider_error`/502 (with added `details.reason`, `details.param`). They also get, through the run-pass snapshot: the new `language`, `parent_space` and per-scope `voice` layers (§2.5), the strict glossary filter, streaming upstream (§3.3), model attribution headers.
- CLI 1.2 requires `GET /api/llm/v1/instructions` with `schema: "gravity.instructions/1"`. On 404 or an unknown schema major: stop with `this Gravity server is too old for gravity 1.2 (no instructions bundle); upgrade the server or use gravity 1.1`. Baked prompts are deleted from the CLI.
- Tool contract: `gravity.tools/1`. The bundle states the tool contract its prompts target; the CLI refuses a different major.
- `/api/llm/v1/prompts/<name>` keeps serving the legacy pass-* prompts for old CLIs.

## 2. Instructions bundle

### 2.1 Endpoint

`GET /api/llm/v1/instructions?runPassId=<id>[&model=<id>]`

- One mode only. Dry runs (`gravity run --dry-run`, PR trigger) are server runs with mode=dry and have run passes, including manifest-overlay passes with `pass_id` NULL.
- Auth: identical to `/messages` run context (`resolveGatewayRunContext`): a repo token with scope `llm` whose repo owns the run, or the user/org principal that started the run. The run must be `running`, the pass `pending|running`.
- `model`: optional override (CLI `--model`). Resolved with the single resolver (§4.2). Recorded race-safely on first fetch: `UPDATE pipeline_run_pass SET model=?, model_provider=? WHERE id=? AND model IS NULL`. A later fetch whose resolved model differs ⇒ 409 `model_conflict`.
- `maxOutput`: optional dev override (CLI hidden `--max-output`); can only LOWER `limits.maxOutputTokens` (clamped to ≤ the real max) for this bundle (and thus `stepTokens` and the rendered limit text); ignored for repo tokens.
- On every 200 the server sets `instructions_version` on the run pass (last fetch wins).
- Errors `{error:{code,message,details?}}`: 401 `unauthorized`; 403 `forbidden`; 404 `not_found`; 409 `run_not_running`; 409 `model_conflict`; 409 `model_unavailable` (`details.reason`: `not_enabled|no_key|catalog_unavailable`, `details.requested`); 402 `no_provider_key`.
- Computed per request (no cache, no ETag). Glossary is read live, so `version` can change during a run; the CLI fetches once per run pass.

`gravity show` uses the tenant tRPC procedure `passes.render {runPassId}` (requirePermission('docs.read') at the run pass's target scope, caller's org only); the platform admin view uses `platform.instructions.render` (cross-tenant, audited). Both share one loader (no run-status/principal checks) and `renderHarnessSystem`. Neither uses this endpoint.

### 2.2 Response

```jsonc
{
  "schema": "gravity.instructions/1",
  "version": "sha256:<hex>",           // sha256(canonicalJson({model, harness, tools, rendered}))
  "toolContract": "gravity.tools/1",
  "model": {
    "provider": "origin|anthropic|openai",
    "id": "anthropic/claude-sonnet-4-6",
    "label": "Claude Sonnet 4.6",
    "requested": "claude-sonnet-4-6",          // what config or --model asked for; null if default
    "fallback": null,                          // or {"from": "<id>", "reason": "<text>"} (only when org allows fallback)
    "limits": { "maxOutputTokens": 64000, "contextWindow": 200000, "known": true },
    "features": { "forcedToolChoice": true },
    "quality": "recommended|supported|not_recommended|untested",
    "qualityNote": null
  },
  "language": { "code": "en", "name": "English" },
  "audiences": [ { "key": "developers", "label": "Developers", "description": "…", "builtin": true } ],
  "harness": { "maxTurns": 80, "maxToolCalls": 200, "stepTokens": 4000, "maxContinueNudges": 4 },
  "blocks": {
    "types": ["heading","prose","list","code","table","callout","quote","divider"],
    "calloutVariants": ["info","success","warning","danger"],
    "listVariants": ["bulleted","numbered"]
  },
  "checks": { "outputMarkers": ["@internal","TODO:","FIXME","XXX"] },
  "phases": {                                 // phase -> prompt name@version (audit; the text is in rendered)
    "guides.plan": "guides-plan@<hex8>",
    "guides.write": "guides-write@<hex8>",
    "reach.write": "reach-write@<hex8>",
    "reference.prose": "reference-prose@<hex8>",
    "changelog.write": "changelog-write@<hex8>",
    "nucleus.distill": "nucleus-distill@<hex8>",
    "check.review": "check-review@<hex8>",
    "units.map": "units-map@<hex8>"
  },
  "rendered": { "system": { "<phase>": "<full system text>" } },
  "tools": { "<toolName>": "<description>" },  // descriptions for every gravity.tools/1 tool (§5.6); CLI uses these verbatim
  "warnings": [ "Model 'auto' lets Origin choose per call; pin a model for documentation runs." ]
}
```

Only phases relevant to the run pass's kind are present, plus `units.map` in every AI run-pass bundle.

### 2.3 Field resolution

- `language`: the SITE's `defaultLanguage` (`siteTargetLanguages(site).defaultLanguage`), default `en`. `name` is the English name of the language. structure_locale is not involved. `units.map` renders in English regardless.
- `audiences`: each pass audience key → built-in (code constants: label + description, written in `src/server/harness/audiences.ts`, aligned with built-in labels Everyone/Logged in/Developers) or `audience_group` by `key` (name, description). Unknown key → `{key, label:key, description:null, builtin:false}` + a `warnings[]` entry.
- Layers (rendered into `system`): exactly the run pass's `instructions_snapshot`, which from now on includes `language`, `parent_space` and per-scope `voice` layers (§2.5). Never recomposed from current rows.
- Glossary (rendered into `system`): `listGlossaryRules` gains option `{language, strict:true}`: terms with `enforceInAi`, action ≠ 'off', cascaded space→site→org (a collection target uses its space), where `language = language.code` OR `verbatim = true`; no fallback to other languages; returns `verbatim`. Independent of the memory module (also for the legacy gateway path).
- Empty pass audience list ⇒ Context line `Audiences: all readers of this space`.
- `model`: §4.2. `limits` from `ai_model_profile` (§4.1) overlaid by Origin's `max_output_k`/`context_k` when present. Origin `auto` → `known:false` defaults, quality `untested`, warning.
- `harness`: platform defaults (constants in code for 1.2): maxTurns 80, maxToolCalls 200 per page/phase, stepTokens = min(4000, floor(maxOutputTokens/2)), maxContinueNudges 4.
- `tools`: descriptions are server-owned text (`src/server/harness/tools.ts`), written for gravity.tools/1.

### 2.4 Rendered system prompt

`renderHarnessSystem(bundle, phase)` — one pure function, used by the endpoint, the admin render and tests. Order (static first so the prefix is cacheable):

```
<prompt text for the phase, placeholders filled: {{language.name}}, {{language.code}}, {{model.limits.maxOutputTokens}}, {{harness.stepTokens}}>

## Context
<context>
Site: <site name> · Space: <space name> · Collection: <path>
Output language: <name> (<code>)
Audiences:
- <label> (<key>): <description>
</context>

## Organization instructions (binding; most general first, later wins on conflict)
<org_instructions source="org" label="Organization voice">…</org_instructions>
<org_instructions source="site" label="Site brief: …">…</org_instructions>
… parent_space, space, collection(s), voice layers, pass, run …

## House terminology (mandatory)
- Use "<term>" … (avoid: …)
```

Context names are the base (source-language) names. Phases `units.map` omit space/collection/pass layers.

### 2.5 Snapshot layers (server, legacy and v2)

`composeLayers` (used by plan and run start, so hashes stay consistent) adds:
- `{source:'language', label:'Output language', text:'Write every reader-facing text in <name> (<code>), whatever the language of the sources, commits or instructions.'}` placed FIRST.
- Voice: for each scope row (site, parent space, space, collections) read RAW `settings.voice.tone` and `settings.voice.style` (not the merged cascade); emit `{source:'voice', id, label:'Voice: <scope name>', text: tone + newline + style}` right after that scope's brief; skip when identical to the org voice text. Extend `InstructionSource`.
- New runs get new instructionsHash values; in-flight runs keep old snapshots (v2 still gets language from Context). capture.ts briefs will include these layers too (accepted).

## 3. Messages gateway (`POST /api/llm/v1/messages`)

### 3.1 Request

- `context.instructionsVersion?: string` — present ⇒ v2 semantics (§3.2–3.5). Gateway appends NO layers, glossary, recall context or server tools; uses `system` as sent. Mismatch with the run pass's recorded version → still served, audited.
- `context.phase?: string` — free text, audit only. `context.purpose` keeps its enum.
- `model?` top-level — v2: must equal the run pass's recorded model or be absent (absent ⇒ recorded model); different ⇒ 409 `model_conflict`. Legacy: ignored as today.
- `system: string | TextBlock[]` (TextBlock = `{type:'text', text, cache_control?}`). OpenAI: texts joined with blank lines. Legacy (no instructionsVersion): flattened to a string before composing. v2 anthropic/origin: forwarded as blocks.
- v2 call on a run pass whose `model` is NULL ⇒ 409 `bundle_required`. The gateway re-checks the recorded model's key on every v2 call: unusable ⇒ 409 `model_unavailable` (never fallback mid-run).

### 3.2 Response

Anthropic Messages shape. Headers: `x-gravity-model`, `x-gravity-provider`, `x-gravity-fallback: 0|1`. `usage` includes `cache_read_input_tokens`/`cache_creation_input_tokens` when reported (assemblers and `toGatewayMessage` must carry them).
Cost accounting (`estimateCostUsd` gains cache-token parameters so `ai.usage` and run budgets agree): `cost = (input + 1.25·cache_creation + 0.1·cache_read)·inPrice + output·outPrice` (Anthropic/Origin-Claude; OpenAI cached tokens are already inside prompt_tokens); ledgers' `input_tokens` store input + cache_creation + cache_read. Add Gemini prices to MODEL_PRICES.

### 3.3 Upstream and stop reasons (v2 and legacy)

- The gateway always calls providers in streaming mode (existing `streamProvider` + assemblers: streamAnthropic, streamOrigin, streamOpenAI) and returns one assembled non-streaming message. `request.signal` is passed through so a client disconnect aborts upstream. A stream that ends without `message_stop` (Anthropic/Origin) or without `finish_reason` (OpenAI) ⇒ 502 `provider_error` (retryable), never a success.
- Content is filtered to `text | tool_use`.
- `stop_reason ∈ end_turn | tool_use | max_tokens | refusal`. OpenAI `content_filter`, Anthropic `refusal` → `refusal`; `pause_turn`, `stop_sequence`, unknown → `end_turn` (+ log).
- v2: on `max_tokens`, EVERY tool_use is removed from content (text kept). Legacy: when `stop_reason=max_tokens` and the LAST content block is a tool_use, that block is removed; no `{_raw}` objects.
- Provider context-length errors (incl. `model_context_window_exceeded`) → 413 `context_too_large` (v2) / `provider_error` with `details.reason: context_too_large` (legacy).

### 3.4 Limits

- `max_tokens` clamped to `limits.maxOutputTokens`. v2 absent ⇒ the model's max. Legacy absent ⇒ 4096 (unchanged).
- Budgets unchanged: 402 `budget_exceeded` (repo `budget.maxCostUsdPerRun` / `maxCostUsdPerMonth`, checked before each call).
- Gravity's own LLM rate limit: `RL_TOKEN_LLM` per token, 120 per 60 s (update `LIMITER_WINDOWS` in ratelimit.ts and the commented wrangler `ratelimits` blocks together, keeping the existing agreement test green). 429 `rate_limited` with `Retry-After`.

### 3.5 Errors (v2)

- 400 `provider_bad_request` (sanitized message, `details.param`), 502 `provider_error` (upstream 5xx), 429 `rate_limited`, 413 `context_too_large`, 409 `model_unavailable` / `model_conflict` / `bundle_required`, 402 `budget_exceeded`/`no_provider_key`.

### 3.6 Prompt caching

The CLI sends `system` as one text block with `cache_control: {type:"ephemeral"}` and one breakpoint on the last content part of the last message. The gateway forwards `cache_control` (system blocks and message parts) only when the resolved model is a Claude model (provider anthropic, or origin with bare id starting `claude`); otherwise it strips every `cache_control` (OpenAI caches automatically).

## 4. Models

### 4.1 `ai_model_profile` (migration 0026)

Migration `0026_harness_v2.sql` (additive): the table below + seeds; `ALTER TABLE org_ai_settings ADD COLUMN allow_model_fallback boolean NOT NULL DEFAULT false`; `ALTER TABLE pipeline_run_pass ADD COLUMN instructions_version text, ADD COLUMN model text, ADD COLUMN model_provider text`. Kysely types in `src/server/db/schema/{platform,pipelines}.ts`. tRPC `ai.setAllowModelFallback` (adminProcedure) + toggle in AI settings.

```sql
CREATE TABLE ai_model_profile (
  model_key text PRIMARY KEY,                    -- bare lowercase model id (bareModelId), e.g. 'claude-sonnet-4-6', 'gpt-4o', 'gemini-2.5-pro'
  max_output_tokens int, context_window int,
  supports_forced_tool_choice boolean NOT NULL DEFAULT true,
  quality text NOT NULL DEFAULT 'untested' CHECK (quality IN ('recommended','supported','not_recommended','untested')),
  quality_note text, updated_by_email text, updated_at timestamptz NOT NULL DEFAULT now()
);
```
Lookup = longest `model_key` prefix of the bare id (`bareModelId`, same rule as `priceForModel`), so dated ids match. Seeds in the migration with `INSERT … ON CONFLICT DO NOTHING` (OpenAI gpt-4o 16384/128000, gpt-4.1, gpt-5 family; Anthropic Claude 4.x family; Gemini 2.5 pro/flash), all quality `untested` (later seed corrections need a new migration). Origin catalog `max_output_k`/`context_k` (×1000) are carried by `CatalogModel`/`toCatalogModel` and override the profile limits. Missing row or NULL limits ⇒ `known:false`, defaults 8192 / 128000. Platform admin edits rows (tRPC `platform.ai.profiles.list|upsert`). `ai.models` rows gain `limits`, `quality`, `qualityNote`; the AI settings UI shows a quality badge and note.

### 4.2 Resolution and fallback

- One resolver `resolveRunModel(scope, requested?)` returning `{target, requested, fallback|null}`: requested (`--model` or scope `ai.model` from collection→space→site→org default) matched on exact id, then bare id; must be enabled and keyed.
- Not usable ⇒ if `org_ai_settings.allow_model_fallback` (new column, default false) ⇒ fallback to the org default model, reported; else 409 `model_unavailable`. Origin catalog empty/unreachable ⇒ `catalog_unavailable` (never a silent default flip).
- Plan time: `plan.ts` calls `resolveRunModel` per AI pass scope (inside the passes map), honouring the plan query's `model`; `PlanPass` carries `model{id,label,quality,limits,fallback, unavailable?:{reason}}` and `language{code,name}`. Run start (`run-api.ts`, in the per-pass loop before the transaction) throws `PipelineError('model_unavailable', 409, {details})` if ANY selected AI pass is unusable: the whole run fails before a lease. `StartRunInput` and the plan query zod schemas gain `model?`; an unknown or unusable model yields `PlanPass.model.unavailable` (not a 400). Non-AI kinds (verbatim, capture) and skipped passes are not checked.
- Legacy calls keep today's fallback but set `x-gravity-fallback: 1`.

### 4.3 OpenAI adapter

`max_completion_tokens` always; `parallel_tool_calls:false` when tools are present; `tool_choice none → "none"`; `tool_result.is_error` → text prefix `[tool error] `; stop mapping per §3.3; unparseable tool args ⇒ treated as cut (`max_tokens` semantics). No `strict`.

## 5. CLI harness v2 (`gravity.tools/1`)

### 5.1 Launch and plan

- Plan view (pre-spend confirm) shows per AI pass: model label, quality, language, fallback. `not_recommended` → warning line (`--yes` still proceeds). New flag `--model <id>`.
- Plan: `--model` is sent on the plan query and `StartRunInput`; any `PlanPass.model.unavailable` ⇒ stop before the confirm with the reason.
- Right after StartRun and BEFORE ingest/map-units: fetch the bundle of every AI run pass (fail fast on 409); store at `.gravity/runs/<runId>/<runPassId>.instructions.json` only under `--debug`; print `model · quality · language · instructions <version8>`. Every AI run-pass bundle includes the `units.map` phase; map-units uses the bundle of the run pass that hosts it.
- `PassReportRequest.generation = {instructionsVersion, model, provider}` (optional). Server: add `generation` to `reportSchema` (runs.ts); when the run pass's `model` or `instructions_version` is NULL (replayed run via `--send`), fill them from it. `RecordedPass` gets the same `generation` object plus `usage{turns (from Usage.Calls), inputTokens, outputTokens}`. `RecordingVersion` stays 1; `gravity run --send` forwards `generation`.

### 5.2 Loop

- One conversation per phase task; `system = rendered.system[phase]` as one cached block; tools from §5.6 with `bundle.tools` descriptions.
- `max_tokens` per call = min(limits.maxOutputTokens, contextWindow − estimatedInput − 2000). `estimatedInput` = last response `usage.input_tokens` + cache tokens + bytes/3 of content appended since; on the first call bytes/3 of system + kickoff + all tool schemas JSON. If the result is < `stepTokens`, elide first. On 413 `context_too_large`: elide once and retry; then fail `context_limit`.
- Never send an empty assistant message: if the assistant content is empty after filtering, append assistant text `(output truncated)` (or `(no output)`).
- Stop reasons:
  - `tool_use` → execute every tool_use in order (terminal last, only if all earlier calls succeeded); one user message with all tool_results.
  - `max_tokens` (truncated) → user text: `Your last reply hit the output limit (~<maxOutputTokens> tokens) and its tool calls were discarded. Work in smaller steps: at most ~<stepTokens> tokens per tool call.` 3 consecutive → task fails `output_limit`.
  - `end_turn` without terminal → user text `Continue. Call <terminal> when the work is complete.` up to `maxContinueNudges`; then fail `no_submit`.
  - `refusal` → task fails with the refusal text.
- A FORCED terminal of a list phase (`finish_plan`, `finish_atoms`, `finish_units`, `finish_review`, `finish_release_notes`) is treated as partial: `finish_units` ⇒ `complete:false` regardless of input; the pass report states "partial (forced at cap)".
- Caps: `harness.maxTurns`, `harness.maxToolCalls` per task. At cap−3 turns the next calls force the terminal tool (`tool_choice {type:tool}` when `features.forcedToolChoice`, else a user text). A forced `submit_page` with `error` findings fails the page.
- No CLI token budget. `budget_exceeded` and `no_provider_key` HALT the run: no new task or pass starts, in-flight tasks are cancelled, pages already sent stay, then the CLI calls `FinishRun{status: failed, summary: <reason>}` normally. Implemented as a new `api.HaltsRun(err)`; `StopsRun` stays reserved for errors where the server already ended the run.
- LLM calls use a per-request deadline of 15 min (other calls keep 120 s); retries 429/5xx/network honouring Retry-After, with a total cap of 30 min per call. The existing lease heartbeat goroutine (execute.go) already runs independently of model calls.
- Context management: when estimatedInput > 60% of contextWindow, replace the content of the oldest tool_results (never the latest `draft_view` or the kickoff) with `[elided: <tool> <args summary>; call it again if needed]` in one batch until < 40%.

### 5.3 Page draft workspace (phases `guides.write`, `reach.write`, `reference.prose`)

State per page: ordered `blocks: [{key, type, content, audiences?, units?, rationale, locked, origin: seed|new}]`, persisted (debug only) at `.gravity/runs/<runId>/<runPassId>/<pageId|new-<slug>>.json`.

Seeding:
- Update pages: from `GET /api/v1/content/pages/:pageId?state=draft&format=json` (as today). Block is EDITABLE iff `ownership ≠ human` AND `type ∈ blocks.types` AND binding kind not `api|verbatim` AND (hybrid ⇒ clean, from a `clean` flag the server adds to the page JSON) AND (reach ⇒ bound to a reach unit). Others are `locked:true`.
- Create pages: empty.
- Reference prose: the run's computed api blocks (`refPage`, locked) merged with the existing page.
- Pages with a verbatim `lock` are never opened (planner may not target them; guides/reach skip them as today).

Mapping draft → `ChangeRequest` (at successful `submit_page`):
- Diff final draft vs seed on normalized `type, content, audiences, units` (rationale-only changes don't count).
- `upserts` = new + changed editable blocks; each upsert's `after` = key of the previous block in final order, `"^"` when first.
- `removeBlockKeys` = deleted editable seed keys. A key is never both upserted and removed; deleted keys are never reused.
- Create: `Op create`, `Target{SpaceID, Slug, CollectionPath}`, `Title`. Update: `Target{PageID}`; title unchanged.
- Empty diff ⇒ Unchanged (no change sent). Provenance (`withProvenance`) as today. Ownership of new blocks: hybrid as today.
- New keys: `guide:<slug>:<n>`, n = 1 + max over all seed keys (deleted included) matching `^guide:<slug>:(\d+)$` (0 if none), incrementing per new block; slug part cut so the key is ≤ 128 chars. Reference prose: `ref:<slug>:<n>` same rule. Existing keys accepted as-is.

Server requirements this implies (platform build):
- merge.ts: `after:"^"` ⇒ splice at index 0, and the existing `lastAfter` chaining also applies after `^` so consecutive top inserts keep their order. `after` applies to CREATED blocks only (updated blocks keep position). An `after` that names a removed or unknown key appends at the end (unchanged).
- merge.ts: skip the unique-binding fallback when the binding key starts with `ai:` (match by block key only).
- content-api.ts page serializer (`GET /api/v1/content/pages/:pageId`, state=draft, format=json): hybrid blocks carry `clean: boolean` (cleanlinessOf), and every block carries `sourceBinding.kind`.

### 5.4 Planner (`guides.plan`)

Accumulating tools: `add_pages {pages:[Page]}` (validated, non-terminal, may be called many times) and `finish_plan {summary}` (terminal).
`Page = {action: create|update|deprecate, pageId?, slug, title, collectionPath?:[string], units:[string], mustCover:[{token?: string, text: string}], why: string}`
- `mustCover`: what the page must cover. `token` (an API path like `GET /topics/{id}`, an identifier, an option name) is machine-checked; `text` is advisory, in the site language. Internal/temporary items must not be listed (prompt rule).
- `why`: reviewer-facing; shown in the change request; given to the writer under "Planner notes" as data.
- Post-processing as today (drop unknown/locked/out-of-target/duplicate slugs, each logged). `add_pages` beyond `options.maxPages` ⇒ tool error `page budget reached (<n>)`.
- Deprecate: with pass option `deprecate:true` ⇒ write phase in mode `deprecate`; `deprecate:false` ⇒ deterministic delete as today, no model.
- PR preview (impact mode) stops after the plan as today.

### 5.5 Writer kickoff (data only)

```
<task mode="create|update|deprecate|reach|reference-prose">
Page: <title> (slug <slug>) · Target: <space>/<collection path>
Editable: all | keys: …
</task>
<must_cover>
- [token: GET /topics/{id}] Retrieve one topic …
</must_cover>
<planner_notes>…why…</planner_notes>
<units>…UnitDetails…</units>
<change_summary>…ChangeSummary…</change_summary>
```
No imperative sentences in Go-owned text. The current page is NOT inlined; the model calls `draft_view`.

### 5.6 Tools (`gravity.tools/1`)

Repo tools (all phases): `read_file{path, ref?, offset?, limit?}` (line ranges; every capped response states `lines a–b of N; call again with offset=<b+1>`), `list_files`, `grep`, `git_log`, `git_show`, `git_diff{…, offset?, limit?}`.
Doc tools: `read_page{slug|pageId, offset?, limit?}`, `search_docs`, `recall`, `inventory`, `list_target`.
Draft tools (write phases):
- `draft_view {}` → `[key] (locked)? type: text…` per block, then check status summary.
- `draft_append {after?: key|"^", rationale?: Rationale, blocks:[Block]}` → inserts after `after` (default end); `rationale` applies to every block in the call that has none; returns assigned keys.
- `draft_replace {key, block: Block}` (editable keys only).
- `draft_delete {keys:[key]}` (editable keys only).
- `check_page {}` → findings (§5.7).
- `raise_hint {kind, claim, unitKey?, blockKey?, detail, forRepos?:[string]}` (non-terminal; collected and sent as today).
- `submit_page {summary, uncovered?:[{token, reason}]}` (terminal) → runs checks; any error ⇒ tool error listing findings; success ⇒ §5.3 mapping.
- Reach mode (`reach.write`) exposes only `draft_view`, `draft_replace`, `check_page`, `raise_hint`, `submit_page` (E7 and `uncovered` do not apply).

`Block` (flat, portable): `{type (enum blocks.types), text?, level? (1|2|3), language?, variant?, rows?: [[string]], header?: boolean, audiences?: [enum of pass audience keys], units?: [string], rationale?: Rationale}`; `Rationale = {summary, sourceRefs?:[string], commits?:[string]}` (optional per block; provenance is back-filled by `withProvenance`). The CLI validates per type and returns precise errors: heading needs text+level; prose/quote need text; list needs text + variant (bulleted|numbered), one item per block; code needs text + language; table needs rows (+header); callout needs text + variant; divider nothing else. Conversion to server content: prose `{text}`, quote `{text}`, heading `{text, level}`, code `{text, language}`, callout `{text, variant}`, list `{text, variant}` (one item per block), table `{rows, header}` (header default true), divider `{}`. `NormalizeContent`: `task` list variant ⇒ `bulleted`; remove the join of `items` into one block.

List phases (accumulate + finish), each item validated on add (tool error per invalid item, valid ones kept):
- plan: `add_pages` + `finish_plan` (§5.4).
- changelog: `add_release_items {items:[{section: enum ReleaseSections, text, commits?:[string], units?:[string]}]}` + `finish_release_notes {summary}` (the CLI groups items by section; `ReleaseNotesInput.Title` dropped).
- nucleus: `add_atoms {atoms:[Atom as today]}` + `finish_atoms {}`; `maxAtoms` enforced across calls (tool error beyond).
- check: `add_findings {findings:[ClaimFinding as today]}` + `finish_review {summary}`.
- units: `add_units {units:[Unit as today]}` (a later duplicate key replaces the earlier) + `finish_units {complete: boolean}`. Map-units ingests with `Complete:true` only after a successful `finish_units{complete:true}` on a non-truncated turn; otherwise ingest nothing.

### 5.7 Deterministic checks (`check_page`, on authored or changed blocks only)

- E1 language (error): over authored reader-facing text (prose, heading, list, callout, quote, table cells; not code) of ≥ 150 words, a stopword-ratio detector for en/fr/es/de/pt finds another supported language clearly dominant (ratio > 2× the site language). Below 150 words or unclear ⇒ W.
- E2 code in prose (error): a prose/list/callout/quote text containing a fenced block, or a JSON object/array ≥ 20 chars that parses, or ≥ 3 consecutive lines indented ≥ 4 spaces.
- E3 markdown heading in text (error): text starting with `#{1,6}\s` or consisting only of `**…**`. Short line without period ⇒ W only.
- E4 planning voice (error): `/\b(this (section|page)|the (section|page)) (should|will|must|is meant to) (document|describe|cover|include|explain|list)\b/i`, French equivalent, and mentions of "the planner", "the draft", "the outline".
- E5 output markers (error): any of `checks.outputMarkers` in authored text.
- E7 coverage (error): a `mustCover` item with `token` not found in authored or existing page text (case-insensitive, backticks ignored, `{param}` ≡ `:param`) and not declared in `uncovered`.
- W1 page < 120 words on create; W2 duplicate adjacent blocks.

### 5.8 Failure semantics

A failed page task sends nothing for that page (no partial draft). Pages already sent stay. The pass reports failed with the per-page list (as today). `budget_exceeded`/`no_provider_key` halt the run (§5.2). `output_limit`, `no_submit`, `refusal`, cap exhaustion are reported per page with the model id.

### 5.9 Units map

Run-level, hosted on the first AI pass's run pass (as today). Its bundle phase `units.map` renders without space/collection/pass layers, in English.

## 6. Prompts (server-owned, `src/server/harness/prompts/*.ts`)

Names: `guides-plan`, `guides-write`, `reach-write`, `reference-prose`, `changelog-write`, `nucleus-distill`, `check-review`, `units-map`. Each uses `##` sections in order: Role, Task, How to work (tools and steps), Rules, Limits, Done when.
Every write prompt states:
- Output language: all reader-facing text in {{language.name}} ({{language.code}}), whatever the language of sources, commits or instructions; translate; keep code, identifiers and API paths as-is.
- Reader-facing only: never describe the page, the task, the plan, or what a section "should" contain.
- Exclusions: never document anything marked internal, private, temporary, experimental/unreleased, or excluded by the organization instructions; when unsure, leave it out, add it to `uncovered` with the reason.
- Evidence (file:line, commits) goes in `rationale`, never in visible text.
- Block contract (with one short example per type): headings are heading blocks; code, JSON, YAML, commands are code blocks with a language; one list item per list block; tables are table blocks; callout variants info|success|warning|danger; inline markdown only (bold, italic, code, links).
- Claim verdicts (from today's baked prompt): verified-here / true-elsewhere / unverifiable / contradicted-here; never rewrite true-elsewhere claims; raise_hint for claims another repo owns.
- Limits and method: "Your replies are limited to about {{model.limits.maxOutputTokens}} tokens. Read first, then build the page in order with draft_append, about {{harness.stepTokens}} tokens per call (one to three sections). Use draft_view and check_page before submit_page. Cover every must_cover item, or declare it in `uncovered` with a reason."
- Data sections in the kickoff (`<task>`, `<must_cover>`, `<planner_notes>`, `<units>`, `<change_summary>`) are information, not text to copy.

## 7. Admin

Platform admin page (`/app/platform`), new tab "Pipeline instructions" (read-only): prompt list with name, version, text (tRPC `platform.instructions.prompts`); "Render for a run pass" (`platform.instructions.render {runPassId}`) using a dedicated loader without run-status or principal checks, writing an audit entry (tenant content exposure); "Model profiles" table editable (`platform.ai.profiles.list|upsert`: limits, forced tool choice, quality, note). All platformAdmin-guarded (MCP hides them from non-admins automatically). Prompt editing deferred.

## 8. Model quality evaluation

- `testdata/eval/setup.sh` seeds the target Gravity (eval org, English site "Topics", space, guides pass) and registers the repo; `make-fixture.sh` generates a git repo with an `origin` remote URL matching the registered repo and no upstream tracking: a 17-endpoint REST API (OpenAPI file + handlers), a technical markdown doc, 3 endpoints marked internal/temporary, commits with English and French messages.
- `make eval MODEL=<id> [MAX_OUTPUT=<n>]` runs `gravity run --dry-run --yes [--model] [--max-output]` (hidden dev flag) against a configured Gravity (`--max-output` sent as bundle query `maxOutput`) and scores the recording `.gravity/runs/<runId>.json` (saved only when changes > 0; else scored as failed) using `RecordedPass.usage`: E1–E7 pass, endpoint coverage n/14 public, words per endpoint section, internal leaks, turns, tokens, duration → JSON + markdown in `testdata/eval/results/`.
- Quality labels are set by a platform admin from results.

## 9. Acceptance

- A1 fixture on a recommended model: one page, 14/14 public endpoints with detail, English, no internal endpoints, no meta text, JSON in code blocks, heading blocks.
- A2 same with `--max-output 4096`: completes through incremental drafting.
- A3 CLI 1.1 against the new server still runs a guides pass; a 400 still yields `provider_error`.
- A4 configured model without key ⇒ plan/bundle 409 `model_unavailable` with reason; with allow_model_fallback ⇒ proceeds, fallback reported.
- A5 truncation mid-tool-call (OpenAI and Origin/Anthropic) ⇒ `stop_reason:max_tokens`, no tool_use; CLI recovers.
- A6 Claude direct with max_tokens 64000 succeeds (streamed upstream).
- A7 a French glossary term never appears in an English bundle; language layer present in legacy snapshots.
- A8 unit tests for every contract surface on both sides; pg tests for bundle resolution (language, audiences, layers = snapshot, glossary filter, version stability, auth matrix).
