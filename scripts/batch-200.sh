#!/usr/bin/env bash
# Seed COUNT draft migrations and execute them as one batch.
#
# Default: 200 migrations with SQL "SELECT <n>". No schema is changed, so this
# is safe for observing batch metrics in Prometheus/Grafana. Migration history
# is permanent; use a fresh database/target when you need a clean run.
#
# Usage:
#   bash scripts/batch-200.sh                  # default: migration + batch
#   bash scripts/batch-200.sh --migration-only # draft saja
#   bash scripts/batch-200.sh --batch-only     # batch dari IDS_FILE
#   COUNT=200 DB_ID=<uuid> bash scripts/batch-200.sh
#   BASE_URL=http://localhost:9000/api/v1 ADMIN_PASSWORD=secret bash scripts/batch-200.sh
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:9000/api/v1}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin-password}"
COUNT="${COUNT:-200}"
DB_ID="${DB_ID:-}"
NAME_PREFIX="${NAME_PREFIX:-batch200}"
SQL_TEMPLATE="${SQL_TEMPLATE:-SELECT {n}}"
APP_URL="${APP_URL:-http://localhost:5173}"
GRAFANA_URL="${GRAFANA_URL:-http://localhost:3000/d/batch-migrations}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://localhost:9090}"
METRICS_URL="${METRICS_URL:-http://localhost:9101/metrics}"
MODE="${MODE:-all}"
IDS_FILE="${IDS_FILE:-/tmp/batch-200-ids.txt}"

JAR=$(mktemp)
BODY=$(mktemp)
IDS=$(mktemp)
trap 'rm -f "$JAR" "$BODY" "$IDS"' EXIT

die() {
	echo "ERROR: $*" >&2
	exit 1
}

case "$COUNT" in
	'' | *[!0-9]*) die "COUNT harus berupa angka 1..200" ;;
esac
if [ "$COUNT" -lt 1 ] || [ "$COUNT" -gt 200 ]; then
	die "COUNT harus 1..200 (batas satu batch adalah 200 migration)"
fi

command -v curl >/dev/null 2>&1 || die "curl tidak ditemukan"
command -v python3 >/dev/null 2>&1 || die "python3 tidak ditemukan"

while [ $# -gt 0 ]; do
	case "$1" in
		--migration-only) MODE=migration ;;
		--batch-only) MODE=batch ;;
		-h | --help)
			sed -n '8,13p' "$0"
			exit 0
			;;
		*) die "argumen tidak dikenal: $1" ;;
	esac
	shift
done

case "$MODE" in
	all | migration | batch) ;;
	*) die "MODE harus all, migration, atau batch" ;;
esac

if [ "$MODE" = "batch" ] && [ ! -s "$IDS_FILE" ]; then
	die "MODE=batch membutuhkan IDS_FILE berisi ID migration: $IDS_FILE"
fi

jget() {
	python3 - "$1" "$BODY" <<'PY' 2>/dev/null || true
import json
import sys

key_path, body_path = sys.argv[1], sys.argv[2]
try:
    with open(body_path) as fh:
        value = json.load(fh)
except Exception:
    sys.exit(1)

for key in key_path.split("."):
    if value is None:
        break
    value = value[int(key)] if key.isdigit() else value.get(key)
print("" if value is None else value)
PY
}

csrf() {
	awk '$6 == "csrf_token" { print $7 }' "$JAR" | tail -n 1
}

