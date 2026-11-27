#!/usr/bin/env bash
# Jalankan stress test k6 (bonus Milestone 2).
#
#   LEADER_USERNAME=tokdalang LEADER_PASSWORD=... DB_ID=<uuid> ./scripts/k6-stress.sh
#
# Memakai binary k6 kalau ada; kalau tidak, memakai image grafana/k6 di network
# Docker yang sama dengan backend.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
PASS_VARS=(
	BASE_URL EDITOR_USERNAME EDITOR_PASSWORD LEADER_USERNAME LEADER_PASSWORD
	DB_ID PREPARE JOB_SQL PEAK_VUS SUBMIT_VUS RAMP_UP HOLD RAMP_DOWN SUBMIT_DURATION
	READ_P95_MS SUBMIT_P95_MS ERROR_RATE DRAIN_TIMEOUT_MS
)

args=()
for name in "${PASS_VARS[@]}"; do
	if [ -n "${!name:-}" ]; then args+=(-e "$name=${!name}"); fi
done

if command -v k6 >/dev/null 2>&1; then
	exec k6 run "${args[@]}" -e "BASE_URL=${BASE_URL:-http://localhost:9000/api/v1}" "$DIR/k6-stress.js"
fi

command -v docker >/dev/null 2>&1 || {
	echo "k6 tidak ada dan docker tidak tersedia" >&2
	exit 1
}
NETWORK=${NETWORK:-$(docker inspect if3110-backend --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{end}}' 2>/dev/null || true)}
[ -n "$NETWORK" ] || {
	echo "tidak menemukan network container if3110-backend; set NETWORK=..." >&2
	exit 1
}
exec docker run --rm -i --network "$NETWORK" -v "$DIR:/scripts:ro" \
	"${args[@]}" -e "BASE_URL=${BASE_URL:-http://backend:9000/api/v1}" \
	grafana/k6:0.54.0 run /scripts/k6-stress.js
