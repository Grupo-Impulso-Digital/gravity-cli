---
name: gravity
description: Use when working on Gravity documentation from a repository - the gravity CLI, a .gravity.yaml manifest, docs structure (sites, spaces, collections, pages), verbatim imports, AI doc passes (guides, changelog, reference), dry runs, change requests and CI wiring. Triggers on "gravity", "gravitydocs", ".gravity.yaml", "docs pipeline", "set up docs for this repo".
---

# Gravity

Gravity keeps documentation in step with code. The repository declares facts in `.gravity.yaml`
(product, docs `structure:`, passes); the `gravity` CLI applies the structure, runs the passes and sends
the results to Gravity as a change request a human reviews in the app.

Drive everything through the `gravity` CLI. Use the Gravity MCP server only to read (pages, trees,
runs) and for small targeted fixes the user asks for.

## Hard rules

1. **Never build structure page-by-page through MCP.** Declare it in `structure:` and run
   `gravity structure apply`. No loops of `pages_create` / `collections_create`.
2. **Never create stub pages where a verbatim pass will import.** A structure page with `source:` is
   created by the import itself; creating it first causes `slug_taken` at run time.
3. **Never claim a run finished without `gravity runs show <id> --json`.** Read `.data.run.status`
   (`succeeded`, `partial`, `failed`, `cancelled`, `abandoned`) and the bundle link. Silence, a closed
   terminal or a CI job that "looks done" is not evidence.
4. **Runs need an up-to-date, committed branch.** The CLI refuses when the branch is behind or diverged
   from its upstream (`branch_behind`, `branch_diverged`) or tracked files are dirty
   (`worktree_dirty`). Run the git commands it prints, with the user's consent. Never force, never
   `--allow-dirty` on a real run (it only exists for dry runs).
5. **Use `--json` for anything you parse.** Check `.ok` and `.error.code`; human text goes to stderr
   and can change.
6. **Ask before spending.** AI passes cost money. Show the plan view / estimate and get a yes before
   `gravity run` or `gravity run --dry-run` on AI passes; only then pass `--yes`.
7. **Ask before acting on other people's work.** Never `gravity runs cancel <id>` a run you did not
   start without the user's explicit consent. Never `gravity approve --all` without listing the targets.
8. **Never write tokens into files** (`.gravity.yaml`, `.env`, CI files, commits). Tokens live in
   `gravity login` profiles or CI secrets set by `gravity ci setup`.
9. **First AI runs are local.** CI skips an AI pass with no watermark (`first_run_manual`). Do the first
   run with the user: `gravity run --dry-run`, review, then send.

## Workflow

Follow these steps in order. Each step names what to ask the user.

### 1. Sign in

```bash
gravity whoami --json | jq '{ok, org: .data.organization.slug, user: .data.principal.user.email}'
gravity login            # device flow in the browser; --org <slug> to pick an organization
gravity org list         # several organizations? confirm which one with the user, then:
gravity org use <slug>
```

### 2. Setup, with the user

Before answering setup prompts or passing `--yes`, ask the user and write down the answers:

- **Audiences**: who reads these docs? Builtins `public`, `users`, `developers`, or organization
  audiences. Private material (runbooks, internal notes) goes in private spaces.
- **Sites and spaces**: which site (existing or new) and which spaces (API reference, guides,
  changelog, handbook...)?
- **Verbatim vs AI-written**: which Markdown in the repo is the source of truth and must be imported
  as-is and locked (verbatim: READMEs, `docs/`), and what should the AI write and maintain
  (guides, changelog, reference prose)?

Then run setup. Interactive on a terminal; otherwise preview first, then write:

```bash
gravity setup --json | jq '.data | {product, site, passes: [.passes[].name], manifest: .manifest.action}'
gravity setup --yes --product <slug> --site <slug>      # writes .gravity.yaml after the user agrees
```

Setup is resumable: re-running keeps what exists and fills gaps. It ends with validate + show and
offers a dry run and CI setup; decline those here and do them in steps 6-8.

### 3. Structure: plan, then refine with the user

```bash
gravity structure plan                       # draft YAML + tree diff (+ create, = exists, ! extra)
gravity structure plan --repo ../other-repo  # include sibling repositories of the same product
gravity structure plan --write               # merge the draft into .gravity.yaml
```

Walk the user through the tree. Edit `structure:` in `.gravity.yaml` directly: rename titles, move
collections, add descriptions, mark imported pages with `source: path/to/file.md`, drop what they do not
want. Spec: [references/structure.md](references/structure.md).

```bash
gravity structure apply --dry-run --json | jq '.data | {created, conflicts, deferred}'
gravity structure apply                      # after the user agrees; never deletes, reports extra
```

`deferred` items are pages with `source:`: the verbatim import creates them. That is expected.

### 4. Validate until clean

```bash
gravity validate --json | jq '.data.issues[] | select(.severity=="error") | {code, pass, path, message, hint}'
```

Each issue has `severity` (`error` blocks runs, `warning` informs), `code`, `pass`, `path` (manifest
path), `message` and `hint` (the fix). Fix, re-run, repeat until `.data.errors == 0`. Validate also asks
the server (slug collisions with existing pages, target approval, languages), so a clean validate means
the real run will not hit those conflicts. If `.data.server == "unavailable"`, say so: only local rules
ran. Codes and fixes: [references/troubleshooting.md](references/troubleshooting.md).

