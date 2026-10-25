# Troubleshooting

Always use `--json` and read `.ok`, `.error.code`, `.error.message`, `.warnings[]`.

## Validate issues

`gravity validate --json` returns `.data.issues[]` with `severity`, `code`, `pass`, `path`, `message`,
`hint`, `source` (`local` or `server`). Errors block runs; warnings inform. Dry runs run the same server
checks, so dry run == real run for conflicts.

| code | meaning | fix |
| --- | --- | --- |
| `manifest_invalid` | schema violation at `path` | edit the key named by `path` |
| `slug_taken` | an existing page owns that slug (message names title and owner) | rename the slug (`slug:` or file name), or set `options.adopt: true` if the user wants the repo to take the page over |
| `duplicate_slug` | two mapped files produce the same slug in one collection | pin `slug:` on one entry or split collections |
| `overview_clash` | two index files map to the same collection overview | give one a `collection:` or exclude it |
| `target_missing` | the pass target does not exist | add it to `structure:` and `gravity structure apply`, or fix `target` |
| `target_unapproved` | the pass needs a grant (verbatim, auto-accept, nucleus or non-public space) and the repository has none for that space | `gravity approve <site/space>` with a user login, after the user agrees |
| `language_not_enabled` | the site lacks a requested language | see i18n.md |
| `translations_disabled` | org translations gate off: nothing will translate | see i18n.md |
| `empty_source` | file has no content; skipped | write content, exclude it, or `allowEmpty: true` |
| `package_title` | title came from a package name and was humanized | set `title:` in front matter or the files entry if the result is wrong |
| `structure_source_unmapped` | a structure page has `source:` no verbatim pass maps | add the file to a verbatim pass (same slug/collection) or drop `source:` |
| `structure_depth` | collections nested deeper than 5 | flatten |

If `.data.server` is `unavailable`, the server could not validate (older server or not connected); only
local rules ran. Say so to the user.

## Run preflight refusals

- `branch_behind`: run the printed `git pull --ff-only` (with the user's consent), then retry.
- `branch_diverged`: local and upstream both have commits; the user decides (rebase or merge). Never
  force-push on their behalf.
- `worktree_dirty`: commit or stash tracked changes (untracked files are ignored). A run reads committed
  history only. `--allow-dirty` is accepted only with `--dry-run`, and the dirty files are then left out.
- `pass_unknown` / `pass_not_applicable` (exit `2`): `--pass` names no pass, or a pass the plan skips
  (`disabled`, `trigger_mismatch` or `branch_mismatch`); the message says what to change.
- `send_mismatch`: HEAD or `.gravity.yaml` changed since the dry run; dry-run again.

## Models and instructions

- `model_unavailable` (exit `2`, before anything starts): an AI pass's model is not enabled
  (`not_enabled`), has no provider key (`no_key`) or the model catalog is unreachable
  (`catalog_unavailable`). Ask the user to fix it in Gravity's AI settings, or pick another enabled
  model with `--model <id>`. `gravity show --model <id> --json` previews `passes[].model` first.
- Warning `model_not_recommended`: the model is rated not recommended for documentation; tell the user
  before passing `--yes`.
- `model_conflict`: the run pass already recorded another model; start a new run.
- `server_too_old`: the server has no instructions bundle; upgrade it or use gravity 1.1.
- `budget_exceeded` (exit `5`) / `no_provider_key` (exit `3`): the whole run halts and is finished as
  failed; nothing else to retry until the budget or provider is fixed in Gravity. The `budget_exceeded`
  message names the cap (this run, the month or a feature), with the limit, the spend and the reset
  date when the server sends them, and `--json` carries them in `error.details`. Tell the user to ask
  an organization admin to raise the cap in Gravity › AI usage. For `no_provider_key`, tell them to ask
  an admin to connect Origin or add a provider key in Gravity › Settings › AI. `gravity doctor` shows
  the connected providers and the active spend caps.
- `--debug` keeps each pass's instructions bundle and page drafts under `.gravity/runs/<runId>/` for
  diagnosis.

## Lease waits

`waiting for run <id> (<trigger> on <branch>, started <ago>) - timeout <t>`: another run holds the
branch lease. Report the holder (`gravity runs show <id> --json`). Options: wait, raise
`--lease-timeout`, or with the user's consent `gravity runs cancel <id>` (declines its held changes).
Error `lease_held` after the timeout means nothing ran.

## Runs that look stuck or unclear

- `gravity runs show <id> --json`: `.data.run.status`, per-pass `.data.passes[].status`, `progress`, `skipReason`, bundle
  link. This is the only source of truth for completion.
- `running` with progress moving: wait. No progress and the holder machine is gone: the lease expires
  (`.data.run.lease.expiresAt`); cancelling releases it at once. A run is finished only when its status is
  `succeeded`, `partial`, `failed` or `cancelled`; `abandoned` appears as a failure in `gravity runs`, so
  treat it as not finished.
- `skipped` with `first_run_manual`: CI skipped the first run of an AI pass; run it locally.
- `skipped` with `target_unapproved`: `gravity approve` lists the space and the reason; `gravity approve <site/space>`.
- `partial`: some passes failed; read each pass's `error`.

## Credentials

- Exit `4` / `unauthorized`: `gravity login` (profile token expired or revoked). In CI, the
  `GRAVITY_REPO_TOKEN` secret is missing or revoked: `gravity ci setup` re-mints and installs it.
- Exit `4` / `token_expired`: the token passed its expiry. A `gravity login` token lasts 90 days (renew it with
  `gravity login`); a repository token lasts 365 days by default (`gravity ci setup` prints the date and mints
  a replacement, or mint one in the app under CLI & tokens › Repo tokens, then update the CI secret
  `GRAVITY_REPO_TOKEN`). `gravity status` and every CI run warn 14 days ahead (`token_expiring`).
- `token_unresolved`: an env var holds `${{ ... }}` / `$VAR` literally; fix the CI secret wiring.
- `token_host_mismatch`: the manifest `apiUrl` differs from the host the profile was issued for; log in to
  that host explicitly if trusted.
- Several organizations: `gravity org list`, `gravity org use <slug>`.
- Exit `3` (`module_disabled`, `seat_limit`): license; ask an admin.
- Exit `3` with `ai_disabled`: AI is turned off for the organization; the run halts and is finished as
  failed. An organization admin turns it on in Gravity › AI usage.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/v1/install.sh | sh   # macOS / Linux
irm https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/v1/install.ps1 | iex        # Windows
brew install Grupo-Impulso-Digital/tap/gravity                                                          # macOS (a cask)
scoop bucket add impulso https://github.com/Grupo-Impulso-Digital/scoop-bucket; scoop install gravity
gravity version
```

The installer picks the newest release of the major that has an archive for the platform, verifies it
against the release's `checksums.txt` (only a warning when that file or a sha256 tool is missing) and
installs to `/usr/local/bin` when writable, else `~/.local/bin` (`GRAVITY_INSTALL_DIR` overrides). Agent skill:
`gravity agent install [--tool claude|cursor|codex|agents-md] [--global]`.
