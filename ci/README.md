# Gravity in CI

The `gravity` CLI is designed to run inside CI pipelines. It registers the repo
with the platform, checks API-doc and docs drift, authors documentation from the
code, reports coverage, and generates release notes.

These templates are for **downstream consumers** of the CLI. This repo's own
pipelines are [`.github/workflows/ci.yml`](../.github/workflows/ci.yml) (build,
lint, test) and [`.github/workflows/docs.yml`](../.github/workflows/docs.yml),
which is the reference implementation of the cadence below.

## The cadence contract

Three triggers, three jobs. `live` is whatever branch you deploy from (`main`,
`live`, `staging` — adjust the name, not the shape).

| Trigger              | Commands                                                                     | What it does                                     |
| -------------------- | ---------------------------------------------------------------------------- | ------------------------------------------------ |
| **Pull request**     | `gravity check api --ci --format github`<br>`gravity check docs --ci --format github` | Read-only. Annotates the diff with drift.        |
| **Push to `live`**   | `gravity ping --ci`<br>`gravity sync --ci`<br>`gravity docs generate --since <sha> --ci` | Registers the repo, shapes the site, authors what changed. |
| **Release tag pushed**| `gravity release-notes --ci --output proposal --from <previous tag> --to <tag>` | Drafts the release page in `releaseNotes.space`. |

`--ci` is a global flag accepted by every command, and it is switched on
automatically when the `CI` environment variable is `true` (GitHub Actions,
GitLab CI and Bitbucket Pipelines all set it): no prompts, plain ASCII output
without emoji.

Rules that make the cadence work:

- **Checks on PRs, authoring on `live`.** `check api` / `check docs` only read
  and verify, so exit `1` keeps its meaning there: findings are annotated on the
  diff. They are **advisory by default** (`continue-on-findings: "true"`); a repo
  opts into a blocking gate explicitly (`fail-on-findings: true` on the org
  reusable workflow, `GRAVITY_FAIL_ON_FINDINGS=true` here). Exit `2` always
  fails. `check docs` and `check api` look only at this repo's pages — the ones
  the platform attributes to it, or, before any attribution exists, the spaces
  `.gravity.yaml` declares — so a sibling repo's pages never fail your PR, and
  the whole site is never checked by default.
- **`ping` first, once, in the live job.** It registers/refreshes this repo's
  `connected_repo` row (branch, commit, resolved manifest) so write attribution
  and coverage see the right revision. Cheap, idempotent, writes no content.
- **`sync` before `docs generate`.** `sync` ensures spaces, collections and home
  pages exist, so generated pages land in a shaped site instead of creating one
  implicitly.
- **`--since` takes the previous *successful* run's commit, never `HEAD~1`.** A
  failed or skipped run must not leave a permanent documentation gap. Resolve
  it, best first:
  1. `gh run list --workflow=docs.yml --branch=live --status=success --limit=1 --json headSha`
  2. a marker tag the job moves on success, read with
     `git rev-parse docs-synced` (the portable option — the GitLab and Bitbucket
     templates use it)
  3. **a full run** — omit `--since` entirely. Never fall back to `HEAD~1`.
- **Serialize the live job.** `concurrency: { group: gravity-docs-live,
  cancel-in-progress: false }` on GitHub, `resource_group` on GitLab, a
  `deployment` environment on Bitbucket. Two overlapping runs race on page
  attribution (last-writer-wins) and on the declarative inventory.
- **Never gate a deploy on exit `1`.** Findings are documentation drift, not a
  broken build. In the push/release jobs treat exit `2` as the only hard
  failure. Where the PR into `live` *is* the deploy (a promotion PR), keep the
  PR checks advisory too.
- **`docs generate` is opt-in per repo.** It spends LLM budget and writes
  proposals onto pages; while a team hand-authors its pages, leave it off
  (`generate: false` / `GRAVITY_DOCS_GENERATE` unset) and let `ping` + `sync`
  run alone. It needs an LLM provider key on the tenant (Settings → AI), or it
  exits `2` with `no_provider_key`.
