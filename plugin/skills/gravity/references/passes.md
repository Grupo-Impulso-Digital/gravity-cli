# Pass kinds

A pass is one job that keeps part of the docs current. Choose the kind by who is the source of truth.

## verbatim (no AI)

Imports repository Markdown/MDX as pages locked to the repository: the file is the source of truth and
the page cannot be edited in the app. Use for READMEs, `docs/` folders, ADRs, runbooks kept in git.

- Map files with `options.files` (globs, `collection`, `stripPrefix`, literal `slug`/`title`).
- Index files (`README.md`, `index.md`) become their folder's `overview` page.
- `guide.fr.md` or `lang: fr` front matter imports as a translation of the source page.
- Empty files are skipped with `empty_source` unless `allowEmpty: true`.
- An existing unlocked page with the same slug blocks the import (`slug_taken`); use `adopt: true` only
  if the user wants the repository to take it over, or rename the slug.
- Deleted or unmapped files produce delete proposals for their pages.

## reference (AI only with `prose: true`)

Deterministic API reference from `code.openapi` documents: one page set per document, `pageStrategy`
single / per-tag / per-operation. `prose: true` lets the AI write intro and usage prose around the
generated API blocks.

## guides (AI)

Task-oriented guides the AI writes and keeps current from code changes in its scope. Use for how-tos
and concept pages nobody maintains by hand. Tune with `instructions`, `scope.paths`, `maxPages`.

## changelog (AI)

Release notes per tag (`release` trigger) and an Unreleased page on `push`. `audience: customer` for
user-facing notes, `internal` for engineering notes. `source: changelog-file` reads an existing
CHANGELOG instead of commits.

## nucleus (AI)

Writes durable facts, decisions and glossary entries into the organization memory (Nucleus), not pages.

## check (AI unless `claims: false`)

The pull request gate (`gravity check` in CI): doc drift against API changes, coverage, claim checks,
verbatim conflicts. `failOn` decides what fails the job.

## capture (AI, Doc Agent)

Screenshots of UI routes through a configured connection; for apps with a UI.

## Triggers and branches

- `push`: on commits to the branches in `branches` (default: the default branch).
- `pr`: dry, on pull requests (comments, check).
- `release`: on tags (changelog).
- `schedule`: periodic CI runs.
- `manual`: `gravity run` outside CI. A manual run runs passes triggered on `manual`, `push` or
  `schedule`; `--pass <name>` runs a named pass whatever its triggers.

## First runs are local

An AI pass with no watermark has never run. CI (repository token) skips it with `first_run_manual`, so
a merge never launches a surprise full-history AI run. Do the first run with the user:

```bash
gravity run --dry-run --pass <name>     # plan view shows the estimate (commits, files, tokens, cost)
gravity run --send <runId>              # after reviewing the result
```

## Estimates

The plan view (and `gravity show`) shows per pass: AI or not, first run or not, commits and files in
range, approximate input tokens and cost (`null` when the server cannot estimate). Always state the
estimate to the user before running AI passes.

## Targets and approval

A pass writes only into its `target`. A target must exist (`target_missing`) and be approved for the
repository (`target_unapproved`). A signed-in user with write access is approved automatically on their
own runs; CI repository tokens need the approval recorded once: `gravity approve <pass>` (lists pending
targets without arguments).
