# gravity-cli

`gravity` keeps a product's documentation true to its code. A repository
declares a few **code facts** in `.gravity.yaml`; the Gravity app holds the
**editorial intent**: which sites and spaces the repository feeds, what each
**pass** writes, for whom, in which voice. In CI, `gravity run` loads the
repository's effective configuration from the app, works out what changed since
each pass last ran (commits **and** code: OpenAPI operations, symbols, units),
and lets every pass update its target. Everything one run proposes is reviewed
in the app as one bundle.

> **1.0 is a clean break.** The v0.x commands (`sync`, `docs`, `release-notes`,
> `check api|docs`, `coverage`, `capture`, `nucleus`, `auth`, `doctor`, `ping`,
> `repos`, `spaces`) and the v1 manifest are gone; invoking a removed command
> prints its replacement and exits `2`. `gravity init` converts a v1
> `.gravity.yaml` in one step. Pipelines pinned to `ci/github@v0` or
> `GRAVITY_VERSION=0` stay on 0.x until you migrate them.

## Install

`gravity` is a single static binary.

```bash
curl -fsSL https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.sh | sh
```

The script installs the newest 1.x release for your OS and architecture,
verifies its checksum, and puts it in `/usr/local/bin` (or `~/.local/bin`).
`GRAVITY_VERSION` takes a major (`1`), a release (`v1.2.3`) or `latest`;
`GRAVITY_INSTALL_DIR` picks the directory.

```bash
brew install Grupo-Impulso-Digital/tap/gravity                     # macOS, Linux
scoop bucket add impulso https://github.com/Grupo-Impulso-Digital/scoop-bucket
scoop install gravity                                               # Windows
irm https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.ps1 | iex
go install github.com/Grupo-Impulso-Digital/gravity-cli/cmd/gravity@latest
```

## Quick start

```bash
gravity login      # browser sign-in; stores a profile in ~/.config/gravity/profiles.yaml
gravity init       # at most three questions, a preview, then .gravity.yaml + the CI file + the secret
gravity preview    # every page your working tree would change, before you push
```

`gravity init` writes the CI file for your provider. On GitHub it is one step:

```yaml
- uses: Grupo-Impulso-Digital/gravity-cli/ci/github@v1
  with:
    token: ${{ secrets.GRAVITY_REPO_TOKEN || secrets.GRAVITY_TOKEN }}
```

Anywhere else it is one line, with `GRAVITY_REPO_TOKEN` from the CI secret
store:

```bash
curl -fsSL https://app.gravitydocs.io/install.sh | GRAVITY_VERSION=1 GRAVITY_INSTALL_DIR=.gravity-bin sh && .gravity-bin/gravity run
```

From then on, pull requests get a doc-impact comment, pushes to the default
branch propose updates for review, and release tags write the changelog. See
[ci/README.md](ci/README.md) for GitLab, Bitbucket and Azure.

```bash
gravity status     # auth, product, passes, targets, watermarks, last runs, open bundles, health
gravity passes     # which passes apply to this branch and trigger
gravity explain dev-portal/api/refunds   # who wrote each block, from which commit and run
```

## Passes

A pass is one job a repository does for its documentation: a target (site,
space, optionally a collection), a kind, triggers, audiences and instructions.

| Kind | Writes | Typical target |
| ---- | ------ | -------------- |
| `reference` | API reference pages from OpenAPI documents (deterministic api blocks, optional AI prose) | Developer portal › API |
| `guides` | Guides kept true to the code: an impact analysis, a page plan, then block-level edits that cite commits | Product docs › Guides |
| `verbatim` | Markdown/MDX files imported as-is and locked to the repository | Handbook |
| `changelog` | Release pages and an Unreleased page, from commits or `CHANGELOG.md` | Product › Changelog |
| `nucleus` | Facts about the product in Nucleus, the shared memory, tagged with this repository | product namespace |
| `check` | Nothing: drift, coverage and claim review for pull requests | — |
| `capture` | Screenshots-and-steps pages through the Gravity Doc Agent | Product › Guides |

