# gravity-cli

`gravity` keeps a product's documentation true to its code. A repository
declares its documentation in `.gravity.yaml`: the **structure** of the docs
(site, spaces, collections, pages) and the **passes** that keep them current
(verbatim imports of the repository's Markdown, an API reference from OpenAPI,
AI-written guides and changelogs). `gravity` applies the structure, runs the
passes and sends what they produce to Gravity as one **change request** that a
human reviews in the app.

Two kinds of run, nothing in between:

- **Dry run** (`gravity run --dry-run`): the whole run, model calls and Markdown
  conversion included, with nothing written to Gravity. The result is shown in
  the terminal and recorded; `gravity run --send <runId>` sends exactly that
  result without recomputing it.
- **Real run** (`gravity run`): from any branch, with the local `.gravity.yaml`,
  into Gravity exactly like a CI run. Branch filters still apply: a pass
  without `branches:` runs only on the default branch.

## Install

`gravity` is a single static binary.

```bash
curl -fsSL https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/v1/install.sh | sh
```

The script installs the newest 1.x release for your OS and architecture that
has an archive for it, verifies its checksum against the release's
`checksums.txt` (with a warning when that file or a sha256 tool is missing; a
mismatch stops the install), and puts it in `/usr/local/bin` when writable,
else `~/.local/bin`. `GRAVITY_VERSION` takes a major (`1`, the default), a
release (`v1.2.3`) or `latest`; `GRAVITY_INSTALL_DIR` picks the directory.
Release candidates (`v1.2.0-rc.1`) are GitHub pre-releases: a major,
`latest`, Homebrew and Scoop never install one, only its exact version does
(`GRAVITY_VERSION=v1.2.0-rc.1`).

```bash
brew install Grupo-Impulso-Digital/tap/gravity                     # macOS (a Homebrew cask)
scoop bucket add impulso https://github.com/Grupo-Impulso-Digital/scoop-bucket
scoop install gravity                                               # Windows
irm https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/v1/install.ps1 | iex
go install github.com/Grupo-Impulso-Digital/gravity-cli/cmd/gravity@latest
```

Releases ship binaries for Linux, macOS and Windows on amd64 and arm64. The
tap publishes a Homebrew cask, not a formula, and its post-install hook strips
the macOS quarantine attribute; treat Homebrew as macOS only and use
`install.sh` or a release archive on Linux.

## Quick start

```bash
gravity login                 # browser sign-in; one profile per organization
gravity setup                 # detect, choose product and site, draft the structure, pick passes, write .gravity.yaml
gravity structure apply       # create the spaces and collections the structure declares
gravity run --dry-run         # everything a run would write, nothing written; then send it, or adjust
gravity ci setup              # the CI file and the GRAVITY_REPO_TOKEN secret
```

`gravity setup` is the front door. It signs you in if needed, detects the
repository (languages, monorepo packages, READMEs and docs folders, OpenAPI
specs, changelog, CI), asks for the product and the site, drafts the docs
structure, suggests passes, writes `.gravity.yaml`, validates it and shows the
result, then offers to apply the structure, to dry-run and to wire CI.
Re-running it is safe: what the manifest declares is kept and only gaps are
filled. Without a terminal, `--yes` accepts the suggestions; `--json` alone
prints the proposal without writing anything.

Working with an AI coding agent? `gravity agent install` writes the Gravity
skill for Claude Code (`--tool cursor|codex|agents-md` for the others), or add
the plugin marketplace in Claude Code:
`/plugin marketplace add Grupo-Impulso-Digital/gravity-cli`, then
`/plugin install gravity@gravity`. The skill walks the agent through the same
flow with you and keeps it away from the known traps (building structure page
by page over MCP, stub pages where an import will land, claiming a run is done
without `gravity runs show`).

## See what the manifest declares

`gravity show` is a static view of `.gravity.yaml`, plus light server lookups
when you are signed in:

```
# Structure  = exists  + created by structure apply  v created by the import
  Polaris  site polaris
  |-- = Documentation  docs - product-docs - public
  |   |-- = Polaris  overview  <- README.md
  |   `-- + Polaris Admin/  admin
  |       |-- v Polaris Admin  admin-overview  <- packages/admin/README.md
  |       `-- + FAQ  faq
  `-- + Guides  guides - product-docs