call() {
	local method=$1 path=$2 data=${3:-}
	local token
	local args=()
	token=$(csrf)
	args=(-sS -o "$BODY" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$method" -H 'Content-Type: application/json')
	if [ -n "$token" ] && [ "$method" != "GET" ]; then
		args+=(-H "X-CSRF-Token: $token")
	fi
	if [ -n "$data" ]; then
		args+=(-d "$data")
	fi
	curl "${args[@]}" "$BASE_URL$path"
}

echo "== Login =="
LOGIN_PAYLOAD=$(python3 -c 'import json,sys; print(json.dumps({"username": sys.argv[1], "password": sys.argv[2]}))' "$ADMIN_USERNAME" "$ADMIN_PASSWORD")
STATUS=$(curl -sS -o "$BODY" -w '%{http_code}' -c "$JAR" \
	-H 'Content-Type: application/json' -d "$LOGIN_PAYLOAD" "$BASE_URL/users/login")
if [ "$STATUS" != "200" ]; then
	cat "$BODY" >&2
	die "login gagal (HTTP $STATUS)"
fi
[ -n "$(csrf)" ] || die "cookie csrf_token tidak ditemukan setelah login"

echo "== Cari database target =="
STATUS=$(call GET /databases)
if [ "$STATUS" != "200" ]; then
	cat "$BODY" >&2
	die "gagal membaca daftar database (HTTP $STATUS)"
fi

if [ -z "$DB_ID" ]; then
	DB_ID=$(python3 - "$BODY" <<'PY'
import json
import sys

data = json.load(open(sys.argv[1]))
for item in data.get("databases", []):
    if item.get("level") == "edit":
        print(item["id"])
        break
PY
)
fi
[ -n "$DB_ID" ] || die "tidak ada database dengan level edit; buat koneksi + akses edit dulu atau set DB_ID"

STATUS=$(call GET "/databases/$DB_ID")
if [ "$STATUS" != "200" ]; then
	cat "$BODY" >&2
	die "database $DB_ID tidak bisa diakses (HTTP $STATUS)"
fi
DB_NAME=$(jget name)
DB_LEVEL=$(jget level)
[ "$DB_LEVEL" = "edit" ] || die "database $DB_NAME hanya punya level '$DB_LEVEL', butuh 'edit'"
echo "Database: $DB_NAME ($DB_ID)"

if [ "$MODE" = "batch" ]; then
	cp "$IDS_FILE" "$IDS"
	echo
	echo "== Memakai $(wc -l <"$IDS" | tr -d ' ') migration dari $IDS_FILE =="
else
	echo
	echo "== Membuat $COUNT draft migration =="
	: >"$IDS"
	for i in $(seq 1 "$COUNT"); do
		name=$(printf '%s-%03d' "$NAME_PREFIX" "$i")
		sql=${SQL_TEMPLATE//\{n\}/$i}
		payload=$(python3 -c 'import json,sys; print(json.dumps({"name": sys.argv[1], "sql": sys.argv[2]}))' "$name" "$sql")
		STATUS=$(call POST "/databases/$DB_ID/migrations" "$payload")
		if [ "$STATUS" != "201" ]; then
			echo "Gagal membuat draft $name (HTTP $STATUS)" >&2
			cat "$BODY" >&2
			exit 1
		fi
		id=$(jget id)
		[ -n "$id" ] || die "response draft $name tidak berisi id"
		printf '%s\n' "$id" >>"$IDS"
		if [ $((i % 25)) -eq 0 ]; then
			printf '  %d/%d draft dibuat\n' "$i" "$COUNT"
		fi
	done
	echo "  selesai: $(wc -l <"$IDS" | tr -d ' ') draft"

	if [ "$MODE" = "migration" ]; then
		cp "$IDS" "$IDS_FILE"
		echo "IDs disimpan: $IDS_FILE"
		echo "Jalankan batch: MODE=batch DB_ID=$DB_ID IDS_FILE=$IDS_FILE bash scripts/batch-200.sh"
		exit 0
	fi
fi

echo
echo "== Menjalankan satu batch =="
BATCH_PAYLOAD=$(python3 -c 'import json,sys; ids=[line.strip() for line in open(sys.argv[1]) if line.strip()]; print(json.dumps({"migration_ids": ids}))' "$IDS")
STATUS=$(call POST "/databases/$DB_ID/batches" "$BATCH_PAYLOAD")
if [ "$STATUS" != "202" ]; then
	cat "$BODY" >&2
	die "gagal membuat batch (HTTP $STATUS)"
fi
BATCH_ID=$(jget id)
TOTAL=$(jget total)
TOTAL=${TOTAL:-$(wc -l <"$IDS" | tr -d ' ')}
[ -n "$BATCH_ID" ] || die "response batch tidak berisi id"
echo "Batch: $BATCH_ID ($TOTAL item)"

START=$(date +%s%N)
while :; do
	STATUS=$(call GET "/databases/$DB_ID/batches/$BATCH_ID")
	if [ "$STATUS" != "200" ]; then
		cat "$BODY" >&2
		die "gagal memantau batch (HTTP $STATUS)"
	fi
	BSTATUS=$(jget status)
	PROCESSED=$(jget processed)
	FAILED=$(jget failed)
	PROCESSED=${PROCESSED:-0}
	FAILED=${FAILED:-0}
	printf '\r  status=%-9s proses=%s/%s gagal=%s' "$BSTATUS" "$PROCESSED" "$TOTAL" "$FAILED"
	case "$BSTATUS" in
		success | partial | failed | cancelled) break ;;
	esac
	sleep 0.5
done
END=$(date +%s%N)
printf '\n'

ELAPSED=$(python3 -c "print(f'{($END - $START) / 1e9:.2f}')")
RATE=$(python3 -c "print(f'{$PROCESSED / max($ELAPSED, 0.001):.1f}')")

echo
echo "Batch ID : $BATCH_ID"
echo "Status   : $BSTATUS"
echo "Progres  : $PROCESSED sukses, $FAILED gagal, total $TOTAL"
echo "Durasi   : ${ELAPSED}s (${RATE} item/detik)"
echo "UI       : $APP_URL/databases/$DB_ID/migrations/batches/$BATCH_ID"
echo "Grafana  : $GRAFANA_URL"
echo "Prometheus: $PROMETHEUS_URL"

if curl -fsS --max-time 2 "$METRICS_URL" >/dev/null 2>&1; then
	echo
	echo "Metrik saat ini:"
	curl -sS "$METRICS_URL" | grep -E '^migration_(batch|item)' | sort || true
else
	echo
	echo "Metrik tidak tersedia di $METRICS_URL"
fi

echo
echo "PromQL yang berguna:"
echo "  sum by (status) (increase(migration_batch_total[30m]))"
echo "  sum by (status) (increase(migration_batch_items_total[30m]))"
echo "  histogram_quantile(0.95, sum by (le) (increase(migration_item_duration_seconds_bucket[30m])))"

[ "$BSTATUS" = "success" ] || exit 1
