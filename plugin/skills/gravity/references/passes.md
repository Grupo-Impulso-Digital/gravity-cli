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

- `push`: on commits to the branches in `branches` (default: the default branch). The filter is also
  applied to manual runs: from another branch the pass is skipped (`branch_mismatch`), and `--pass` on it
  fails with `pass_not_applicable`. Release runs are exempt.
- `pr`: dry, on pull requests (comments, check).
- `release`: on tags (changelog).
- `schedule`: periodic CI runs.
- `manual`: `gravity run` outside CI. A manual run runs passes triggered on `manual`, `push` or
  `schedule`; `--pass <name>` runs a named pass whatever its triggers.

## What the AI reads

Each AI phase starts from a kickoff made of tagged data, not instructions (those come from Gravity's
instructions bundle):

- **Change summary**: the range (and, for a release, its tag and the previous one); commits with sha,
  subject, PR number, author and the first four lines of the body (60 at most); changed files with status
  and `+added -deleted` counts (200 at most; lockfiles, `node_modules`, `vendor`, `dist`, minified files
  and `code.exclude` never take part); the units touched, added or removed; the OpenAPI delta per document
  (added, removed, changed, breaking or not); removed or renamed symbols, best effort.
- **Guides plan**: candidate pages in the target with why each is one, pages reachable through units this
  repository contributes to, undocumented units, and open hints from other repositories.
- No diffs and no file contents. The model asks for them with read-only tools: `git_log`, `git_diff`,
  `git_show`, `list_files`, `read_file` (at the end of the range under review, or at a given `ref`),
  `grep`; and `read_page`, `search_docs`, `recall`, `inventory`, `list_target` for Gravity. Pages are
  written through `draft_*`, `check_page`, `raise_hint` and `submit_page`; plans, release notes, facts,
  claim findings and unit maps through `add_*` and `finish_*`.

Principles to repeat to the user:

- **Commit messages are hints; impact comes from code units.** `plan.Decide` decides from changed files
  and units whether a pass runs (changelog passes run on any commit); `passes.ComputeImpact` finds the
  affected pages from the units touched and the pages bound to them. Only when that yields nothing does
  the guides pass search the target for the first three commit subjects.
- **`gravity run` reads committed history only.** Uncommitted work is invisible to the AI. A real run
  refuses a dirty tree (tracked files, `worktree_dirty`); a dry run does too unless `--allow-dirty`,
  which ignores the dirty files.

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

A pass writes only into its `target` (`<site>/<space>[/<collection>...]`), and the target must exist
(`target_missing`).

Why grants exist: in CI the pass runs with the repository token, and anyone who can push could edit
`.gravity.yaml` to point it at any space. So the token may only write (and read) where a person allowed
it. The allowance is a **grant per site/space**: one grant covers every pass of the repository that
targets that space or any collection inside it.

When a grant is needed (`requiresApproval`, with `reasons`):

- `skips_review`: a verbatim pass (imports and translations go live, repo-locked) or a pass whose
  changes are auto-accepted (`publish: auto-if-trusted` into a space that trusts the repository and
  whose review policy needs no human);
- `nucleus`: a nucleus pass writes to the product memory directly;
- `private_space`: the target space is not public (private, unlisted, or inheriting private), so the
  token would gain read access to it.

Normal passes (guides, reference, changelog, check) that land as change requests in a public space
need **no grant**: humans review them anyway, and their target status is `ok`.

Grants are made automatically whenever someone with write access to the space acts with their own
login: `gravity setup` (it also asks one yes/no for anything left), creating or updating a pass in the
app, `gravity structure apply`, and a real `gravity run`. Otherwise the pass is skipped with
`target_unapproved` until a person runs `gravity approve <site/space>` (no arguments lists the waiting
spaces and the reason in plain words; a pass name also works). A repository token can never grant.
Never grant (or `--all`) without telling the user which spaces and why.