# Verbatim - docs  polaris/docs - 3 pages
  SOURCE                        COLLECTION  SLUG            TITLE          FLAGS
  README.md                 ->  -           overview        Polaris        page exists
  packages/admin/README.md  ->  admin       admin-overview  Polaris Admin  package-name title
  packages/api/README.md    ->  api         api-overview    Polaris Api    empty, package-name title, not in structure

# Passes  2 passes
  PASS         KIND      TARGET                       RUNS ON  AUDIENCES  LANGUAGES    AI   ESTIMATE
  docs         verbatim  polaris/docs                 push     -          -            no   free - first run
  user-guides  guides    polaris/guides (unapproved)  push     users      fr,es -> fr  yes  ~$0.84 - 12 commits
  ! user-guides needs a grant for polaris/guides: gravity approve polaris/guides
```

On a terminal the same view uses box-drawing trees, bordered tables and colour.
`gravity validate` checks the manifest the way a run would: the schema, local
rules (duplicate slugs, empty files, package-name titles, structure pages
whose `source:` no verbatim pass imports, nesting deeper than five
collections) and the platform's checks (pages that already own a slug,
targets missing or awaiting approval, languages the site lacks, translations
switched off). Each issue names its pass, path and fix; errors exit `1`. It
also prints the languages each pass will really produce.

## Structure

The repository declares its docs tree under `structure:`:

```yaml
structure:
  site: { slug: polaris, name: Polaris }
  spaces:
    - slug: developers
      name: Developers
      type: api-reference          # a space type of the app catalog
      visibility: public           # public | private
      collections:
        - slug: admin
          title: Admin
          collections: [...]       # nested, at most five levels
          pages:
            - slug: installation
              title: Installation
              source: packages/admin/README.md   # owned by a verbatim pass
```

- `gravity structure plan [--repo <path>]... [--site <slug>]` drafts it
  deterministically from the repository (root README, `docs/`, package READMEs
  and docs, OpenAPI, changelog, runbooks) merged with what is declared and the
  spaces the site already has, and prints the YAML with a tree diff against
  Gravity. `--write` merges it into `.gravity.yaml`, adding a verbatim pass for
  the page sources when none maps them.
- `gravity structure apply [--dry-run]` creates what is missing. It is
  idempotent by slug path, never deletes (extra items are reported), and never
  creates a page that has a `source:`: the verbatim import creates it, so no
  stub ever blocks an import.
- `gravity structure show [--site <slug>]` prints the live tree, pages included.

## Runs

Outside CI, `gravity run` first checks the branch: it fetches the upstream and
refuses, with the exact git commands to fix it, when the branch is behind or
has diverged (`branch_behind`, `branch_diverged`), or when tracked files are
uncommitted (`worktree_dirty`; `--allow-dirty` lets a dry run, and only a dry
run, go ahead on the committed state; untracked files are ignored). A branch
without an upstream, or a real run with unpushed commits, only gets a warning.
It then validates the manifest (a real run stops on errors with exit `1`; a dry
run reports the same conflicts the real run would hit) and shows the plan:

```
# Plan  dry run - manual on main @ abc1234
  PASS           KIND       TARGET                  THIS RUN                                                        AI   ESTIMATE
  developer-api  reference  Developer Portal > API  runs - 2 commits                                                no   free - first run
  user-guides    guides     Developer Portal > API  skips: target awaits approval (gravity approve dev-portal/api)  yes  -
  * 1 of 2 passes run
```

AI passes run on the model configured for their scope in Gravity's AI
settings, or on `--model <id>`. Under the pass table, the plan names each AI
pass that runs with its model, the model's quality label (`recommended`,
`supported`, `not_recommended`, `untested`) and the output language:

```
  * user-guides model: Claude Sonnet 4.6 - recommended - French (fr)
```

A model rated `not_recommended` adds a warning (`--yes` still goes ahead), a
fallback model says which one it replaces, and a model that cannot run (not
enabled, no provider key, model catalog unreachable) stops the run before you
are asked, with `model_unavailable` and exit `2`. Once the run has started,
each AI pass prints its model and the version of the instructions Gravity
serves for it:

```
user-guides: Claude Sonnet 4.6 - recommended - French (fr) - instructions 1a2b3c4d
```

`--debug` keeps those instructions bundles and every page draft under
`.gravity/runs/<runId>/`.

On a terminal it asks before passes that call a model, unless `--yes`. When
another run holds the branch, it says at once which one and waits with a
countdown (`--lease-timeout`, default 20m):

```
! waiting for run prun_9 (push on main, started 14m ago), which holds main - timeout 20m0s
  hint if it is stuck: gravity runs cancel prun_9