Passes live in the app by default: `gravity init` registers them there, and
you edit targets, instructions and review rules in the app without touching the
repository. A pass declared in `.gravity.yaml` (passes-as-code,
`gravity init --passes-as-code`) appears in the app locked as "managed in
repo".

Each pass carries its own instructions. The app composes them with the
organization, site, space and collection layers; the CLI never embeds them,
and `gravity preview` shows the composed result per pass.

## `.gravity.yaml`

The smallest valid manifest is one line:

```yaml
version: 2
```

Identity comes from the git remote and passes come from the app. Everything else
is optional: `product`, `apiUrl`, `appPasses` (`allow` | `ignore`), `code`
(`openapi`, `entrypoints`, `include`, `exclude`, `units`), `docs`, and
`passes`. Every pass has a `name` and a `kind`; passes that write pages need a
`target` (`<site>/<space>[/<collection>...]`). The manifest is validated
against an embedded JSON Schema: an unknown key fails with a suggestion
(`passes[0].trigers: unknown key (did you mean "triggers"?)`), a `token:`
anywhere is refused, and paths must stay inside the repository.

`code.include` says what counts as code: a pass without `scope.paths` reacts
only to changes under it (plus `code.openapi`), and removed or renamed symbols
are read only there. `code.exclude` drops paths from every change set.

Only the authoritative branch (the default branch unless the app says
otherwise) stores the manifest's passes in Gravity. Other branches see their
local passes as an overlay (`gravity passes` marks them `repo (local)`).

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
Deleting the file removes that language version. `gravity preview` lists
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
| `gravity logout` | Revoke the current user token and remove its profile; `--all`. With `--token`, `GRAVITY_REPO_TOKEN` or `GRAVITY_TOKEN` holding a user token, revokes that token (repository tokens are revoked in the app). |
| `gravity whoami` | Principal, organization, token kind, scopes, expiry, API URL, profile. |
| `gravity init` | Connect the repository in at most three questions; `--yes`, `--product`, `--passes-as-code`, `--app-passes`, `--ci <provider>`, `--no-secret`, `--dry-run`, `--repo <id>`. Never commits or pushes. |
| `gravity status` | Auth, connection, manifest, passes with targets and watermarks, recent runs, open bundles, tokens, health and capability warnings; `--runs N`, `--check`. |
| `gravity passes` | `list`, `show <name>`, `edit <name>`; `--trigger`, `--branch`. |
| `gravity run` | The pipeline over committed history (uncommitted changes are never sent; `preview` shows them); `--pass`, `--trigger`, `--branch`, `--from`, `--to`, `--note`, `--dry-run`, `--lease-timeout`, `--parallel`, `--no-comment`, `--annotate`, `--strict`. |
| `gravity preview` | Every pass as a dry run over the working tree (or `--committed`): page diffs, composed instructions, cost; `--format text\|diff\|json`, `--open`. |
| `gravity check` | The pull request gate: check passes (or the built-in check: drift, coverage of new operations, verbatim notes) plus every pass's doc impact; `--fail-on`, `--annotate`, `--comment`. |
| `gravity explain <page>` | Per block: last writer (repository, pass, commit, run), history, ownership and the page lock; `--block <key>`. |
| `gravity version` | Version, commit, build date, Go version, platform. |

### `gravity run` outside CI

Locally, and on a GitHub `workflow_dispatch` (or any manual pipeline), the
trigger is `manual`. A manual run runs the passes whose triggers include
`manual`, `push` or `schedule`, and names the ones it left out;
`gravity run --pass <name>` runs one pass whatever its triggers (its branch
rules still apply; a server older than this rule refuses it with a hint).
A run reads committed history only: every write cites the commit it comes
from, so uncommitted changes are not part of it, and `gravity run` warns when
the working tree has some. `gravity preview` is the command that reads the
working tree.

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

