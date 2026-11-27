#!/usr/bin/env bash
# DEPRECATED: mode batch multi-migration sudah dinonaktifkan.
# Throughput and cancellation test for batch migrations.
# Seeds draft migrations through the public API, runs one batch and reports
# processed items per second, then checks cancellation latency.
# Pembuat batch harus leader dari semua group yang punya akses ke database uji.
# Usage: SEED=300 ./scripts/perf-batch.sh
set -uo pipefail
echo "Script mode batch lama dinonaktifkan. Gunakan scripts/chunked-batch.sh." >&2
exit 2

BASE_URL="${BASE_URL:-http://localhost:9000/api/v1}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin-password}"
SEED="${SEED:-300}"
MIN_RATE="${MIN_RATE:-20}"

JAR=$(mktemp)
BODY=$(mktemp)
trap 'rm -f "$JAR" "$BODY"' EXIT

jget() {
	python3 -c '
import json, sys
data = json.load(sys.stdin)
for key in sys.argv[1].split("."):
    data = data[int(key)] if key.isdigit() else data.get(key)
print("" if data is None else data)
' "$1" 2>/dev/null
}
csrf() { awk '$6 == "csrf_token" { print $7 }' "$1" | tail -n 1; }
call() {
	local method=$1 path=$2 data=${3:-}
	local args=(-sS -o "$BODY" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$method" -H 'Content-Type: application/json')
	local token
	token=$(csrf "$JAR")
	if [ -n "$token" ] && [ "$method" != "GET" ]; then args+=(-H "X-CSRF-Token: $token"); fi
	if [ -n "$data" ]; then args+=(-d "$data"); fi
	curl "${args[@]}" "$BASE_URL$path"
}
seed_drafts() { # count sql label -> ID per line
	local count=$1 sql=$2 label=$3 i status
	for i in $(seq 1 "$count"); do
		status=$(call POST "/databases/$DB_ID/migrations" "{\"name\":\"perf-$label $i\",\"sql\":\"$sql\"}")
		if [ "$status" != "201" ]; then
			echo "Gagal membuat draft $label ke-$i (HTTP $status): $(cat "$BODY")" >&2
			return 1
		fi
		jget id <"$BODY"
	done
}

curl -sS -o /dev/null -c "$JAR" -H 'Content-Type: application/json' \
	-d "{\"username\":\"$ADMIN_USERNAME\",\"password\":\"$ADMIN_PASSWORD\"}" "$BASE_URL/users/login"

DB_ID="${DB_ID:-}"
if [ -z "$DB_ID" ]; then
	call GET /databases >/dev/null
	DB_ID=$(jget databases.0.id <"$BODY")
fi
if [ -z "$DB_ID" ]; then
	echo "Tidak ada database yang bisa diakses." >&2
	exit 2
fi
echo "Database uji: $DB_ID"

echo "== Menyiapkan $SEED draft =="
IDS=$(mktemp)
trap 'rm -f "$JAR" "$BODY" "$IDS"' EXIT
for i in $(seq 1 "$SEED"); do
	status=$(call POST "/databases/$DB_ID/migrations" "{\"name\":\"perf $i\",\"sql\":\"SELECT $i\"}")
	if [ "$status" = "201" ]; then
		jget id <"$BODY" >>"$IDS"
	else
		echo "Gagal membuat draft ke-$i (HTTP $status)" >&2
		head -c 200 "$BODY" >&2
		exit 2
	fi
done
echo "Draft siap: $(wc -l <"$IDS" | tr -d ' ')"

BATCH_IDS=$(python3 -c '
import json, sys
print(json.dumps({"migration_ids": [line.strip() for line in open(sys.argv[1]) if line.strip()]}))
' "$IDS")

echo "== Throughput batch penuh =="
START=$(date +%s%N)
STATUS=$(call POST "/databases/$DB_ID/batches" "$BATCH_IDS")
if [ "$STATUS" != "202" ]; then
	echo "Gagal membuat batch (HTTP $STATUS): $(cat "$BODY")" >&2
	exit 2
fi
BATCH_ID=$(jget id <"$BODY")
TOTAL=$(jget total <"$BODY")

while :; do
	call GET "/databases/$DB_ID/batches/$BATCH_ID" >/dev/null
	bstatus=$(jget status <"$BODY")
	processed=$(jget processed <"$BODY")
	printf '\r  status=%s %s/%s' "$bstatus" "$processed" "$TOTAL"
	case "$bstatus" in
	success | partial | failed | cancelled) break ;;
	esac
	sleep 0.5
done
END=$(date +%s%N)
ELAPSED=$(python3 -c "print(f'{($END-$START)/1e9:.2f}')")
RATE=$(python3 -c "print(f'{$processed/max($ELAPSED,0.001):.1f}')")
echo
echo "Batch $BATCH_ID: $processed item dalam ${ELAPSED}s ($RATE item/detik)"
python3 -c "raise SystemExit(0 if $RATE >= $MIN_RATE else 1)" ||
	echo "PERINGATAN: throughput di bawah target $MIN_RATE item/detik"

echo "== Latency cancel =="
CANCEL_FILE=$(mktemp)
seed_drafts 50 "SELECT pg_sleep(0.02)" cancel >"$CANCEL_FILE"
CANCEL_IDS=$(python3 -c '
import json, sys
ids = [line.strip() for line in open(sys.argv[1]) if line.strip()]
print(json.dumps({"migration_ids": ids}))
' "$CANCEL_FILE")
STATUS=$(call POST "/databases/$DB_ID/batches" "$CANCEL_IDS")
if [ "$STATUS" = "202" ]; then
	CB=$(jget id <"$BODY")
	sleep 0.2
	C0=$(date +%s%N)
	call POST "/databases/$DB_ID/batches/$CB/cancel" '{}' >/dev/null
	C1=$(date +%s%N)
	LAT=$(python3 -c "print(f'{($C1-$C0)/1e6:.0f}')")
	while :; do
		call GET "/databases/$DB_ID/batches/$CB" >/dev/null
		bstatus=$(jget status <"$BODY")
		[ "$bstatus" = "cancelled" ] && break
		sleep 0.3
	done
	echo "Cancel diterima dalam ${LAT} ms, batch berstatus cancelled"
else
	echo "Lewati uji cancel (HTTP $STATUS)"
fi
rm -f "$CANCEL_FILE"

echo "== 20 batch paralel pada database yang sama =="
PAR_FILE=$(mktemp)
seed_drafts 20 "SELECT pg_sleep(0.1)" par >"$PAR_FILE"
PAR_IDS=$(sed 's/.*/"&"/' "$PAR_FILE" | paste -sd, -)
TMP=$(mktemp -d)
for i in $(seq 1 20); do
	(
		code=$(curl -sS -o "$TMP/$i.body" -w '%{http_code}' -b "$JAR" \
			-H 'Content-Type: application/json' -H "X-CSRF-Token: $(csrf "$JAR")" \
			-d "{\"migration_ids\":[$PAR_IDS]}" "$BASE_URL/databases/$DB_ID/batches")
		echo "$code" >"$TMP/$i.code"
	) &
done
wait
echo "202: $(grep -l '^202$' "$TMP"/*.code | wc -l | tr -d ' ')  409: $(grep -l '^409$' "$TMP"/*.code | wc -l | tr -d ' ')  lainnya: $(grep -L -e '^202$' -e '^409$' "$TMP"/*.code | wc -l | tr -d ' ')"
for f in "$TMP"/*.code; do
	if [ "$(cat "$f")" = "202" ]; then
		id=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["id"])' "${f%.code}.body" 2>/dev/null)
		[ -n "$id" ] && call POST "/databases/$DB_ID/batches/$id/cancel" '{}' >/dev/null
	fi
done
rm -rf "$TMP"
rm -f "$PAR_FILE"
