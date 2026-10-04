---
description: Draft, refine and apply the declared docs structure (structure plan / apply)
argument-hint: "[--site <slug>] [--repo <path>...]"
allowed-tools: Bash(gravity:*), Bash(jq:*), Read, Edit
---

Work on the `structure:` block of `.gravity.yaml` with the user, following the `gravity` skill. Arguments: $ARGUMENTS

1. Current state: `gravity structure show --json` (live tree with pages) and `gravity show` (declared tree).
2. Draft: `gravity structure plan $ARGUMENTS`. Present the tree and the diff (`+` create, `=` exists, `!` extra on the server).
3. Ask the user what to rename, move, drop, make private, and which pages are imported from repository files (`source:`). Write the draft with `gravity structure plan --write` or edit `structure:` directly.
4. `gravity validate --json`; fix `structure_source_unmapped`, `structure_depth`, `duplicate_slug` and target issues.
5. `gravity structure apply --dry-run --json`: report `created`, `conflicts`, `extra`, `deferred` (pages with `source:` are created by the verbatim import; that is expected).
6. After the user confirms: `gravity structure apply`. Report the result; `extra` items are never deleted, the user handles them in the app.

Never create sites, spaces, collections or pages one by one through MCP tools, and never create a stub page where a verbatim pass will import.