### 5. Show, and walk the user through it

```bash
gravity show
gravity show --json | jq '.data.verbatim[].pages[] | select(.flags | length > 0) | {source, slug, flags}'
```

Cover: the structure tree; the verbatim mapping (source file -> collection / slug / title; explain any
`empty`, `duplicate_slug`, `package_title`, `existing_page`, `not_in_structure` flag); the passes
(kind, target, triggers, branches, audiences, AI or not, estimate); effective languages per pass; CI.

### 6. Dry run, review, send

A dry run is the whole real run (LLM calls, conversion) with nothing written to Gravity. It records
the changes in `.gravity/runs/<runId>.json` and renders them.

```bash
gravity run --dry-run                       # on a TTY: plan view, confirm, result, "Send this run?"
gravity run --dry-run --yes --json > /tmp/gravity-dry.json   # non-interactive, after the user agreed to the cost
jq '.data | {recording, costUsd, pages: [.pages[] | {op, slug, title}], issues}' /tmp/gravity-dry.json
```

Review the pages with the user. Then either:

- send it as is, without recomputing: `gravity run --send <runId>` (refused with `send_mismatch` if HEAD
  or `.gravity.yaml` changed since; dry-run again then), or
- adjust `.gravity.yaml` (instructions, files, structure), commit, and dry-run again.

A real run (`gravity run`) works from any branch with the local `.gravity.yaml` and creates a normal
change request. Run `gravity --json review latest` for the link; reviewing happens in the app.

### 7. CI

```bash
gravity ci setup            # writes the CI file, mints a repository token, installs GRAVITY_REPO_TOKEN (gh/glab) or prints it once
gravity ci check --json     # exit 1 when the file is missing/outdated or the secret is absent
```

Commit the CI file and `.gravity.yaml` on a branch; the user merges.

### 8. Later

```bash
gravity run --pass <name>          # one pass, whatever its triggers
gravity runs                       # recent runs; --watch to follow
gravity runs show <runId> --json   # the only source of truth for "did it finish"
gravity review latest              # open the change request
gravity approve                    # list pending targets; gravity approve <pass> after the user agrees
gravity status                     # health, passes, open bundles
```

## Pass kinds (pick per content)

| kind | writes | AI | fits |
| --- | --- | --- | --- |
| verbatim | repository Markdown as locked pages | no | READMEs, `docs/`, ADRs: the repo is the source of truth |
| reference | API reference from OpenAPI | only with `prose: true` | any OpenAPI/Swagger document |
| guides | task-oriented guides, kept current | yes | how-tos derived from code changes |
| changelog | release notes from commits/tags | yes | customer or internal changelog |
| nucleus | facts into the organization memory | yes | internal knowledge, no pages |
| check | PR gate: doc drift and claim checks | yes, unless `claims: false` | pull requests |
| capture | UI screenshots via the Doc Agent | yes | apps with UI routes |

Triggers: `push`, `pr`, `release`, `schedule`, `manual`; `branches` filters push/schedule (default:
the default branch). `gravity run` outside CI is a manual run; `--pass <name>` runs that pass whatever its
triggers. Details: [references/passes.md](references/passes.md).

## Languages (explain when languages come up)

Three layers decide what gets translated:

1. **Site languages** (app setting): which languages a site serves.
2. **Organization translations setting** (app): the gate. Off = nothing is machine-translated, whatever
   passes request (`translations_disabled` warning).
3. **Pass `options.languages`**: which languages a pass requests for the pages it writes.

Verbatim files `guide.fr.md` or front matter `lang: fr` import as translations directly (the language
must be enabled on the site, else `language_not_enabled`). Read the outcome per pass:

```bash
gravity validate --json | jq '.data.i18n[] | {pass, languages, effective, reason}'
```

More: [references/i18n.md](references/i18n.md).

## Lease waits and stuck-looking runs

Only one run per branch at a time. When another holds the lease the CLI prints
`waiting for run <id> (<trigger> on <branch>, started <ago>)` with a countdown. Tell the user who holds
it (`gravity runs show <id>`). Wait, raise `--lease-timeout`, or - only with the user's consent -
`gravity runs cancel <id>`. A run that seems stuck: `gravity runs show <id>` shows per-pass status and
progress; Ctrl-C on a local run finishes it as cancelled and releases the lease.

## Exit codes

`0` ok, `1` findings or validation errors, `2` operational error, `3` license refusal (ask an
admin), `4` missing or rejected credentials (`gravity login`), `130` interrupted.

## References

- [references/manifest.md](references/manifest.md): `.gravity.yaml` keys, including `structure:` and `allowEmpty`
- [references/structure.md](references/structure.md): structure spec, plan/apply, `deferred_to_import`
- [references/passes.md](references/passes.md): pass kinds, options, triggers, first runs
- [references/i18n.md](references/i18n.md): the three language layers
- [references/troubleshooting.md](references/troubleshooting.md): validate codes and fixes, leases, approvals, tokens, install
