# Gravity in CI

The `gravity` CLI is designed to run inside CI pipelines. It generates release
notes, checks API-doc drift, and checks docs completeness against the code in
the checked-out repository.

## Exit-code convention

Every check/release command follows the same convention so pipelines can react:

| Code | Meaning  | Typical CI behaviour            |
| ---- | -------- | ------------------------------- |
| `0`  | pass     | step succeeds                   |
| `1`  | findings | drift/gaps/stale bindings found |
| `2`  | error    | auth, network, or bad input     |

A non-zero exit fails the step by default. If you want findings (exit `1`) to be
advisory rather than blocking, mark the step as allowed-to-fail in your CI
system (`allow_failure: true` in GitLab, `continue-on-error: true` in GitHub).

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
`.gravity.yaml`, which is exactly what you want in CI.

Every LLM call made by `release-notes` and `check docs --ai` is proxied through
the Gravity gateway, so tenant provider keys never touch CI — the CLI only ever
sends the `sk_live_…` bearer token.

## GitHub Actions

Use the composite action in [`github/action.yml`](github/action.yml):

```yaml
jobs:
  docs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0 # release-notes & --ai need full history
      - uses: impulso/gravity-cli/ci/github@main
        with:
          token: ${{ secrets.GRAVITY_TOKEN }}
          site: docs
          command: "check docs"
          args: "--ci --format github"
```

`--format github` emits `::error::`/`::warning::` annotations so findings show
up inline in the checks UI.

## GitLab CI

See [`gitlab/.gitlab-ci.yml`](gitlab/.gitlab-ci.yml). Include or copy the jobs;
they install the CLI with `go install` and run the checks. Set `GRAVITY_TOKEN`
as a masked/protected variable.

## Bitbucket Pipelines

See [`bitbucket/pipe`](bitbucket/pipe). Copy its contents into
`bitbucket-pipelines.yml`. Add `GRAVITY_TOKEN` as a secured repository variable.

## Authoring vs checking

Run `check api` / `check docs` on pull requests (they only read + verify, exit
`1` on findings). Run `gravity sync` on merge or release to author the
`sources`/`documents` mappings from `.gravity.yaml` — it writes a draft +
proposal for human review and exits `0`/`2` only (never `1`). Keeping authoring
off the PR gate preserves the meaning of a findings exit.

## Note on git history

`release-notes` and `check docs --ai` inspect git history and diffs. Make sure
your CI checkout includes the relevant history (for example
`fetch-depth: 0` on GitHub, or unshallow the clone) so ranges resolve.