```

Each pass shows live progress; Ctrl-C finishes the run as cancelled, releases
the lease and exits `130`. A dry run ends with the result report:

```
# Result  nothing was written to Gravity
  ok developer-api  reference -> Developer Portal > API  1 new
      + new    refunds  Refunds  API reference for 1 operations from api/openapi.yaml
  ! user-guides  guides -> Developer Portal > API  skipped: target awaits approval

Dry run finished
  1 pass ran, 1 skipped, 0 failed - 1 change - $0.25
  Recorded in .gravity/runs/prun_1.json
  Send it:  gravity run --send prun_1
```

A dry run is recorded only when it has changes. On a terminal it then asks
"Send this run to Gravity?". `gravity run --send <runId|latest>` replays the
recorded changes as a real run without recomputing (no model is called); it
refuses (`send_mismatch`) when HEAD or `.gravity.yaml` changed since the dry
run, and deletes the recording once sent. Each recorded AI pass keeps the model, provider and
instructions version it ran with and its turns and tokens; sending forwards
them, and `--model` does not apply to `--send`. A real run prints the change
request link and offers to open it; `gravity review [runId|latest]` opens it
later, `gravity runs [--limit N] [--watch]` lists recent runs, `gravity runs
show <id>` is the authoritative state of one run (per-pass status, progress,
lease, change request) and `gravity runs cancel <id>` cancels one. A run has
finished only when `runs show` says `succeeded`, `partial`, `failed` or
`cancelled`; `abandoned` is a status the CLI shows like a failure, not a
finish.

The repository token CI uses may only write where a person allowed it, since
anyone who can push could otherwise point `.gravity.yaml` at any space. The
allowance is a grant per site/space: one grant covers every pass of the
repository that targets that space or a collection in it. A grant is needed
only when a pass would bypass review or expose content: verbatim imports and
auto-accepting passes (they go live without review), nucleus passes (they
write to the product memory) and any target space that is not public (the
token could read it). Passes that land as change requests in a public space
need none. `gravity setup`, `gravity run` and other actions you take with your
own login grant automatically where you have write access, and setup asks one
yes/no per remaining grant; otherwise the pass is skipped (`target awaits
approval`) until `gravity approve <site/space>` (or `--all`) grants it.

Locally, and on a GitHub `workflow_dispatch` (or any manual pipeline), the
trigger is `manual`: passes triggered on `manual`, `push` or `schedule` run,
and `gravity run --pass <name>` runs one pass whatever its triggers (branch
rules still apply). A run reads committed history only: every write cites the
commit it comes from. In CI the first run of an AI pass is skipped
(`first_run_manual`) unless the repository allows it: do it locally with a dry
run, then send it.

## Passes

A pass is one job a repository does for its documentation: a target (site,
space, optionally a collection), a kind, triggers, audiences and instructions.

| Kind | Writes | AI | Typical target |
| ---- | ------ | -- | -------------- |
| `verbatim` | Markdown/MDX files imported as-is and locked to the repository | no | Developers › Admin |
| `reference` | API reference pages from OpenAPI documents (deterministic api blocks, optional AI prose) | only with `prose: true` | Developer portal › API |
| `guides` | Guides kept true to the code: an impact analysis, a page plan, then pages drafted block by block, checked, and cited to commits | yes | Product docs › Guides |
| `changelog` | Release pages and an Unreleased page, from commits or `CHANGELOG.md` | yes | Product › Changelog |
| `nucleus` | Facts about the product in Nucleus, the shared memory, tagged with this repository | yes | product namespace |
| `check` | Nothing: drift, coverage and claim review for pull requests | unless `claims: false` | — |
| `capture` | Screenshots-and-steps pages through the Gravity Doc Agent | yes | Product › Guides |

Passes are declared in `.gravity.yaml` and appear in the app as managed in the
repository; the app holds the instructions of the organization, site, space
and collection layers and composes them with each pass's own. Languages come
from three layers: the languages a site serves, the organization's
translations setting (off means nothing is machine-translated) and a pass's
`options.languages`; `gravity show` and `gravity validate` print what each pass
will really produce, and why.

### How AI passes work

Gravity owns every instruction. For each AI pass of a run, the CLI fetches an
instructions bundle from Gravity: the system prompt of each phase (rendered
from the organization, site, space, collection, pass and run layers, the
glossary and the output language, which is the site's default language), the
tool descriptions, the model and its real limits, and the loop's caps. The
CLI runs the conversation and the tools; a model never has to fit a page or a
list in one reply:

- A page is written in a draft, a few blocks per call (`draft_append`,
  `draft_replace`, `draft_delete`), then checked: wrong output language, code
  or JSON in prose, Markdown headings in text, planning voice ("this section
  should…"), output markers such as `TODO:` and uncovered `mustCover` items
  are errors the model must fix before `submit_page` succeeds. Only the
  difference between the draft and the page is sent, as one change. Blocks a
  person owns, API reference blocks and verbatim imports are locked.
- Plans, release notes, Nucleus facts, claim reviews and unit maps are built
  over many calls (`add_*`, then `finish_*`).
- The loop sizes each reply from the model's output limit and context window,
  elides old tool results when the context fills, and stops a page that hits a
  cap, a refusal or the output limit three times in a row; that page sends
  nothing and the pass fails with the list of failed pages.
- There is no CLI token budget: the repository's run and month budgets in
  Gravity apply. `budget_exceeded`, `no_provider_key` and `ai_disabled` halt
  the whole run, which is finished as failed; `budget_exceeded` exits `5` and
  says which cap was reached, `ai_disabled` exits `3` (see Exit codes). A `429`
  `concurrency_limited` is retried after its `Retry-After`.

Every pass report records which model, provider and instructions version it
ran with.

### What the AI reads

A model starts each phase from a **kickoff**: tagged data, no instructions (the
instructions are the bundle's). For a guides pass the plan kickoff holds:

- **The change summary** of the pass's commit range: the range and, for a
  release, its tag and the previous one; the commits (sha, subject, pull
  request number, author and the first four lines of the body, at most 60);
  the changed files with status and `+added -deleted` counts (at most 200);
  the product **units** the range touched, added or removed; the **OpenAPI
  delta** per document (added, removed and changed operations, breaking or
  not); and removed or renamed symbols, best effort. Lockfiles, `node_modules`,
  `vendor`, `dist`, minified files and `code.exclude` never take part.
- **Candidate pages** in the target, each with why it is a candidate, plus
  pages reachable through units this repository contributes to, units
  with no page yet, and open hints from other repositories.
- The pass header: repository, pass, target, audiences and product.

The kickoff carries no diffs and no file contents. The model asks for them with
read-only tools, which read the repository at the end of the range under review,
never the working tree:

| Tools | What they do |
| ----- | ------------ |
| `git_log`, `git_diff`, `git_show`, `list_files`, `read_file`, `grep` | Read the repository: history, diffs, a commit, the file list, a file (in line ranges, at another `ref` if asked) and a search. |
| `read_page`, `search_docs`, `recall`, `inventory`, `list_target` | Read Gravity: a page block by block with its owner and last writer, a search of the docs, Nucleus memory, the product's units and the target's page tree. |
| `draft_view`, `draft_append`, `draft_replace`, `draft_delete`, `check_page`, `raise_hint`, `submit_page` | Write one page in a draft and submit it. |
| `add_*` and `finish_*` | Build a plan, release notes, Nucleus facts, claim findings or a unit map over several calls. |

Two principles hold across passes:

- **Commit messages are hints; impact comes from code.** Whether a pass runs
  is decided from the files and units a range changed (`plan.Decide`), and
  which pages a change affects from the units it touched and the pages bound to
  them (`passes.ComputeImpact`). A commit subject is only the last resort for
  finding pages: when no unit produced a candidate page or an undocumented
  unit, the first three subjects are searched in the target. Changelog passes are the exception: they write
  from the commits (or from `CHANGELOG.md`).
- **`gravity run` reads committed history only.** Every write cites a commit,
  so uncommitted work is never part of a run. Outside CI a real run refuses
  uncommitted tracked files (`worktree_dirty`), and so does a dry run unless
  you pass `--allow-dirty`, which then ignores them.

## `.gravity.yaml`

The smallest valid manifest is one line:

```yaml
version: 2
```

Identity comes from the git remote. Everything else is optional: `product`,
`apiUrl`, `appPasses` (`allow` | `ignore`), `code` (`openapi`, `entrypoints`,
`include`, `exclude`, `units`), `docs`, `passes` and `structure`. Every pass has
a `name` and a `kind`; passes that write pages need a `target`
(`<site>/<space>[/<collection>...]`). The manifest is validated against an
embedded JSON Schema: an unknown key fails with a suggestion
(`passes[0].trigers: unknown key (did you mean "triggers"?)`), a `token:`
anywhere is refused, and paths must stay inside the repository.

`code.include` says what counts as code: a pass without `scope.paths` reacts
only to changes under it (plus `code.openapi`), and removed or renamed symbols
are read only there. `code.exclude` drops paths from every change set.

A real run uses the local manifest from any branch; only the authoritative
branch (the default branch unless the app says otherwise) stores it in Gravity,
and only runs on that branch move the passes' watermarks.

### One repository, several sites

A repository can feed different sites with different content. Here the same
service keeps an API reference and integration guides on the developer portal,
and task guides on the customer help center:

```yaml
version: 2
product: acme-platform
code:
  openapi: [api/openapi.yaml]
