#!/usr/bin/env bash
# Uji mode batch per potongan row lewat API penuh:
# draft -> change request -> approval leader -> queue worker -> checkpoint target.
# Harus jalan sebagai leader semua group yang punya akses ke database target,
# karena approve dan mode batch hanya boleh dilakukan leader.
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:9000/api/v1}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin-password}"
DB_ID="${DB_ID:-}"
TARGET_DB="${TARGET_DB:-}"
ROWS="${ROWS:-50000}"
CHUNK="${CHUNK:-5000}"
PG_CONTAINER="${PG_CONTAINER:-if3110-db}"
NAME="chunked-bench-$(date +%s)"

command -v curl >/dev/null || { echo "curl tidak ditemukan" >&2; exit 1; }
command -v python3 >/dev/null || { echo "python3 tidak ditemukan" >&2; exit 1; }

JAR=$(mktemp)
BODY=$(mktemp)
trap 'rm -f "$JAR" "$BODY"' EXIT
csrf() { awk '$6 == "csrf_token" { print $7 }' "$JAR" | tail -n 1; }
call() {
	local method=$1 path=$2 data=${3:-}
	local args=(-sS -o "$BODY" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$method" -H 'Content-Type: application/json')
	local token; token=$(csrf)
	if [ -n "$token" ] && [ "$method" != "GET" ]; then args+=(-H "X-CSRF-Token: $token"); fi
	if [ -n "$data" ]; then args+=(-d "$data"); fi
	curl "${args[@]}" "$BASE_URL$path"
}
jget() { python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));print(d.get(sys.argv[2],""))' "$BODY" "$1"; }

curl -sS -o /dev/null -c "$JAR" -X POST "$BASE_URL/users/login" -H 'Content-Type: application/json' \
	-d "{\"username\":\"$ADMIN_USERNAME\",\"password\":\"$ADMIN_PASSWORD\"}"

if [ -z "$DB_ID" ]; then
	call GET /databases >/dev/null
	DB_ID=$(python3 - "$BODY" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])).get("databases",[])
print(next((x["id"] for x in d if x.get("level")=="edit"), ""))
PY
)
fi
[ -n "$DB_ID" ] || { echo "tidak ada database edit; set DB_ID" >&2; exit 1; }

if [ -z "$TARGET_DB" ]; then
	call GET "/databases/$DB_ID" >/dev/null
	TARGET_DB=$(jget db_name)
fi
[ -n "$TARGET_DB" ] || { echo "TARGET_DB tidak diketahui" >&2; exit 1; }

echo "== Seed $ROWS row di $TARGET_DB =="
docker exec -i "$PG_CONTAINER" psql -U postgres -d "$TARGET_DB" -v ON_ERROR_STOP=1 -q <<SQL
DROP TABLE IF EXISTS batch_bench;
CREATE TABLE batch_bench (id bigint PRIMARY KEY, payload text NOT NULL);
-- Tabel dibuat oleh postgres, padahal worker migration jalan sebagai role koneksi (duren).
ALTER TABLE batch_bench OWNER TO ${PG_USER:-duren};
INSERT INTO batch_bench (id, payload) SELECT g, repeat('x', 50) FROM generate_series(1, $ROWS) g;
SQL

echo "== Baseline: satu UPDATE penuh =="
BASE_OUT=$(docker exec -i "$PG_CONTAINER" psql -U postgres -d "$TARGET_DB" -tAc "EXPLAIN (ANALYZE, TIMING OFF, SUMMARY ON) UPDATE batch_bench SET payload = payload || 'y';")
BASE_MS=$(printf '%s\n' "$BASE_OUT" | sed -n 's/.*Execution Time: \([0-9.]*\) ms.*/\1/p' | tail -1)
docker exec -i "$PG_CONTAINER" psql -U postgres -d "$TARGET_DB" -q -c "UPDATE batch_bench SET payload = repeat('x', 50);"

UP="UPDATE batch_bench SET payload = payload || 'y' WHERE id IN (SELECT id FROM batch_bench WHERE id > :last_id ORDER BY id LIMIT :batch_size)"
DOWN="UPDATE batch_bench SET payload = left(payload, length(payload) - 1) WHERE id >= :from_id AND id <= :to_id"
PAYLOAD=$(python3 -c 'import json,sys;print(json.dumps({"name":sys.argv[1],"sql":sys.argv[2],"sql_down":sys.argv[3],"batch_size":int(sys.argv[4])}))' "$NAME" "$UP" "$DOWN" "$CHUNK")

echo "== Buat migration batch =="
code=$(call POST "/databases/$DB_ID/migrations" "$PAYLOAD")
[ "$code" = "201" ] || { echo "gagal create: $(cat "$BODY")" >&2; exit 1; }
MID=$(jget id); echo "migration=$MID"

echo "== Ajukan review =="
code=$(call POST "/databases/$DB_ID/migrations/$MID/change-requests" '{"kind":"up"}')
[ "$code" = "201" ] || { echo "gagal submit: $(cat "$BODY")" >&2; exit 1; }
CRID=$(jget id)

echo "== Approve sebagai leader =="
code=$(call POST "/databases/$DB_ID/change-requests/$CRID/approve" '{"comment":"bench"}')
[ "$code" = "200" ] || { echo "gagal approve (pastikan admin leader semua group): $(cat "$BODY")" >&2; exit 1; }

START=$(date +%s%N)
DEADLINE=$((SECONDS + ${WAIT_TIMEOUT:-300}))
while :; do
	call GET "/databases/$DB_ID/migrations/$MID" >/dev/null
	status=$(jget status); batch=$(jget batch_status); rows=$(jget batch_rows); chunks=$(jget batch_chunks)
	printf '\r  status=%s batch=%s rows=%s chunks=%s' "$status" "$batch" "$rows" "$chunks"
	case "$status" in success|failed|partial) break ;; esac
	if [ "$SECONDS" -ge "$DEADLINE" ]; then
		echo
		echo "timeout ${WAIT_TIMEOUT:-300}s; job tidak jalan. Cek migration lain berstatus partial di database ini (memblokir queue):" >&2
		echo "  GET /databases/$DB_ID/migrations -> revert dulu yang partial" >&2
		exit 1
	fi
	sleep 0.3
done
END=$(date +%s%N)
ELAPSED=$(python3 -c "print(f'{($END-$START)/1e9:.2f}')")
echo
[ "$status" = "success" ] || { echo "batch tidak sukses: $(cat "$BODY")" >&2; exit 1; }
wrong=$(docker exec -i "$PG_CONTAINER" psql -U postgres -d "$TARGET_DB" -tAc "SELECT count(*) FROM batch_bench WHERE payload <> repeat('x', 50) || 'y'")
chk=$(docker exec -i "$PG_CONTAINER" psql -U postgres -d "$TARGET_DB" -tAc "SELECT count(*) FROM _dbmp.batch_chunks c JOIN _dbmp.batch_runs r ON r.run_id=c.run_id WHERE r.migration_id='$MID'")
echo "Selesai: $rows row, $chunks chunk, ${ELAPSED}s, row_salah=$wrong, checkpoint_chunk=$chk, baseline_ms=${BASE_MS}"
