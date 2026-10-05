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
  into Gravity exactly like a CI run.

## Install

`gravity` is a single static binary.

```bash
curl -fsSL https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.sh | sh
```

The script installs the newest 1.x release for your OS and architecture that
has an archive for it, verifies its checksum, and puts it in `/usr/local/bin`
(or `~/.local/bin`). `GRAVITY_VERSION` takes a major (`1`), a release
(`v1.2.3`) or `latest`; `GRAVITY_INSTALL_DIR` picks the directory.

```bash
brew install Grupo-Impulso-Digital/tap/gravity                     # macOS, Linux
scoop bucket add impulso https://github.com/Grupo-Impulso-Digital/scoop-bucket
scoop install gravity                                               # Windows
irm https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.ps1 | iex
go install github.com/Grupo-Impulso-Digital/gravity-cli/cmd/gravity@latest
```

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
has diverged, or when tracked files are uncommitted (`--allow-dirty` lets a dry
run go ahead on the committed state). It then validates the manifest (a dry
run reports the same conflicts the real run would hit) and shows the plan:

```
# Plan  dry run - manual on main @ abc1234
  PASS           KIND       TARGET                  THIS RUN                                                        AI   ESTIMATE
  developer-api  reference  Developer Portal > API  runs - 2 commits                                                no   free - first run
  user-guides    guides     Developer Portal > API  skips: target awaits approval (gravity approve dev-portal/api)  yes  -
  * 1 of 2 passes run
```

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

On a terminal it then asks "Send this run to Gravity?". `gravity run --send
<runId|latest>` replays the recorded changes as a real run without
recomputing; it refuses (`send_mismatch`) when HEAD or `.gravity.yaml` changed
since the dry run. A real run prints the change request link and offers to open
it; `gravity review [runId|latest]` opens it later, `gravity runs [--watch]`
lists recent runs, `gravity runs show <id>` is the authoritative state of one
run (per-pass status, progress, lease, change request) and
`gravity runs cancel <id>` cancels one.

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
| `guides` | Guides kept true to the code: an impact analysis, a page plan, then block-level edits that cite commits | yes | Product docs › Guides |
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
| `gravity login` | Device flow sign-in; `--org`, `--profile`, `--no-browser`, `--with-token` (stdin). |
| `gravity logout` | Revoke the current user token and remove its profile; `--all`. |
| `gravity whoami` | Principal, organization, token kind, scopes, expiry, API URL, profile. |
| `gravity org [list\|use <slug>]` | Your organizations and their profiles; switch the current one. |
| `gravity setup` | The guided front door (see Quick start); `--yes`, `--product`, `--site`, `--no-structure`, `--no-run`, `--no-ci`. Never commits or pushes. |
| `gravity show` | The manifest as a tree, a mapping table and a pass table, with flags, effective languages, estimates and CI; `--pass`. |
| `gravity validate` | Schema, local rules and the platform's checks, grouped with fixes; exits `1` on errors. |
| `gravity structure plan\|apply\|show` | Draft the structure (`--repo`, `--site`, `--write`), create what is missing (`--dry-run`), print the live tree (`--site`). |
| `gravity run` | Dry run (`--dry-run`) or real run; `--pass`, `--yes`, `--send <runId>`, `--allow-dirty`, `--note`, `--lease-timeout`; in CI also `--trigger`, `--branch`, `--from`, `--to`, `--parallel`, `--no-comment`, `--annotate`, `--strict`. |
| `gravity review [runId\|latest]` | Open a run's change request (prints the link without a terminal or with `--json`). |
| `gravity runs [--watch]` | Recent runs; `runs show <id>` per-pass state, `runs cancel <id>`. |
| `gravity approve [site/space]... [--all]` | List the spaces waiting for a grant and why, grant them (user token). |
| `gravity ci setup\|check` | Write the CI file, mint and install `GRAVITY_REPO_TOKEN` (`--provider`, `--no-secret`); check the file and the secret. |
| `gravity status` | Auth, connection, manifest, passes with targets and watermarks, recent runs, open bundles, tokens, health and capability warnings; `--runs N`, `--check`. |
| `gravity check` | The pull request gate: check passes (or the built-in check: drift, coverage of new operations, verbatim notes) plus every pass's doc impact; `--fail-on`, `--annotate`, `--comment`. |
| `gravity explain <page>` | Per block: last writer (repository, pass, commit, run), history, ownership and the page lock; `--block <key>`. |
| `gravity agent install` | Write the Gravity skill for `--tool claude\|cursor\|codex\|agents-md`; `--global`. |
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