passes:
  - name: developer-api
    kind: reference
    template: api-reference
    target: dev-portal/api
    triggers: [push, pr]
    audiences: [developers]
  - name: integration-guides
    kind: guides
    template: developer-guide
    target: dev-portal/guides
    triggers: [push, pr]
    scope:
      paths: ["api/**", "src/server/**"]
    instructions: |
      Developer instructions: authentication, endpoints, webhooks and error
      codes, with request examples. Never describe the UI.
  - name: help-center
    kind: guides
    template: user-guide
    target: help-center/guides
    triggers: [push]
    scope:
      paths: ["src/ui/**"]
    instructions: |
      What customers see and do in the app, one task per page, in plain
      language. Never mention endpoints or code.
```

Each pass has its own range, watermark and scope: a change under `src/ui/**`
runs `help-center` and skips the others at no AI cost.

## Verbatim documents

Some documents must be published exactly as written and edited only in the
repository: specifications, compliance text, SDK READMEs, handbooks.

```yaml
passes:
  - name: handbook
    kind: verbatim
    target: internal/handbook
    triggers: [push]
    options:
      files:
        - include: "docs/handbook/**/*.md"
          stripPrefix: docs/handbook
```

A leading `# Heading` equal to the page title is dropped, wherever the title
comes from (front matter, `files[].title` or the heading itself). Package-name
headings such as `# @acme/polaris-admin` become the title `Polaris Admin`, and
slugs never come from package names. A file with nothing but a title is
skipped with a warning (`empty_source`) unless `options.allowEmpty: true`.

