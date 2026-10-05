---
description: Find a Gravity run's change request, check its status and open it for review
argument-hint: "[runId|latest]"
allowed-tools: Bash(gravity:*), Bash(jq:*)
---

Help the user review a Gravity run, following the `gravity` skill. Run: $ARGUMENTS (default: latest)

1. List recent runs if no id was given: `gravity runs --json` and pick the latest relevant one (confirm with the user if ambiguous).
2. `gravity runs show <runId> --json`: report `.data.run.status` (succeeded, partial, failed, running, cancelled, abandoned), each pass's status and skip reason, and the bundle (changes awaiting review). Do not call a run finished unless this says so.
3. Still running: report progress and offer `gravity runs --watch`; never cancel without the user's consent.
4. Skipped `target_unapproved`: `gravity approve` lists pending targets; approve only what the user confirms.
5. Get the link: `gravity review <runId> --json | jq -r .data.url` and give it to the user (on a terminal, `gravity review <runId>` opens it). Reviewing and accepting changes happens in the Gravity app.
