#!/usr/bin/env bash
# Bandingkan satu UPDATE besar dengan UPDATE per potongan row.
# Checkpoint (cursor + committed rows) ditulis pada transaksi yang sama
# dengan potongan, seperti syarat spek dataset batch.
#
# Usage:
#   bash scripts/dataset-batch.sh
#   ROWS=2000000 CHUNK=10000 bash scripts/dataset-batch.sh
set -euo pipefail

DB="${DB:-dataset_bench}"
ROWS="${ROWS:-2000000}"
CHUNK="${CHUNK:-10000}"
PAYLOAD="${PAYLOAD:-50}"
PG_CONTAINER="${PG_CONTAINER:-if3110-db}"

command -v docker >/dev/null 2>&1 || {
	echo "docker tidak ditemukan" >&2
	exit 1
}

psql_db() { docker exec -i "$PG_CONTAINER" psql -U postgres -v ON_ERROR_STOP=1 -q -d "$DB" "$@"; }
psql_admin() { docker exec -i "$PG_CONTAINER" psql -U postgres -v ON_ERROR_STOP=1 -q -d postgres "$@"; }

exists=$(psql_admin -tAc "SELECT 1 FROM pg_database WHERE datname='$DB'")
if [ "$exists" != "1" ]; then
	psql_admin -c "CREATE DATABASE \"$DB\"" >/dev/null
fi

echo "== Reset tabel di $DB =="
psql_db -c "DROP TABLE IF EXISTS bench_rows, bench_checkpoint;" >/dev/null
psql_db -c "CREATE TABLE bench_rows(id bigint PRIMARY KEY, payload text NOT NULL);" >/dev/null

echo "== Seed $ROWS row =="
psql_db -c "INSERT INTO bench_rows SELECT g, repeat('x', $PAYLOAD) FROM generate_series(1, $ROWS) g;" >/dev/null

echo "== Baseline: satu UPDATE =="
base_out=$(psql_db -tAc "EXPLAIN (ANALYZE, TIMING OFF, SUMMARY ON) UPDATE bench_rows SET payload = payload || 'y';")
base_ms=$(printf '%s\n' "$base_out" | sed -n 's/.*Execution Time: \([0-9.]*\) ms.*/\1/p' | tail -1)
[ -n "$base_ms" ] || {
	echo "gagal membaca Execution Time dari EXPLAIN ANALYZE" >&2
	exit 1
}

echo "== Reset payload untuk mode per potongan =="
psql_db -c "UPDATE bench_rows SET payload = repeat('x', $PAYLOAD);" >/dev/null

psql_db -c "CREATE TABLE bench_checkpoint(run text PRIMARY KEY, last_id bigint NOT NULL DEFAULT 0, committed_rows bigint NOT NULL DEFAULT 0, chunks bigint NOT NULL DEFAULT 0);
INSERT INTO bench_checkpoint(run) VALUES ('chunked');" >/dev/null

psql_db -c "CREATE OR REPLACE PROCEDURE bench_chunked(batch_size int) LANGUAGE plpgsql AS \$\$
DECLARE
  last_id bigint := 0;
  mx bigint;
  n bigint;
BEGIN
  LOOP
    WITH c AS (
      SELECT id FROM bench_rows WHERE id > last_id ORDER BY id LIMIT batch_size
    ), u AS (
      UPDATE bench_rows b SET payload = b.payload || 'y'
      FROM c WHERE b.id = c.id
      RETURNING b.id
    )
    SELECT count(*), coalesce(max(id), last_id) INTO n, mx FROM u;

    IF n = 0 THEN
      COMMIT;
      EXIT;
    END IF;

    -- checkpoint ikut transaksi potongan ini
    UPDATE bench_checkpoint
      SET last_id = mx, committed_rows = committed_rows + n, chunks = chunks + 1
      WHERE run = 'chunked';
    COMMIT;

    last_id := mx;
  END LOOP;
END \$\$;" >/dev/null

echo "== Mode per potongan (batch_size=$CHUNK) =="
t0=$(date +%s%N)
psql_db -c "CALL bench_chunked($CHUNK);" >/dev/null
t1=$(date +%s%N)
chunk_ms=$(((t1 - t0) / 1000000))

IFS='|' read -r chunk_rows chunks <<<"$(psql_db -tAc "SELECT committed_rows, chunks FROM bench_checkpoint WHERE run='chunked'")"
missed=$(psql_db -tAc "SELECT count(*) FROM bench_rows WHERE payload NOT LIKE '%y'")

echo
printf '%-10s %12s %14s %12s\n' "mode" "rows" "duration_ms" "rows/s"
awk -v m=baseline -v r="$ROWS" -v d="$base_ms" 'BEGIN{printf "%-10s %12d %14.1f %12.0f\n", m, r, d, r/(d/1000)}'
awk -v m=chunked -v r="$chunk_rows" -v d="$chunk_ms" 'BEGIN{printf "%-10s %12d %14.1f %12.0f\n", m, r, d, r/(d/1000)}'
echo
echo "chunks=$chunks rows_belum_terupdate=$missed"
if awk -v d="$base_ms" 'BEGIN{exit !(d < 1000)}'; then
	echo "catatan: baseline < 1 detik; naikkan ROWS supaya memenuhi syarat spek"
fi
