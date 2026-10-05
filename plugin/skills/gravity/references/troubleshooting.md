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
- `worktree_dirty`: commit or stash tracked changes. A real run reads committed history only.
  `--allow-dirty` is accepted only with `--dry-run`.
- `send_mismatch`: HEAD or `.gravity.yaml` changed since the dry run; dry-run again.

## Lease waits

`waiting for run <id> (<trigger> on <branch>, started <ago>) - timeout <t>`: another run holds the
branch lease. Report the holder (`gravity runs show <id> --json`). Options: wait, raise
`--lease-timeout`, or with the user's consent `gravity runs cancel <id>` (declines its held changes).
Error `lease_held` after the timeout means nothing ran.

## Runs that look stuck or unclear

- `gravity runs show <id> --json`: `.data.run.status`, per-pass `.data.passes[].status`, `progress`, `skipReason`, bundle
  link. This is the only source of truth for completion.
- `running` with progress moving: wait. No progress and the holder machine is gone: the lease expires
  (status `abandoned`); cancelling releases it at once.
- `skipped` with `first_run_manual`: CI skipped the first run of an AI pass; run it locally.
- `skipped` with `target_unapproved`: `gravity approve` lists the space and the reason; `gravity approve <site/space>`.
- `partial`: some passes failed; read each pass's `error`.

## Credentials

- Exit `4` / `unauthorized`: `gravity login` (profile token expired or revoked). In CI, the
  `GRAVITY_REPO_TOKEN` secret is missing or revoked: `gravity ci setup` re-mints and installs it.
- `token_unresolved`: an env var holds `${{ ... }}` / `$VAR` literally; fix the CI secret wiring.
- `token_host_mismatch`: the manifest `apiUrl` differs from the host the profile was issued for; log in to
  that host explicitly if trusted.
- Several organizations: `gravity org list`, `gravity org use <slug>`.
- Exit `3` (`module_disabled`, `seat_limit`): license; ask an admin.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.sh | sh   # macOS / Linux
irm https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.ps1 | iex        # Windows
brew install Grupo-Impulso-Digital/tap/gravity
scoop bucket add impulso https://github.com/Grupo-Impulso-Digital/scoop-bucket; scoop install gravity
gravity version
```

The installer picks the newest release of the major that has an archive for the platform. Agent skill:
`gravity agent install [--tool claude|cursor|codex|agents-md] [--global]`.
