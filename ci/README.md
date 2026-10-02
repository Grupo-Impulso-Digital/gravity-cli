# Gravity in CI

Every template here is one job with one step that runs `gravity run`. The CLI
detects the provider and the trigger (pull request, push, release tag,
schedule, manual run), resolves each pass's commit range from its watermark,
skips the passes whose scope did not change, and runs the rest in one Gravity
run. You do not pass a site, a space, a range or a command per trigger.

`gravity init` writes the right file for your provider (with your default
branch and, when a pass needs it, a weekly schedule) and installs the secret.
The files below are the same templates with `main` as the default branch; they
are regenerated from `internal/cisetup` and checked by its tests.

| Provider | File | Where it goes |
| -------- | ---- | ------------- |
| GitHub Actions | [`github/gravity.yml`](github/gravity.yml) | `.github/workflows/gravity.yml` |
| GitLab CI | [`gitlab/gravity.yml`](gitlab/gravity.yml) | `.gitlab/gravity.yml`, plus `include: [{ local: .gitlab/gravity.yml }]` in `.gitlab-ci.yml` |
| Bitbucket Pipelines | [`bitbucket/bitbucket-pipelines.yml`](bitbucket/bitbucket-pipelines.yml) | `bitbucket-pipelines.yml` (merge the `pipelines` into yours) |
| Azure Pipelines | [`azure/azure-pipelines.gravity.yml`](azure/azure-pipelines.gravity.yml) | `azure-pipelines.gravity.yml`, then create a pipeline from it |

Jenkins and CircleCI snippets are printed by `gravity init --ci jenkins` and
`gravity init --ci circleci`.

## What a run does per trigger

| Trigger | Mode | Result |
| ------- | ---- | ------ |
| Pull request | dry run | Doc-impact comment on the pull request, step summary, annotations for findings. Exit `1` only for `check` findings in `failOn`. |
| Push to the default branch | write | One bundle of changes awaiting review in Gravity; inventory and handoffs recorded; watermarks advance. |
| Push to another branch | write, not authoritative | Passes that apply to that branch run; the manifest and the inventory are not updated from it. |
| Release tag (`v*`) | write | Changelog pages for the release; release-only passes. |
| Schedule | write | Passes that list the `schedule` trigger. |

Deploys are never gated by documentation writes. Only `check` findings exit `1`.

## Secrets and tokens

`GRAVITY_TOKEN` is a repository token (`gr_repo_…`) minted by `gravity init`
with exactly the scopes of the repository's passes. Store it in the provider's
secret store; never commit it, and never put it in `.gravity.yaml` (a `token:`
there is a manifest error).

Pull request comments need a provider token next to it:

| Provider | Variable | Permission |
| -------- | -------- | ---------- |
| GitHub | `GITHUB_TOKEN` (set by the action) | `pull-requests: write` in the workflow |
| GitLab | `GITLAB_TOKEN` | project access token with the `api` scope (`CI_JOB_TOKEN` cannot post notes) |
| Bitbucket | `BITBUCKET_ACCESS_TOKEN` | repository access token with pull requests write |
| Azure | `SYSTEM_ACCESSTOKEN` (mapped by the template) | the build service may contribute to pull requests |

Without it the report is written to `gravity-report.md` and the log says so.
`--no-comment` turns comments off.

Fork pull requests run without secrets. The CLI then exits `0` with a warning
instead of failing. The GitHub template uses `pull_request`, never
`pull_request_target`, which would run fork code with your secrets.

## Reports

- **GitHub**: the report is appended to the step summary, findings become
  `::error`/`::warning` annotations, and the action exposes `run-url` and
  `exit-code` outputs.
- **GitLab**: findings go to `gl-code-quality-report.json`, which the template
  publishes as a Code Quality report.
- **Azure**: the report is attached to the build summary
  (`##vso[task.uploadsummary]`) and findings become `##vso[task.logissue]`
  entries.
- **Bitbucket, Jenkins, CircleCI**: `gravity-report.md` in the working copy.

## The GitHub action

```yaml
- uses: Grupo-Impulso-Digital/gravity-cli/ci/github@v1
  with:
    token: ${{ secrets.GRAVITY_TOKEN }}
```

| Input | Default | Notes |
| ----- | ------- | ----- |
| `token` | required | The repository token. Empty on fork pull requests (the run is skipped). |
| `command` | `run` | `run`, `check` or `status`. |
| `args` | `""` | Extra flags, for example `--pass developer-api`. |
| `version` | `1` | A major (`1`), a release (`v1.2.3`), `latest`, or `source` (build from the action checkout). |
| `working-directory` | `.` | Directory of the repository. |
| `api-url` | `""` | Only for a self-hosted platform; empty lets `.gravity.yaml` decide. |

| Output | Value |
| ------ | ----- |
| `run-url` | The Gravity run, or the bundle to review when the run proposed changes. |
| `exit-code` | `0` success, `1` findings, `2` error, `3` licence refusal. |

The action resolves the version through `install.sh`, caches the binary per
resolved release with `actions/cache`, and installs it with `install.sh`
(`install.ps1` on Windows runners). No Go toolchain is needed.

## Versions

Templates and the action install the newest `1.x` release
(`GRAVITY_VERSION=1`, `ci/github@v1`), so a future 2.0 never changes a 1.x
pipeline. Pipelines still on CLI 0.3 keep `ci/github@v0` or `GRAVITY_VERSION=0`
and never receive 1.0; run `gravity init` to convert the repository when you are
ready.

`install.sh` accepts `GRAVITY_VERSION=<major>` (the newest release of that
major), a release tag (`v1.2.3`) or `latest`; `GRAVITY_INSTALL_DIR` picks where
the binary goes. It verifies the release checksum.

## Git history

Ranges need history: `fetch-depth: 0` on GitHub, `GIT_DEPTH: 0` on GitLab,
`clone: depth: full` on Bitbucket, `fetchDepth: 0` on Azure. A shallow clone is
deepened when the CLI can, and a range it cannot resolve exits `2` with the
reason.
