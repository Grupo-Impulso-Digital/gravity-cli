---
description: Dry-run the Gravity passes, review the result with the user, then send it or adjust
argument-hint: "[--pass <name>]..."
allowed-tools: Bash(gravity:*), Bash(git status:*), Bash(git fetch:*), Bash(git log:*), Bash(jq:*), Read, Edit
---

Run Gravity for this repository, following the `gravity` skill. Arguments: $ARGUMENTS

1. Preconditions: `git status` clean for tracked files and the branch up to date with its upstream. If not, tell the user the exact git commands and wait; never force.
2. `gravity validate --json`; stop and fix errors first.
3. Plan and cost: `gravity show --json | jq '.data.passes[] | {name, kind, ai, estimate}'`. State which passes will run, which use AI, and the estimated cost. Get the user's go-ahead.
4. Dry run: `gravity run --dry-run $ARGUMENTS --yes --json` (or interactively on a terminal). On a preflight refusal (`branch_behind`, `branch_diverged`, `worktree_dirty`) relay the printed commands. On a lease wait, report the holder (`gravity runs show <id>`) and ask before cancelling anything.
5. Review the result with the user: `.data.pages` (op, slug, title, collection, language), per-pass warnings and findings, `.data.issues`, `.data.costUsd`, `.data.recording`.
6. Then either send it unchanged: `gravity run --send <runId> --json` (dry-run again on `send_mismatch`), or adjust `.gravity.yaml`, commit, and dry-run again.
7. After a real run, confirm completion with `gravity runs show <runId> --json` (`.data.run.status`, `.data.bundle.appUrl`) before saying it is done, and give the review link (`gravity review <runId> --json`).