### `gravity init`

```
$ gravity init
✓ Signed in as dave@acme.io · Acme
✓ github.com/acme/billing-api · TypeScript · OpenAPI 3.1.0 (42 operations) · Next.js UI (18 routes) · 31 Markdown docs · GitHub Actions
? Product › Acme Platform (gateway connected)                                   [1]
? What should this repository keep up to date?   site: Developer Portal          [2]
  ✓ Developer Portal › API        reference  ← OpenAPI api/openapi.yaml (42 operations)
  ✓ Developer Portal › Guides     guides     ← Next.js routes in app (18)
  ✓ Developer Portal › Changelog  changelog  ← tags v* (12 releases)  [new space]
    Developer Portal › Handbook   verbatim   ← docs/handbook (9 files)  [new space]
  ✓ Nucleus memory                nucleus
    Change site… (now Developer Portal)
Preview   (every file with its full content, passes, new spaces, token scopes, secret)
? Write these and wire CI? › Write + set the secret (gh) · Write files only · Cancel   [3]
✓ Connected billing-api with 4 passes        Try it now:  gravity preview
```

Detection is local (remote, languages, OpenAPI documents, UI and server routes,
docs folders, runbooks, release tags, CI provider). The repository token
carries exactly the scopes its passes need; it is installed with
`gh secret set` / `glab variable set` on stdin when that CLI is signed in, or
printed once to paste. Existing CI files are never overwritten. `--yes` accepts
every suggestion (required without a terminal); `--dry-run` stops after the
preview.

init stores the repository token as `GRAVITY_REPO_TOKEN` and never touches
`GRAVITY_TOKEN`, which CI files still running gravity 0.x (`ci/github@v0` or
`@main`, `GRAVITY_VERSION=0`, `gravity sync`, ...) keep using; init names those
files so you can remove them once 1.x runs. A workflow that calls the shared
`gravity-docs.yml` is not one of them: that workflow runs gravity 1.x for a
version 2 manifest with `GRAVITY_REPO_TOKEN`, so init keeps it instead of
adding `gravity.yml`.

### Global flags

`--profile` (`GRAVITY_PROFILE`), `--api-url` (`GRAVITY_API_URL`), `--token`
(`GRAVITY_REPO_TOKEN`, `GRAVITY_TOKEN`), `--manifest` (`GRAVITY_MANIFEST`), `-C <dir>`, `--json`,
`--no-color` (`NO_COLOR`), `-q`, `-v`.

- Credentials: `--token` > `GRAVITY_REPO_TOKEN` > `GRAVITY_TOKEN` > profile;
  `whoami` and `status` name the source. A variable holding an unexpanded
  reference (`$(NAME)`, `${{ … }}`, `$NAME`) is skipped. Tokens are never
  read from `.gravity.yaml`.
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
  `repository.default_branch`; `gravity init` falls back to the checked-out
  branch.

Profiles live in `~/.config/gravity/profiles.yaml` (mode `0600`, honors
`XDG_CONFIG_HOME`). A token in the v0.x `config.yaml` is copied once into the
profile `default`; the old file is never modified.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| `0` | Success, or nothing to do (no change in scope; a fork or Dependabot pull request without a token). |
| `1` | Findings: `check` findings in `failOn` (`run` and `check`), or `status --check` on an unhealthy repository. |
| `2` | Operational error: network, bad input, invalid manifest, missing target, failed pass, unknown or inapplicable `--pass`, lease timeout, removed command. |
| `3` | Licence refusal (`module_disabled`, `seat_limit`): ask a workspace administrator. |
| `4` | No usable credentials: no token (`token_missing`), only CI variables that were never expanded such as a literal `$(GRAVITY_REPO_TOKEN)` (`token_unresolved`), or a token the server rejects (`401`, also in the middle of a run). |

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
