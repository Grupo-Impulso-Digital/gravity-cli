# Gravity in CI

Every template here is one job with one step that runs `gravity run`. The CLI
detects the provider and the trigger (pull request, push, release tag,
schedule, manual run), resolves each pass's commit range from its watermark,
skips the passes whose scope did not change, and runs the rest in one Gravity
run. You do not pass a site, a space, a range or a command per trigger.

`gravity ci setup` writes the right file for your provider (with your default
branch and, when a pass needs it, a weekly schedule) and installs the secret;
`gravity ci check` tells you whether the file and the secret are in place.
The files below are the same templates with `main` as the default branch; they
are regenerated from `internal/cisetup` and checked by its tests.

| Provider | File | Where it goes |
| -------- | ---- | ------------- |
| GitHub Actions | [`github/gravity.yml`](github/gravity.yml) | `.github/workflows/gravity.yml` |
| GitLab CI | [`gitlab/gravity.yml`](gitlab/gravity.yml) | `.gitlab/gravity.yml`, plus `include: [{ local: .gitlab/gravity.yml }]` in `.gitlab-ci.yml` |
| Bitbucket Pipelines | [`bitbucket/bitbucket-pipelines.yml`](bitbucket/bitbucket-pipelines.yml) | `bitbucket-pipelines.yml` (merge the `pipelines` into yours) |
| Azure Pipelines | [`azure/azure-pipelines.gravity.yml`](azure/azure-pipelines.gravity.yml) | `azure-pipelines.gravity.yml`, then create a pipeline from it |

Jenkins and CircleCI snippets are printed by `gravity ci setup --provider jenkins`
and `gravity ci setup --provider circleci`.

## What a run does per trigger