### Upgrading CI files from 1.0.0-1.0.2

Repository tokens now live in `GRAVITY_REPO_TOKEN`. CI files written by
gravity 1.0.0 to 1.0.2 pass only `GRAVITY_TOKEN` (or, on GitHub with 1.0.2,
both secrets through the action's single `token` input); they keep working
while the token is stored in `GRAVITY_TOKEN`. `gravity status` names each such
file with the lines to change, and running `gravity ci setup` again rewrites a
gravity CI file it generated and you did not edit. Then store the repository
token as `GRAVITY_REPO_TOKEN` and, once no 0.x pipeline needs it, delete
`GRAVITY_TOKEN`.

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
needs a declared `check` pass, which also takes `coverageMin`, `require` and
`annotate` (`false` keeps its findings out of the CI annotations). Locked
verbatim pages whose file the pull request changes are reviewed first, as the
file will be imported.
`--fail-on` defaults to `drift, claims, verbatim`.

### Global flags

`--profile` (`GRAVITY_PROFILE`), `--api-url` (`GRAVITY_API_URL`), `--token`
(`GRAVITY_REPO_TOKEN`, `GRAVITY_TOKEN`), `--manifest` (`GRAVITY_MANIFEST`), `-C <dir>`, `--json`,
`--no-color` (`NO_COLOR`), `-q`, `-v`.

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
  https; plain http is accepted for localhost only.
- `--json` prints exactly one JSON document on stdout
  (`{ ok, command, version, data, warnings, error }`); progress goes to stderr.
- With `CI=true`, or without a terminal, output is plain ASCII and nothing
  prompts. On a terminal, prompts and live progress use the Charm libraries.
- CI detection is overridable with `GRAVITY_TRIGGER`, `GRAVITY_BRANCH`,
  `GRAVITY_HEAD_SHA`, `GRAVITY_BASE_SHA`, `GRAVITY_PR`, `GRAVITY_TAG`,
  `GRAVITY_RUN_URL`. The default branch comes from `origin/HEAD`, else
  `GRAVITY_DEFAULT_BRANCH`, GitLab's `CI_DEFAULT_BRANCH` or the GitHub event's
  `repository.default_branch`; `gravity setup` falls back to the checked-out
  branch.

Profiles live in `~/.config/gravity/profiles.yaml` (mode `0600`, honors
`XDG_CONFIG_HOME`), one per organization; `gravity org use <slug>` switches
between them. The v0.x `config.yaml` is ignored.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| `0` | Success, or nothing to do (no change in scope; a fork or Dependabot pull request without a token). |
| `1` | Findings: `validate` errors (also before a real run, and in a dry run that sending would fail on), `check` findings in `failOn`, structure conflicts, refused approvals, `ci check` on an incomplete setup, `status --check` on an unhealthy repository. |
| `2` | Operational error: network, bad input, invalid manifest, missing target, failed pass, unknown or inapplicable `--pass`, lease timeout, a branch behind or diverged from its upstream, uncommitted tracked files, a recording that no longer matches HEAD. |
| `3` | Licence refusal (`module_disabled`, `seat_limit`): ask a workspace administrator. |
| `130` | Interrupted (Ctrl-C or SIGTERM): the run was finished as cancelled and its lease released. |
| `4` | No usable credentials: no token (`token_missing`), only CI variables that were never expanded such as a literal `$(GRAVITY_REPO_TOKEN)`, in CI or locally without a profile token (`token_unresolved`), or a token the server rejects (`401`, also in the middle of a run). |

Precedence is `3` > `2` > `1` > `0`. A missing or unresolved token stops a
command before any request; a rejected token stops it at the first `401`
(a run ends without its finish call), and both exit `4`. Locally, with a
profile token, the error adds "run `gravity login` to sign in again". Fork
pull requests are detected per provider (GitHub event repositories, GitLab
source and target projects, `SYSTEM_PULLREQUEST_ISFORK` on Azure,
`CHANGE_FORK` on Jenkins, `CIRCLE_PR_*` on CircleCI), and GitHub runs started
by Dependabot by their actor; only those skip with a notice when the token is
missing. Bitbucket runs no pipeline for a fork
in the target repository, so a missing token there is always `4`. Documentation writes never fail a deploy:
only `check` findings exit `1`. `--strict` turns unapproved targets and missing
scopes into `2` and missing modules into `3`.

## Development

```bash
make build   # ./bin/gravity
make test    # go test ./...
make lint    # golangci-lint (pinned in go.mod) incl. the format check
make ci      # lint + test + build
```

Contributor standards and the architecture live in [AGENTS.md](AGENTS.md).