- **Release notes run on the tag push, not on `release: published`.** A release
  created by GoReleaser or any `GITHUB_TOKEN` never triggers another workflow,
  so a `release` trigger silently never fires. Range from the previous tag to
  the pushed tag explicitly (`--from`/`--to`).
- **Everything gravity writes is a draft + open proposal.** No CI job can change
  live documentation without a human approving it in the app.

## Exit-code convention

Every command follows the same convention so pipelines can react:

| Code | Meaning  | Typical CI behaviour                                  |
| ---- | -------- | ----------------------------------------------------- |
| `0`  | pass     | step succeeds (also: an empty commit range, a no-op)  |
| `1`  | findings | drift/gaps/stale bindings found                       |
| `2`  | error    | auth, network, bad input, unknown site/space          |
| `3`  | licence  | the workspace's licence lacks the module; ask an admin |

A non-zero exit fails the step by default. To let findings be advisory rather
than blocking without also swallowing real errors:

- **GitHub Actions** — `continue-on-findings: "true"` on the composite action
  (exit `1` → success, exit `2` still fails). `continue-on-error: true` on the
  step also works but hides errors too.
- **GitLab CI** — `allow_failure: { exit_codes: 1 }`.
- **Bitbucket Pipelines** — `<command> || [ $? -eq 1 ]`.

## Authentication

`GRAVITY_TOKEN` is the **secret**. Store it in your CI provider's secret store
and never commit it:

- **GitHub Actions** — repository or org secret, referenced as
  `${{ secrets.GRAVITY_TOKEN }}`.
- **GitLab CI** — a *masked, protected* CI/CD variable named `GRAVITY_TOKEN`.
- **Bitbucket Pipelines** — a *secured* repository variable named
  `GRAVITY_TOKEN`.

The site, spaces and API URL come from the committed `.gravity.yaml`; the
templates set nothing else. `GRAVITY_SITE`, `GRAVITY_SPACE` and
`GRAVITY_API_URL` are non-secret overrides — set them only when a pipeline must
target something other than the manifest (environment variables take
precedence over `.gravity.yaml`). A committed `token:` in `.gravity.yaml` is a
hard load error, never honored.

Every LLM call made by `release-notes`, `docs generate` and `check docs --ai` is
proxied through the Gravity gateway, so tenant provider keys never touch CI —
the CLI only ever sends the `sk_live_…` bearer token.

## Git history

