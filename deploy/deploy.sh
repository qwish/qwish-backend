#!/usr/bin/env bash
set -euo pipefail
deploy_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
compose=(docker compose --env-file "$deploy_dir/deploy.env" -f "$deploy_dir/compose.yaml")

if [[ ! -f "$deploy_dir/deploy.env" || ! -f "$deploy_dir/api.env" ]]; then
  echo "Create deploy.env and api.env from the example files first." >&2
  exit 1
fi
if [[ $# -gt 1 ]]; then
  echo "Usage: bash deploy/deploy.sh [versioned-image]" >&2
  exit 1
fi
if [[ $# -eq 1 ]]; then
  export API_IMAGE=$1
fi
"${compose[@]}" config --quiet

previous_container=$("${compose[@]}" ps -q api)
if [[ -n "$previous_container" ]]; then
  umask 077
  docker inspect --format '{{.Config.Image}}' "$previous_container" > "$deploy_dir/previous-image"
fi
"${compose[@]}" pull
if ! "${compose[@]}" up -d --wait --wait-timeout 600; then
  echo "Deployment did not become healthy. Inspect docker compose logs before rollback." >&2
  echo "The previous image, if available, is recorded in deploy/previous-image." >&2
  exit 1
fi
echo "Deployment is healthy. Keep deploy.env aligned with the deployed image."