Markdown and MDX become native blocks with high fidelity: front matter (title,
slug, order, description), headings with their anchors, tables, code with its
language, admonitions and GitHub alerts, mermaid, task lists, footnotes,
details. Relative images are uploaded, links between files become links between
pages, and folders become collections. Imported pages are **locked**: read-only
in the editor with a banner and a link to the file, human edit proposals are
refused, and AI passes may read and cite them but never change them. A file
deleted in the repository becomes a deletion proposal. `gravity check` flags a
code change that contradicts a locked page, as a finding against the
repository.

Translations live next to their source: `rotation.fr.md` (or `.pt-BR.mdx`)
beside `rotation.md`, or a file whose front matter says `lang: fr` and whose
slug matches a source page. Each one is uploaded after every source page of the
run as that page's French version, never as a page of its own, and is locked
with it. A language the site has not enabled is skipped with a warning.
Deleting the file removes that language version. `gravity show` lists
translations with their language and `gravity explain` names the translated
files of a locked page.

## Several repositories, one product

Products span repositories, and ownership moves: services split, features
migrate, a gateway declares an endpoint that a service behind it implements.
Gravity does not fence pages per repository.

- **Units are product-wide.** Every API operation (`api:post:/v1/refunds`) and
  every feature a repository maps is a unit of the product, with contributing
  repositories and their roles: `declares` (routes or re-exports it),
  `implements` (owns its behaviour), `documents`. Set the repository's role with
  `code.units.role` (`[declares, implements]` for a gateway that also
  implements).
- **Any pass whose change touches a unit may update the blocks bound to it**,
  whichever repository wrote them last, including pages outside its own target
  when this repository declares or implements the unit (updates of bound blocks
  only, never new pages). Human-written blocks are never overwritten.
- **Provenance is a history.** Every block records who wrote it (repository,
  pass, commit, run) and why; `gravity explain <page>` shows the last writer and
  the earlier ones per block, plus the page's repository lock.
- **Claims are checked across repositories.** A statement this repository
  cannot prove is not drift when another repository implements or declares the
  unit, wrote the block, or recorded the fact in Nucleus (`true elsewhere`). A
  claim nobody can prove is a soft note. Only a claim this repository's code
  contradicts is a finding, and when the behaviour belongs to another
  repository the CLI raises a hint for that repository instead.
