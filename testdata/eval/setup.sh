#!/usr/bin/env bash
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

usage() {
  cat >&2 <<'EOF'
usage: setup.sh

Seeds a Gravity server for the model quality eval and writes testdata/eval/.env.

Required:
  GRAVITY_API_URL   the Gravity server, e.g. https://staging.gravitydocs.io
  GRAVITY_TOKEN     an API key (or CLI user token) of an admin of the eval organization
Optional:
  EVAL_ORG          organization slug to act in (X-Gravity-Organization); default: the token's
  EVAL_CREATE_ORG   1 creates EVAL_ORG as a new workspace when the token is not a member of it
  EVAL_REMOTE       origin URL of the fixture (default https://github.com/gravity-eval/topics-api.git)
  EVAL_SITE         site name (default Topics), created in English
  EVAL_SPACE        space name (default Guides)
  EVAL_PRODUCT      product created with the repository (default Topics API)
  EVAL_REPO         where the fixture is generated (default testdata/eval/work/topics-api)
  GRAVITY           the gravity binary (default bin/gravity, built when missing)
Needs: bash, curl, jq, git.
EOF
}

for v in GRAVITY_API_URL GRAVITY_TOKEN; do
  if [[ -z "${!v:-}" ]]; then
    echo "setup.sh: $v is required" >&2
    usage
    exit 2
  fi
done
for bin in curl jq git; do
  command -v "$bin" >/dev/null || { echo "setup.sh: $bin is required" >&2; exit 2; }
done

api=${GRAVITY_API_URL%/}
org=${EVAL_ORG:-}
remote=${EVAL_REMOTE:-https://github.com/gravity-eval/topics-api.git}
site_name=${EVAL_SITE:-Topics}
space_name=${EVAL_SPACE:-Guides}
product_name=${EVAL_PRODUCT:-Topics API}
repo_dir=${EVAL_REPO:-$here/work/topics-api}
gravity=${GRAVITY:-$root/bin/gravity}

rpc_id=0
mcp() {
  local name=$1 args=$2 headers=() body raw
  rpc_id=$((rpc_id + 1))
  headers=(-H "Authorization: Bearer $GRAVITY_TOKEN" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream")
  if [[ -n "$org" ]]; then
    headers+=(-H "X-Gravity-Organization: $org")
  fi
  body=$(jq -cn --arg name "$name" --argjson args "$args" --argjson id "$rpc_id" \
    '{jsonrpc: "2.0", id: $id, method: "tools/call", params: {name: $name, arguments: $args}}')
  raw=$(curl -sS --fail-with-body "${headers[@]}" -d "$body" "$api/api/mcp") || {
    echo "setup.sh: $name failed: $raw" >&2
    exit 1
  }
  if [[ "$raw" == event:* || "$raw" == data:* ]]; then
    raw=$(printf '%s\n' "$raw" | sed -n 's/^data: //p' | tail -n 1)
  fi
  if [[ $(jq -r '(.error != null) or (.result.isError == true)' <<<"$raw") == "true" ]]; then
    echo "setup.sh: $name refused: $(jq -c '.error // .result.content' <<<"$raw")" >&2
    exit 1
  fi
  jq -c '.result.structuredContent // (.result.content[0].text | fromjson? // .)' <<<"$raw"
}

remote_key() {
  local r=$1
  r=${r#*://}
  r=${r#*@}
  r=${r%.git}
  r=${r/://}
  local host=${r%%/*}
  printf '%s/%s' "$(tr '[:upper:]' '[:lower:]' <<<"$host")" "${r#*/}"
}

if [[ -n "$org" ]]; then
  orgs=$(mcp gravity_whoami '{}')
  if ! jq -e --arg o "$org" '[.. | objects | select(.slug? == $o or .id? == $o)] | length > 0' <<<"$orgs" >/dev/null; then
    if [[ "${EVAL_CREATE_ORG:-}" != "1" ]]; then
      echo "setup.sh: the token cannot act in organization $org; set EVAL_CREATE_ORG=1 to create it" >&2
      exit 1
    fi
    saved=$org
    org=""
    created=$(mcp onboarding_createWorkspace "$(jq -cn --arg n "$saved" '{name: $n}')")
    org=$(jq -r '.slug' <<<"$created")
    echo "created organization $org"
  fi
fi

sites=$(mcp sites_list '{}')
site_id=$(jq -r --arg n "$site_name" '[.items[] | select(.name == $n)][0].id // empty' <<<"$sites")
if [[ -z "$site_id" ]]; then
  site_id=$(mcp sites_create "$(jq -cn --arg n "$site_name" '{name: $n, description: "Model quality eval"}')" | jq -r '.id')
  echo "created site $site_name"
fi
mcp sites_config_set "$(jq -cn --arg id "$site_id" '{siteId: $id, settings: {i18n: {defaultLanguage: "en"}}}')" >/dev/null
site_slug=$(mcp sites_list '{}' | jq -r --arg id "$site_id" '.items[] | select(.id == $id) | .slug')

spaces=$(mcp spaces_list "$(jq -cn --arg id "$site_id" '{siteId: $id}')")
space_slug=$(jq -r --arg n "$space_name" '[.items[] | select(.name == $n)][0].slug // empty' <<<"$spaces")
if [[ -z "$space_slug" ]]; then
  space_slug=$(mcp spaces_create "$(jq -cn --arg n "$space_name" --arg id "$site_id" '{name: $n, siteId: $id}')" | jq -r '.slug')
  echo "created space $space_name"
fi
target="$site_slug/$space_slug"

key=$(remote_key "$remote")
repo_id=$(mcp repos_list '{}' | jq -r --arg k "$key" '[.items[] | select(.remoteKey == $k)][0].id // empty')
if [[ -z "$repo_id" ]]; then
  repo_id=$(mcp repos_register "$(jq -cn --arg r "$remote" --arg p "$product_name" '{remote: $r, name: "topics-api", newProductName: $p, defaultBranch: "main"}')" | jq -r '.id')
  echo "registered repository $key"
fi

org_header=()
if [[ -n "$org" ]]; then
  org_header=(-H "X-Gravity-Organization: $org")
fi
minted=$(curl -sS --fail-with-body -H "Authorization: Bearer $GRAVITY_TOKEN" -H "Content-Type: application/json" ${org_header[@]+"${org_header[@]}"} \
  -d '{"name":"model-eval","expiresInDays":null}' "$api/api/v1/repos/$repo_id/tokens") || {
  echo "setup.sh: minting the repository token failed: $minted" >&2
  exit 1
}
repo_token=$(jq -r '.token' <<<"$minted")

if [[ ! -x "$gravity" ]]; then
  make -C "$root" build >/dev/null
fi
EVAL_TARGET=$target "$here/make-fixture.sh" -f "$repo_dir" "$remote" >/dev/null
if ! GRAVITY_API_URL=$api GRAVITY_TOKEN=$GRAVITY_TOKEN GRAVITY_REPO_TOKEN='' "$gravity" -C "$repo_dir" approve --all >/dev/null 2>&1; then
  echo "setup.sh: could not approve $target for the repository; approve it in the app before the first eval" >&2
fi

env_file=$here/.env
umask 077
cat >"$env_file" <<EOF
GRAVITY_API_URL=$api
GRAVITY_REPO_TOKEN=$repo_token
EVAL_REMOTE=$remote
EVAL_TARGET=$target
EVAL_ORG=$org
EOF
echo "eval ready: site $site_slug, target $target, repository $key; settings in $env_file"
