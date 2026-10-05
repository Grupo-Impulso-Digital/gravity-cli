---
description: Set up Gravity docs for this repository with the user (login, setup, structure, validate, show)
argument-hint: "[product-slug]"
allowed-tools: Bash(gravity:*), Bash(git status:*), Bash(git branch:*), Bash(jq:*), Read, Edit
---

Set up Gravity documentation for this repository, following the `gravity` skill. Product hint: $ARGUMENTS

1. `gravity whoami --json`. Not signed in: `gravity login` (several orgs: `gravity org list`, ask which, `gravity org use <slug>`).
2. Preview without writing: `gravity setup --json` and summarize the detection, suggested product, site, structure and passes.
3. Ask the user, and wait for answers:
   - Audiences: public, users, developers, or organization audiences? What is private?
   - Which site (existing or new) and which spaces?
   - Which repository Markdown is the source of truth and must be imported verbatim (locked)?
   - What should the AI write and maintain (guides, changelog, API reference prose)?
4. Run `gravity setup` interactively if a terminal is available; otherwise `gravity setup --yes --product <slug> --site <slug> --no-run --no-ci`, then edit `.gravity.yaml` to match the answers (passes, targets, audiences, verbatim files).
5. `gravity structure plan`, review the tree with the user, refine `structure:` (add `source:` on imported pages; never create those pages yourself). Then `gravity structure apply --dry-run`, confirm, `gravity structure apply`.
6. `gravity validate --json` until `.data.errors == 0`, fixing each issue with its `hint`.
7. `gravity show` and walk the user through the tree, the verbatim mapping flags, the passes and effective languages.
8. Offer the next step: `/gravity:run` for the first dry run, then `gravity ci setup`.

Never build structure page-by-page through MCP tools. Never write tokens into files.