- **Handoffs are detected.** When a unit's source moves from one repository to
  another, the inventory records the handoff and the new owner's next run
  reviews the unit's pages. Pull requests already say "`api:post:/v1/refunds`
  implements moves from gateway to billing-api once merged".
- **Competing changes surface in review.** When two runs change the same blocks
  at once, both versions are shown side by side, and the run summary says so.

## Commands

| Command | Purpose |
| ------- | ------- |
| `gravity login` | Device flow sign-in; `--org`, `--no-browser`. Headless: `--with-token` (reads stdin) or `--token <token>` verifies a token and stores it. The profile is named `--profile`, else the organization slug. |
| `gravity logout` | Revoke the current user token and remove its profile; `--all`. |
| `gravity whoami` | Principal, organization, token kind, scopes, expiry, API URL, profile. |
| `gravity org [list\|use <slug>]` | Your organizations and their profiles; switch the current one. |
| `gravity setup` | The guided front door (see Quick start); `-y/--yes`, `--product`, `--site`, `--no-structure`, `--no-run`, `--no-ci`. Never commits or pushes. |
| `gravity show` | The manifest as a tree, a mapping table and a pass table, with flags, effective languages, estimates, the model and output language of each AI pass, and CI; `--pass`, `--model`. |
| `gravity validate` | Schema, local rules and the platform's checks, grouped with fixes; exits `1` on errors. |
| `gravity structure plan\|apply\|show` | Draft the structure (`--repo`, repeatable, `--site`, `--write`), create what is missing (`--dry-run`), print the live tree (`--site`). |
| `gravity run` | Dry run (`--dry-run`) or real run; `--pass`, `-y/--yes`, `--send <runId\|latest>`, `--allow-dirty` (dry runs only), `--note`, `--lease-timeout` (default `20m`), `--model`, `--debug`; for CI and scripted runs also `--trigger`, `--branch`, `--from`, `--to`, `--parallel` (max 4), `--no-comment`, `--annotate`, `--strict`. |
| `gravity review [runId\|latest]` | Open a run's change request (prints the link without a terminal or with `--json`); defaults to `latest`. |
| `gravity runs` | Recent runs (`--limit`, default 10; `--watch` until none is running); `runs show <id>` per-pass state, `runs cancel <id>`. |
| `gravity approve [site/space]... [--all]` | List the spaces waiting for a grant and why, grant them (user token); a pass name grants that pass's space. |
| `gravity ci setup\|check` | Write the CI file, mint and install `GRAVITY_REPO_TOKEN` (`--provider github\|gitlab\|bitbucket\|azure\|jenkins\|circleci\|none`, `--no-secret`); check the file and the secret (exit `1` when incomplete). |
| `gravity status` | Auth, connection, manifest, passes with targets and watermarks, recent runs, open bundles, tokens, health and capability warnings; `--runs N` (default 5), `--check`. |
| `gravity check` | The pull request gate: check passes (or the built-in check: drift, coverage of new operations, verbatim notes) plus every pass's doc impact; `--pass`, `--from`, `--to`, `--fail-on`, `--annotate`, `--comment`. |
| `gravity explain <page>` | Per block: last writer (repository, pass, commit, run), history, ownership and the page lock; `<page>` is a page id, `<site>/<space>/<page>` or a viewer URL; `--block <key>`. |
| `gravity agent install` | Write the Gravity skill for `--tool claude\|cursor\|codex\|agents-md` (default `claude`); `--global` (claude, codex). |
| `gravity version` | Version, commit, build date, Go version, platform. |

Every command takes `--json` and then prints one envelope on stdout
(`{ ok, command, version, data, warnings, error }`) for scripts and agents.

### Servers older than 1.1

The CLI degrades instead of failing when the platform lacks a 1.1 endpoint:
`validate` (and the validation before runs) falls back to local checks with a
`server_validate_unsupported` warning; `structure show` and `structure plan`
read the site tree without pages; `approve` lists the targets with their
approval links in the app; `runs` reads recent runs from `status`; a real run
whose local manifest snapshot is refused runs with the passes stored from the
authoritative branch, with a warning. `structure apply` and `runs cancel` say
the server cannot do it yet and exit `2`.

### Servers older than 1.2