| Trigger | Mode | Result |
| ------- | ---- | ------ |
| Pull request | dry run | Doc-impact comment on the pull request, step summary, annotations for findings. Exit `1` only for `check` findings in `failOn`. |
| Push to the default branch | write | One bundle of changes awaiting review in Gravity; inventory and handoffs recorded; watermarks advance. |
| Push to another branch | write, not authoritative | Passes that apply to that branch run; the manifest and the inventory are not updated from it. |
| Release tag (`v*`) | write | Changelog pages for the release; release-only passes. |
| Schedule | write | Passes that list the `schedule` trigger. |
| Manual | write | Passes whose triggers include `manual`, `push` or `schedule`; the log names the passes it left out. `gravity run --pass <name>` (on GitHub, the action's `args: --pass <name>`) runs one pass whatever its triggers. |

A run is manual when it is a GitHub `workflow_dispatch` (or `repository_dispatch`),
a GitLab pipeline started from the web, the API, a trigger, a parent pipeline
or chat (the GitLab template runs on `web` and `api` pipelines of any branch,
and on the default branch for the others), an Azure run with `BUILD_REASON`
`Manual`, a Bitbucket `gravity-manual` custom pipeline (the template sets
`GRAVITY_TRIGGER=manual`), a Jenkins build caused by a user (`BUILD_CAUSE` or
`ROOT_BUILD_CAUSE` from the EnvInject plugin, or `BUILD_USER_ID` from the
build-user-vars plugin), a CircleCI pipeline triggered through the API
(`CIRCLE_PIPELINE_TRIGGER_SOURCE`, which the snippet maps from
`pipeline.trigger_source`), or any local run. `GRAVITY_TRIGGER=manual`
overrides the detection anywhere.

Deploys are never gated by documentation writes. Only `check` findings exit `1`.

The first CI run of an AI pass (no watermark yet) is skipped with
`first_run_manual` unless the repository setting `ci.firstRun` is `run`: a
merge never launches a full AI pipeline nobody estimated. Do the first run
locally, `gravity run --dry-run`, review it, then `gravity run --send <runId>`.

## Secrets and tokens

`GRAVITY_REPO_TOKEN` holds the repository token (`gr_repo_…`) that `gravity
ci setup` mints with exactly the scopes of the repository's passes and stores in
the provider's secret store. The CLI reads `GRAVITY_REPO_TOKEN` first, then
`GRAVITY_TOKEN` (a user or organization token, and the name gravity 0.x
pipelines use), so a 0.x pipeline keeps its `GRAVITY_TOKEN` while the
repository moves to 1.x. Never commit a token, and never put it in
`.gravity.yaml` (a `token:` there is a manifest error).

How each template hands the token over:

| Template | Variables |
| -------- | --------- |
| GitHub | The action's `repo-token` (`secrets.GRAVITY_REPO_TOKEN`) and `token` (`secrets.GRAVITY_TOKEN`) inputs, exported under the same names, so `whoami` and `status` name the one used. |
| Azure | Maps both `GRAVITY_REPO_TOKEN` and `GRAVITY_TOKEN` in the step's `env`; an undefined one stays a literal `$(NAME)`, which the CLI skips. |
| GitLab, Bitbucket, CircleCI | Nothing to map: CI/CD, repository and project variables reach the job as environment variables, so either name works. |
| Jenkins | Binds `GRAVITY_REPO_TOKEN` from the credential `gravity-repo-token`. |
| Other CI | Runs `gravity run` with `GRAVITY_REPO_TOKEN` set. |

CI files written by gravity 1.0.0 to 1.0.2 pass only `GRAVITY_TOKEN` (GitHub
before 1.0.2, Azure, Jenkins), or both secrets through the one `token` input
(GitHub 1.0.2). They keep working while the token sits in `GRAVITY_TOKEN`.
`gravity status` flags them, and `gravity ci setup` rewrites a file it generated
that you did not edit (an edited file is kept, and it prints the lines to
change).

Pull request comments need a provider token next to it:

| Provider | Variable | Permission |
| -------- | -------- | ---------- |
| GitHub | `GITHUB_TOKEN` (set by the action) | `pull-requests: write` in the workflow |
| GitLab | `GITLAB_TOKEN` | project access token with the `api` scope (`CI_JOB_TOKEN` cannot post notes) |
| Bitbucket | `BITBUCKET_ACCESS_TOKEN` | repository access token with pull requests write |
| Azure | `SYSTEM_ACCESSTOKEN` (mapped by the template) | the build service may contribute to pull requests |

Without it the report is written to `gravity-report.md` and the log says so.
`--no-comment` turns comments off. `gravity ci setup` cannot mint the GitLab or
Bitbucket token for you: its closing summary names the token to create and
where to store it.

Fork pull requests run without secrets. The CLI detects them per provider
(the GitHub event's head and base repositories, GitLab's source and target
projects, `SYSTEM_PULLREQUEST_ISFORK` on Azure, `CHANGE_FORK` on Jenkins,
`CIRCLE_PR_*` on CircleCI) and then skips with a notice and exit `0`. GitHub
runs started by Dependabot (the event's actor, sender or pull request author)
get only Dependabot secrets and are skipped the same way; add
`GRAVITY_REPO_TOKEN` as a Dependabot secret to check them. Anywhere else no
token, or only literal variable references such as `$(GRAVITY_REPO_TOKEN)` on
Azure, fails with exit `4`, so a missing secret never passes silently. The GitHub template uses `pull_request`, never
`pull_request_target`, which would run fork code with your secrets.

## Reports

- **GitHub**: the report is appended to the step summary, findings become
  `::error`/`::warning` annotations, and the action exposes `run-url` and
  `exit-code` outputs.
- **GitLab**: findings go to `gl-code-quality-report.json`, which the template
  publishes as a Code Quality report; `gravity-report.md` is kept as a job
  artifact.
- **Azure**: the report is attached to the build summary
  (`##vso[task.uploadsummary]`) and findings become `##vso[task.logissue]`
  entries.
- **Bitbucket**: `gravity-report.md`, kept as a step artifact.
- **Jenkins, CircleCI**: `gravity-report.md` in the working copy.

## The GitHub action

```yaml
- uses: Grupo-Impulso-Digital/gravity-cli/ci/github@v1
  with:
    repo-token: ${{ secrets.GRAVITY_REPO_TOKEN }}
    token: ${{ secrets.GRAVITY_TOKEN }}
```

| Input | Default | Notes |
| ----- | ------- | ----- |
| `repo-token` | `""` | The repository token, `secrets.GRAVITY_REPO_TOKEN`; exported as `GRAVITY_REPO_TOKEN`, read first. |
| `token` | `""` | A user or organization token, `secrets.GRAVITY_TOKEN`; exported as `GRAVITY_TOKEN`. With neither, fork and Dependabot pull requests are skipped and every other run exits `4`. |
| `command` | `run` | `run`, `check` or `status`. |
| `args` | `""` | Extra flags, for example `--pass developer-api`. |
| `version` | `1` | A major (`1`), a release (`v1.2.3`), `latest`, or `source` (build from the action checkout). |
| `working-directory` | `.` | Directory of the repository. |
| `api-url` | `""` | Only for a self-hosted platform; empty lets `.gravity.yaml` decide. |

| Output | Value |
| ------ | ----- |
| `run-url` | The Gravity run, or the bundle to review when the run proposed changes. |
| `exit-code` | `0` success, `1` findings, `2` error, `3` licence refusal, `4` missing, unresolved or rejected token. |

The action resolves the version through `install.sh`, caches the binary per
resolved release with `actions/cache`, and installs it with `install.sh`
(`install.ps1` on Windows runners). No Go toolchain is needed.

## Versions

Templates and the action install the newest `1.x` release
(`GRAVITY_VERSION=1`, `ci/github@v1`), so a future 2.0 never changes a 1.x
pipeline. Pipelines still on CLI 0.3 keep `ci/github@v0` or `GRAVITY_VERSION=0`
and never receive 1.x; CLI 1.1 no longer converts v1 manifests, so move such a
repository with `gravity setup`, which writes a fresh version 2 manifest.

`install.sh` accepts `GRAVITY_VERSION=<major>` (the newest release of that
major), a release tag (`v1.2.3`) or `latest`; `GRAVITY_INSTALL_DIR` picks where
the binary goes. It verifies the release checksum.

## Git history

Ranges need history: `fetch-depth: 0` on GitHub, `GIT_DEPTH: 0` on GitLab,
`clone: depth: full` on Bitbucket, `fetchDepth: 0` on Azure. A shallow clone is
deepened when the CLI can, and a range it cannot resolve exits `2` with the
reason.
