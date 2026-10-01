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
| **Push to `live`**   | `gravity ping`<br>`gravity sync --ci`<br>`gravity docs generate --since <sha> --ci` | Registers the repo, shapes the site, authors what changed. |
| **Release published**| `gravity release-notes --ci --output proposal`                                | Drafts the release page.                         |

Rules that make the cadence work:

- **Checks on PRs, authoring on `live`.** `check api` / `check docs` only read
  and verify, so exit `1` keeps its meaning there. Keeping authoring off the PR
  gate is what preserves that meaning.
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
  failure.
- **Everything gravity writes is a draft + open proposal.** No CI job can change
  live documentation without a human approving it in the app.

## Exit-code convention

Every command follows the same convention so pipelines can react:

| Code | Meaning  | Typical CI behaviour            |
| ---- | -------- | ------------------------------- |
| `0`  | pass     | step succeeds                   |
| `1`  | findings | drift/gaps/stale bindings found |
| `2`  | error    | auth, network, or bad input     |

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

`GRAVITY_API_URL` and `GRAVITY_SITE` are non-secret and can be set as plain
variables. Environment variables take precedence over any committed
`.gravity.yaml`, which is exactly what you want in CI. A committed `token:` in
`.gravity.yaml` is a hard load error, never honored.

Every LLM call made by `release-notes`, `docs generate` and `check docs --ai` is
proxied through the Gravity gateway, so tenant provider keys never touch CI —
the CLI only ever sends the `sk_live_…` bearer token.

## Git history

`release-notes`, `check docs --ai` and `docs generate --since` all inspect git
history and diffs. Make sure the checkout includes it: `fetch-depth: 0` on
GitHub, `GIT_DEPTH: 0` on GitLab, `clone: { depth: full }` on Bitbucket.
Ranges that cannot resolve are exit `2`.

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

Use the composite action in [`github/action.yml`](github/action.yml):

```yaml
jobs:
  docs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: Grupo-Impulso-Digital/gravity-cli/ci/github@main
        with:
          token: ${{ secrets.GRAVITY_TOKEN }}
          site: docs
          command: "check docs"
          args: "--ci --format github"
```

`--format github` emits `::error::`/`::warning::` annotations so findings show
up inline in the checks UI.

Inputs:

| Input                  | Default                      | Notes                                                                |
| ---------------------- | ---------------------------- | -------------------------------------------------------------------- |
| `token`                | — (required)                 | `sk_live_…`, from a secret.                                          |
| `api-url`              | `https://api.gravitydocs.io` | Override for self-hosted.                                            |
| `site`                 | `""`                         | Optional when `.gravity.yaml` sets `site`.                           |
| `command`              | `check docs`                 | `ping`, `repos`, `check api`, `check docs`, `sync`, `docs generate`, `coverage`, `release-notes`. |
| `args`                 | `--ci --format github`       | Appended verbatim. Pass `""` for commands with no `--ci`/`--format` (`ping`, `repos`). |
| `since`                | `""`                         | Appended as `--since <ref>` for an incremental `docs generate`.      |
| `continue-on-findings` | `false`                      | Exit `1` → success; exit `2` still fails.                            |
| `version`              | `main`                       | Git ref of this repo to build the CLI from.                          |

The full three-trigger pipeline is
[`.github/workflows/docs.yml`](../.github/workflows/docs.yml) in this repo —
copy it, or call the org's reusable `gravity-docs.yml` if your repo is private
and can reach it.

## GitLab CI

See [`gitlab/.gitlab-ci.yml`](gitlab/.gitlab-ci.yml). Include or copy the jobs;
they install the CLI with `go install` and run the full cadence. Set
`GRAVITY_TOKEN` as a masked/protected variable and `LIVE_BRANCH` to your
deployment branch.

## Bitbucket Pipelines

See [`bitbucket/pipe`](bitbucket/pipe). Copy its contents into
`bitbucket-pipelines.yml`. Add `GRAVITY_TOKEN` as a secured repository variable.
