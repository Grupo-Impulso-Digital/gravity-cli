# Model quality eval

The eval kit measures how well a model writes a guides page through the CLI
1.2 harness. A platform admin reads its results to set a model's quality
label in Gravity (`recommended`, `not_recommended`, ...). Everything lives in
`testdata/eval/` and `internal/eval/`.

## The fixture

`testdata/eval/make-fixture.sh [-f] <dir> [origin-url]` creates a git
repository at `<dir>` (`-f` replaces it):

- a Topics REST API with 17 operations in `api/openapi.yaml` and Express
  handlers in `src/routes/`; 14 are public and 3 are internal or temporary
  (`POST /topics/{topicId}/legacy-import`, `POST /internal/reindex`,
  `GET /internal/debug/cache`, marked `x-internal: true` and described as not
  to be documented);
- a technical Markdown document, `docs/architecture.md`;
- seven commits, three with French messages and four with English ones, with
  fixed authors and dates, so the same inputs always give the same commits;
- a `.gravity.yaml` with one guides pass, `topics-guide`, that targets
  `$EVAL_TARGET` (default `topics/guides`) and asks for one page
  (`maxPages: 1`);
- an `origin` remote set to `origin-url` (default `$EVAL_REMOTE`, else
  `https://github.com/gravity-eval/topics-api.git`) and no upstream tracking.

`eval.FixtureEndpoints` lists the same 17 operations for the scorer; a test
generates the fixture and checks that both agree.

## Seeding a server

`testdata/eval/setup.sh` prepares a Gravity server once and writes
`testdata/eval/.env` (mode `0600`, ignored by git). It needs `bash`, `curl`,
`jq` and `git`.

| Variable | Required | Meaning |
| -------- | -------- | ------- |
| `GRAVITY_API_URL` | yes | the Gravity server |
| `GRAVITY_TOKEN` | yes | an API key (or CLI user token) of an admin of the eval organization |
| `EVAL_ORG` | no | organization slug to act in (`X-Gravity-Organization`); default: the token's organization |
| `EVAL_CREATE_ORG` | no | `1` creates `EVAL_ORG` as a new workspace when the token is not a member of it |
| `EVAL_REMOTE` | no | the fixture's origin URL, registered as the repository |
| `EVAL_SITE`, `EVAL_SPACE` | no | site and space names (default `Topics`, `Guides`) |
| `EVAL_PRODUCT` | no | the product created with the repository (default `Topics API`) |
| `EVAL_REPO` | no | where the fixture is generated (default `testdata/eval/work/topics-api`) |
| `GRAVITY` | no | the gravity binary (default `bin/gravity`, built when missing) |

It calls the platform through its MCP endpoint (`/api/mcp`), so every step is
authorised like the app:

1. creates the site (default language English, `i18n.defaultLanguage: en`)
   and its space when they do not exist;
2. registers the repository with a new product unless its remote key is
   already registered;
3. mints a repository token (`POST /api/v1/repos/{repoId}/tokens`);
4. generates the fixture and runs `gravity approve --all` with the user
   token, so the guides pass may write to the space;
5. writes `GRAVITY_API_URL`, `GRAVITY_REPO_TOKEN`, `EVAL_REMOTE`,
   `EVAL_TARGET` and `EVAL_ORG` to `testdata/eval/.env`.

Re-running it reuses the site, space and repository and mints a new token.

## Running an eval

```bash
make eval MODEL=<id> [MAX_OUTPUT=<n>]
```

The target builds `bin/gravity` and runs `testdata/eval/run.sh`, which reads
`testdata/eval/.env` when it exists (or `GRAVITY_API_URL` and
`GRAVITY_REPO_TOKEN`/`GRAVITY_TOKEN` from the environment), regenerates the
fixture, then runs

```bash
gravity run --dry-run --yes --json --model <id> [--max-output <n>]
```

in it. `--max-output` is the hidden development flag sent as the bundle's
`maxOutput`. The run's output goes to `testdata/eval/results/<stem>.log` and
`<stem>.log.json` (both ignored by git), and the scorer writes
`testdata/eval/results/<stem>.json` and `<stem>.md`, where `<stem>` is
`<model>[-mo<n>]-<UTC time>`. `make eval` exits `1` when the run recorded
nothing to score.

## Scoring

`go run ./internal/eval/cmd/score` reads the recording the dry run saved,
`.gravity/runs/<runId>.json` (the newest one written since the run started,
or `-recording <file>`). A dry run records only when it has changes, so a run
without a recording, or a recording without a page change, is scored as
failed. Flags: `-repo`, `-recording`, `-since`, `-duration`, `-model`,
`-max-output`, `-language` (default `en`), `-detail-words` (default 40),
`-run-exit`, `-out`, `-name`.

The pages are the guides pass's recorded changes (all changes when the
recording has no guides pass). The report holds:

| Field | Meaning |
| ----- | ------- |
| `checks` | the page checks of `check_page` (`agent.CheckBlocks`) run on every recorded block: E1 language, E2 code in prose, E3 Markdown heading, E4 planning voice and E5 output markers once per page, plus E7 once per run, which passes when every public endpoint is covered; `passRate` is passed over total |
| `coverage` | public endpoints covered, out of 14. An endpoint is covered when a block names its method and path (`GET /topics/{id}`, `GET /topics/:id`, a table row `GET \| /topics`, or a URL after the method); parameter names do not matter |
| `wordsPerEndpoint` | min, median and mean words of the section that documents each covered endpoint: from the heading that names it (or the heading before the block that names it) to the next heading of the same or a higher level, code excluded; each endpoint takes, among the sections that name it (heading matches first), the one with the most words once split among every endpoint those sections name, so an overview table does not outweigh a per-endpoint section. A covered endpoint with at least `detailWords` words is `detailed` |
| `internalLeaks` | blocks that name the path of an internal endpoint |
| `turns`, `inputTokens`, `outputTokens`, `totalTokens` | the sum of the recorded passes' `usage` |
| `durationSeconds` | the wall time of `gravity run` |
| `generation` | the model, provider and instructions version Gravity served |
| `pass`, `failures` | acceptance A1: one page, 14/14 public endpoints covered in detail, no internal leak, no E1–E5 error and at least one heading block per page |

Pass warnings (for example `partial (forced at cap)`) are copied to the
report.