gravity 1.2 takes every AI instruction from the server's instructions bundle
(`GET /api/llm/v1/instructions`). Against a server without it, a run whose
passes call a model fails before any pass starts with "this Gravity server is
too old for gravity 1.2 (no instructions bundle); upgrade the server or use
gravity 1.1" (`server_too_old`). Runs without AI passes (verbatim imports,
the API reference without prose) work as before.

### `gravity check`

`gravity check` runs the repository's check passes, or a built-in check when
none applies:

- **Drift.** API reference blocks are compared with the pull request's base
  and head. An operation the pull request changes or removes is a finding
  only when no enabled reference pass with the `push` trigger will update its
  page on merge; when one will, the report carries a note instead. A block
  that was already out of date before the pull request is a warning.
- **Coverage.** New operations that no pass will document are reported
  (warnings, or failures with `--fail-on coverage`), and the share of the units
  this repository implements that have a page is reported as a note.
- **Verbatim.** Files the merge will re-import are listed as notes.

Claim review (does the documentation still say true things about the code)
needs a declared `check` pass, which also takes `failOn`, `coverageMin`,
`require`, `claims` (`false` turns claim review, and its model calls, off) and
`annotate` (`false` keeps its findings out of the CI annotations). Locked
verbatim pages whose file the pull request changes are reviewed first, as the
file will be imported.
`--fail-on` defaults to `drift, claims, verbatim`.

### Global flags

`--profile` (`GRAVITY_PROFILE`), `--api-url` (`GRAVITY_API_URL`), `--token`
(`GRAVITY_REPO_TOKEN`, `GRAVITY_TOKEN`), `--manifest` (`GRAVITY_MANIFEST`,
default `.gravity.yaml`), `-C/--dir <dir>`, `--json`, `--no-color`
(`NO_COLOR`), `-q/--quiet`, `-v/--verbose`.

- Credentials: `--token` > `GRAVITY_REPO_TOKEN` > `GRAVITY_TOKEN` > profile;
  values are trimmed, and `whoami` and `status` name the source actually used.
  A variable holding an unexpanded reference (`$(NAME)`, `${{ … }}`, `$NAME`,
  `%NAME%`) is skipped. When every token variable is such a reference, a CI job
  fails with `token_unresolved` (a missing secret must not pass), while a local
  run falls back to the profile with a warning. Tokens are never read from
  `.gravity.yaml`.
- API URL: `--api-url` > `GRAVITY_API_URL` > manifest `apiUrl` > profile
  `apiUrl` > `https://api.gravitydocs.io`. A profile token is only sent to the
  host that issued it (`token_host_mismatch` otherwise). API URLs must use
  https; plain http is accepted for localhost only, and `gravitydocs.io`
  (the marketing site) is refused.
- `--json` prints exactly one JSON document on stdout
  (`{ ok, command, version, data, warnings, error }`); progress goes to stderr.
- With `CI` set to `true`, `1` or `yes`, or without a terminal, output is
  plain ASCII and nothing prompts. On a terminal, prompts and live progress use
  the Charm libraries; a non-empty `ACCESSIBLE` or `TERM=dumb` switches prompts
  to a line mode.