`release-notes`, `check docs --ai` and `docs generate --since` all inspect git
history and diffs. Make sure the checkout includes it: `fetch-depth: 0` on
GitHub, `GIT_DEPTH: 0` on GitLab, `clone: { depth: full }` on Bitbucket.
Ranges that cannot resolve are exit `2` with a plain message ("unknown git ref
… fetch full history"); an empty range is a no-op that exits `0`.

## Repo identity, attribution and coverage

The live job's `gravity ping` is what makes the rest of this coherent:

- **Repo identity** is the normalized git remote (`github.com/Acme/orbit-api`),
  stable across clones, forks, CI checkouts and token rotation. A repo with no
  remote falls back to `<product.slug>/<product.repo>` from `.gravity.yaml`.
- **`gravity repos`** shows the result — this repo's registration plus the
  **sibling repos** publishing to the same site, with the spaces and collections
  each declares and when it last pinged and last wrote:

  ```bash
  gravity repos [--site <slug>] [--json]
  ```

  It performs the same handshake `ping` does, so running it also refreshes the
  registration. It publishes nothing and exits `0`, or `2` on auth/network.
- **Write attribution** — every page `sync` and `docs generate` author carries
  the repo identity, so the platform knows which repo owns which page. Orphan
  and duplicate detection is then scoped to *your* pages: a page a sibling repo
  wrote is never matched, never reported as an orphan, and never proposed for
  deletion.
- **Feature inventory** — `docs generate` publishes the units it found (the
  services, features, systems and API surfaces this repo contains) right after
  its planning phase, before authoring, so a run that dies mid-way still records
  what it saw.
- **`gravity coverage`** reports the result:

  ```bash
  gravity coverage [--site <slug>] [--repo <remoteKey>] [--kind <kind>] \
                   [--min <0..1>] [--format text|json|github] [--ci]
  ```

  `--min` defaults to `coverage.min` in `.gravity.yaml`. Exit `1` when the
  documented/total ratio falls below it or a `coverage.require` page slug is
  missing, `0` otherwise, `2` on auth/network. `check docs` folds the same
  findings in when `coverage.min` or `coverage.require` is set, so a PR gate
  needs one command rather than two.

Older platforms that do not advertise these features degrade silently or with a
single notice, and the commands keep their pre-v2 behaviour — never a failure.

## GitHub Actions

Use the composite action in [`github/action.yml`](github/action.yml), pinned to
major 0 (`@v0`): CLI 1.0 replaces these commands, and `@main` will follow it.

```yaml
jobs:
  docs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: Grupo-Impulso-Digital/gravity-cli/ci/github@v0
        with:
          token: ${{ secrets.GRAVITY_TOKEN }}
          command: "check docs"
```

The action downloads the prebuilt release binary (checksum-verified by
`install.sh` on Linux and macOS runners, `install.ps1` on Windows runners) and
caches it per release, so no Go toolchain is needed. For
`check api`, `check docs` and `coverage` it appends `--format github`, which emits
`::error::`/`::warning::` annotations so findings show up inline in the checks
UI; other commands never receive `--format`. It always runs with `--ci`.

Inputs:

| Input                  | Default                      | Notes                                                                |
| ---------------------- | ---------------------------- | -------------------------------------------------------------------- |
| `token`                | — (required)                 | `sk_live_…`, from a secret.                                          |
| `api-url`              | `""`                         | Empty so `.gravity.yaml` (or the hosted default) wins; set for self-hosted. |
| `site`                 | `""`                         | Empty so `.gravity.yaml` wins.                                       |
| `command`              | `check docs`                 | `ping`, `repos`, `check api`, `check docs`, `sync`, `docs generate`, `coverage`, `release-notes`. |
| `args`                 | `""`                         | Appended verbatim.                                                   |
| `format`               | `github`                     | Appended as `--format` only for `check api`, `check docs`, `coverage` (unless `args` sets `--format`/`--json`). |
| `since`                | `""`                         | Appended as `--since <ref>` for an incremental `docs generate`.      |
| `continue-on-findings` | `false`                      | Exit `1` → success; exit `2`/`3` still fail.                         |
| `version`              | `0`                          | A major version (`0`: the newest 0.x release), a release tag (`v0.3.0`) or `latest`; `source` builds from the action's own checkout (this repo's dogfood). |

The full three-trigger pipeline is
[`.github/workflows/docs.yml`](../.github/workflows/docs.yml) in this repo —
copy it, or call the org's reusable `gravity-docs.yml` if your repo is private
and can reach it.

## GitLab CI

See [`gitlab/.gitlab-ci.yml`](gitlab/.gitlab-ci.yml). Include or copy the jobs;
they install the prebuilt release binary with `install.sh` (cached per pinned
`GRAVITY_CLI_VERSION`) and run the full cadence. Set `GRAVITY_TOKEN` as a
masked/protected variable and `LIVE_BRANCH` to your deployment branch. Add a
`GRAVITY_CI_PUSH_TOKEN` (project access token with `write_repository`) so the
generate job moves the `docs-synced` marker and later runs stay incremental.
The marker push uses `-o ci.skip` and `release-notes` runs only for `v*` tags,
so moving the marker never starts a release pipeline. Release-notes ranges
ignore the marker and start at the previous `v*` tag.

## Bitbucket Pipelines

See [`bitbucket/pipe`](bitbucket/pipe). Copy its contents into
`bitbucket-pipelines.yml`. Add `GRAVITY_TOKEN` as a secured repository variable.
The steps install the prebuilt release binary with `install.sh` and push the
`docs-synced` marker after a successful generate, so later runs stay
incremental.
