#!/usr/bin/env bash
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

if [[ -f "$here/.env" ]]; then
  set -a
  . "$here/.env"
  set +a
fi

model=${MODEL:-}
max_output=${MAX_OUTPUT:-}
if [[ -z "$model" ]]; then
  echo "usage: make eval MODEL=<id> [MAX_OUTPUT=<n>]  (run testdata/eval/setup.sh first)" >&2
  exit 2
fi
if [[ -z "${GRAVITY_API_URL:-}" ]]; then
  echo "eval: GRAVITY_API_URL is not set; run testdata/eval/setup.sh or export it" >&2
  exit 2
fi
if [[ -z "${GRAVITY_REPO_TOKEN:-}${GRAVITY_TOKEN:-}" ]]; then
  echo "eval: GRAVITY_REPO_TOKEN (or GRAVITY_TOKEN) is not set; run testdata/eval/setup.sh or export it" >&2
  exit 2
fi
if [[ -n "$max_output" && ! "$max_output" =~ ^[0-9]+$ ]]; then
  echo "eval: MAX_OUTPUT must be a number of tokens" >&2
  exit 2
fi

gravity=${GRAVITY:-$root/bin/gravity}
repo_dir=${EVAL_REPO:-$here/work/topics-api}
results=${EVAL_RESULTS:-$here/results}
remote=${EVAL_REMOTE:-https://github.com/gravity-eval/topics-api.git}

EVAL_TARGET=${EVAL_TARGET:-topics/guides} "$here/make-fixture.sh" -f "$repo_dir" "$remote" >/dev/null

slug=$(tr '[:upper:]' '[:lower:]' <<<"$model" | sed -E 's/[^a-z0-9.-]+/-/g; s/^-+//; s/-+$//')
stem="$slug${max_output:+-mo$max_output}-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$results"
log="$results/$stem.log"

args=(run --dry-run --yes --json --model "$model")
if [[ -n "$max_output" ]]; then
  args+=(--max-output "$max_output")
fi

start=$(date +%s)
set +e
"$gravity" -C "$repo_dir" "${args[@]}" >"$log.json" 2>"$log"
rc=$?
set -e
end=$(date +%s)

cd "$root"
set +e
go run ./internal/eval/cmd/score -repo "$repo_dir" -since "$start" -duration "$((end - start))" \
  -model "$model" -max-output "${max_output:-0}" -run-exit "$rc" -out "$results" -name "$stem"
score_rc=$?
set -e
echo "gravity run exited $rc; its output is in $log and $log.json"
exit "$score_rc"