- CI detection is overridable with `GRAVITY_TRIGGER` (`pr`, `push`, `release`,
  `schedule`, `manual`), `GRAVITY_BRANCH`, `GRAVITY_HEAD_SHA`,
  `GRAVITY_BASE_SHA`, `GRAVITY_PR`, `GRAVITY_BASE_BRANCH` (the pull request's
  target), `GRAVITY_TAG`, `GRAVITY_RUN_URL`. The default branch comes from
  `origin/HEAD`, else `GRAVITY_DEFAULT_BRANCH`, `CI_DEFAULT_BRANCH` (GitLab) or
  the GitHub event's `repository.default_branch`; the CI file `gravity ci
  setup` writes falls back to the checked-out branch, then `main`.

Profiles live in `~/.config/gravity/profiles.yaml` (mode `0600`, honors
`XDG_CONFIG_HOME`), one per organization; `gravity org use <slug>` switches
between them.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| `0` | Success, or nothing to do (no change in scope; a fork or Dependabot pull request without a token). |
| `1` | Findings: `validate` errors (also before a local real run, and in a local dry run that sending would fail on), `check` findings in `failOn`, structure conflicts, refused approvals, `ci check` on an incomplete setup, `status --check` on an unhealthy repository. |
| `2` | Operational error: network, bad input, invalid manifest, missing target, failed pass, unknown or inapplicable `--pass`, lease timeout, a branch behind or diverged from its upstream, uncommitted tracked files, a recording that no longer matches HEAD. |
| `3` | Licence or organization-policy refusal (`module_disabled`, `seat_limit`, `ai_disabled`): ask a workspace administrator. With `ai_disabled`, AI is turned off for the organization and an admin can turn it on in Gravity › AI usage; the run halts and is finished as failed. |
| `4` | No usable credentials: no token (`token_missing`), only CI variables that were never expanded such as a literal `$(GRAVITY_REPO_TOKEN)`, in CI or locally without a profile token (`token_unresolved`), or a token the server rejects (`401`, also in the middle of a run). |
| `5` | An AI spend cap is used up (`budget_exceeded`): this run's, the month's or a feature's. The run halts and is finished as failed; ask an organization admin to raise the cap in Gravity › AI usage. |
| `130` | Interrupted (Ctrl-C or SIGTERM): the run was finished as cancelled and its lease released. |

Within a run, the passes' outcomes combine as `3` > `2` > `1` > `0`. Across
the error chain, a licence refusal wins, then a `401` (`4`), then a spend cap
(`5`), then the command's own code. A spend cap halts the run before its
outcomes combine, so it replaces the `2` the halted run used to end with, but
never hides a licence or credential refusal. A missing or unresolved token stops a
command before any request; a rejected token stops it at the first `401`
(a run ends without its finish call), and both exit `4`. Locally, with a
profile token, the error adds "run `gravity login` to sign in again". Fork
pull requests are detected per provider (GitHub event repositories, GitLab
source and target projects, `SYSTEM_PULLREQUEST_ISFORK` on Azure,
`CHANGE_FORK` on Jenkins, `CIRCLE_PR_*` on CircleCI), and GitHub runs started
by Dependabot by their actor; only those skip with a notice when the token is
missing. Bitbucket runs no pipeline for a fork
in the target repository, so a missing token there is always `4`. In CI, a
run exits `1` only for `check` findings; a failed pass or a missing target
exits `2`, and a spend cap that halts the run exits `5`. Unapproved targets,
missing scopes and missing modules only skip their pass, unless `--strict`
turns the first two into `2` and missing modules into `3`.

## Migrating from 0.x and 1.0

- 1.1 removed `gravity init` (use `gravity setup` and `gravity ci setup`),
  `gravity passes` (use `gravity show`) and `gravity preview` (use
  `gravity run --dry-run`). The 0.x commands that 1.0 replaced (`auth`,
  `doctor`, `ping`, `repos`, `spaces`, `sync`, `docs`, ...) are gone too,
  without stubs: an unknown command is a usage error and exits `2`.
- A manifest without `version: 2` is a schema error; 1.1 no longer converts v1
  manifests. Run `gravity setup` to write a fresh one.
- The v0.x `config.yaml` is ignored; sign in again with `gravity login`.
- Repository tokens live in `GRAVITY_REPO_TOKEN`. CI files written by
  gravity 1.0.0 to 1.0.2 pass only `GRAVITY_TOKEN` (or, on GitHub with 1.0.2,
  both secrets through the action's single `token` input); they keep working
  while the token is stored in `GRAVITY_TOKEN`. `gravity status` names each
  such file with the lines to change, and running `gravity ci setup` again
  rewrites a gravity CI file it generated and you did not edit. Then store the
  repository token as `GRAVITY_REPO_TOKEN` and, once no 0.x pipeline needs it,
  delete `GRAVITY_TOKEN`.
- Pipelines that must stay on 0.x pin `ci/github@v0` or `GRAVITY_VERSION=0`.

## Development

```bash
make build   # ./bin/gravity
make test    # go test ./...
make lint    # golangci-lint (pinned in go.mod) incl. the format check
make ci      # lint + test + build
make eval MODEL=<id> [MAX_OUTPUT=<n>]   # score a model on the eval fixture
```

`make eval` needs a Gravity server seeded by `testdata/eval/setup.sh`; see
[docs/model-eval.md](docs/model-eval.md).

Pull requests target `develop`, the default branch; `ci-ok` must pass.
Merging never releases: an approver runs `gh workflow run release.yml -f
bump=patch|minor|major` (add `-f prerelease=true` for a release candidate),
which tags and publishes a commit CI already passed
([docs/adr/0001-trunk-delivery.md](docs/adr/0001-trunk-delivery.md)).

Contributor standards and the architecture live in [AGENTS.md](AGENTS.md).
