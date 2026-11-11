# Batch Migration Testing Guide

Panduan ini menjelaskan cara menguji fitur batch migration (item 3): pembuatan batch dari beberapa draft, eksekusi berurutan dengan checkpoint, pembatalan di batas item, retry, audit trail, serta pengamatan metrik.

Untuk skenario manual 10 migration, invoker 200 migration, dan checklist keamanan lengkap, lihat [`BATCH_MANUAL_TESTING.md`](BATCH_MANUAL_TESTING.md).

Batasan yang diuji sengaja mencerminkan keputusan desain:

- Satu transaction per migration, bukan satu transaction untuk seluruh batch.
- Item yang gagal **tidak** menghentikan batch; batch berhenti hanya bila database target tidak terjangkau atau dibatalkan.
- Pembatalan hanya dicek di antara item. Item yang sedang berjalan diselesaikan lebih dulu.
- Kompensasi pembatalan bersifat pembukuan (`compensation_status='retained'`), bukan rollback DDL.

## 1. Prasyarat

Jalankan dari root repository:

```bash
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml exec db createdb -U postgres target_db   # database target untuk koneksi uji
```

Seed admin, koneksi, group, dan akses lewat UI atau API admin. Untuk script otomatis, minimal ada satu user admin dan satu database dengan level akses `edit`.

Variabel yang dipakai semua script:

| Variabel | Default | Keterangan |
|---|---|---|
| `BASE_URL` | `http://localhost:9000/api/v1` | Base URL API |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | `admin` / `admin-password` | Kredensial admin |
| `SEED` | `300` | Jumlah draft untuk uji throughput |
| `MIN_RATE` | `20` | Target minimal item/detik |

## 2. Uji keamanan otomatis

```bash
bash scripts/security-batch.sh
```

Script login sebagai admin, lalu memeriksa matriks berikut. Kolom "Harapan" adalah status HTTP.

| Skenario | Harapan |
|---|---|
| `POST /databases/:id/batches` tanpa sesi | 401 |
| `POST /databases/:id/batches` dengan sesi tapi tanpa `X-CSRF-Token` | 403 |
| `GET /databases/:id/batches/1 OR 1=1` (bukan UUID) | 400/404, bukan 5xx |
| `GET` batch UUID acak | 404 |
| `POST` dengan `migration_ids: []` | 400 |
| `POST` dengan ID migration yang bukan milik koneksi | 409 |
| `POST` dengan migration berstatus `success` | 409 `only draft migrations can be executed` |
| `PUT`/`DELETE /databases/:id/executions` | 405 |
| Body berisi `execution_id` palsu | 202, tetapi `execution_id` item diabaikan dan digenerate server |
| User level `view` membaca batch | 200 |
| User level `view` membuat batch | 403 |

Script membuat user/group uji dengan suffix timestamp dan melewati (SKIP) bagian itu bila pembuatannya gagal, misalnya karena sudah ada.

## 3. Uji throughput dan pembatalan

```bash
SEED=300 bash scripts/perf-batch.sh
```

Yang dilaporkan:

1. **Throughput** — jumlah item selesai dibagi durasi batch. Target `>= 20` item/detik pada mesin lokal.
2. **Latency cancel** — waktu sampai batch berstatus `cancelled` setelah `POST /cancel`.
3. **20 batch paralel** pada database yang sama — diharapkan tepat satu `202` dan sisanya `409` selama batch pertama masih memegang lock. Angka ini dilaporkan sebagai informasi karena batch bisa selesai sebelum request lain tiba.

Untuk membandingkan dengan eksekusi manual, jalankan `scripts/benchmark.sh` dan skenario k6 milik Person 2. Fokus batch adalah overhead per item yang konstan, bukan SQL yang lebih cepat.

## 4. Uji manual penting

### 4.1 Checkpoint dan resume setelah crash

```bash
# mulai batch berisi banyak draft, lalu hentikan backend di tengah jalan
docker kill if3110-backend
docker compose -f deploy/docker-compose.yml up -d backend
```

Setelah start, log harus memuat `Memulihkan N batch migration yang terhenti`. Item yang sedang berjalan menjadi `failed` dengan error `interrupted`, batch menjadi `partial` (bila sudah ada item sukses) atau `failed`. Item `pending` tetap `pending` sehingga bisa di-retry.

### 4.2 Retry

```bash
curl -sS -b cookies.txt -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d '{}' "$BASE_URL/databases/$DB_ID/batches/$BATCH_ID/retry"
```

Batch baru hanya berisi item `pending` dan `skipped` dari batch lama. `failed` tidak pernah dijalankan ulang; itu keputusan produk. Bila tidak ada kandidat, response `409 nothing to retry`.

### 4.3 IDOR antar koneksi

Ambil ID batch milik koneksi A, lalu panggil dengan ID koneksi B. Harus `404`, bukan `403`, supaya keberadaan batch tidak bocor.

### 4.4 Audit trail

`GET /databases/:id/executions?batch_id=...` mengembalikan item batch dengan `execution_id`, `attempt`, `status`, `duration_ms`, dan `compensation_status`. Endpoint ini hanya membaca: `PUT`/`DELETE` harus `405`.

## 5. Pemeriksaan SQL

```sql
-- tidak ada item yang tertinggal running
SELECT count(*) FROM migration_batch_items WHERE status = 'running';

-- distribusi status per batch
SELECT b.id, b.status, b.total, b.processed, b.failed,
       count(i.id) FILTER (WHERE i.status = 'failed') AS item_failed
FROM migration_batches b LEFT JOIN migration_batch_items i ON i.batch_id = b.id
GROUP BY b.id ORDER BY b.created_at DESC LIMIT 10;

-- checkpoint bergerak monoton
SELECT id, last_id, processed, failed, heartbeat_at FROM migration_batches ORDER BY created_at DESC LIMIT 5;

-- query cursor memakai index (batch_id, id)
EXPLAIN (ANALYZE, BUFFERS)
SELECT * FROM migration_batch_items
WHERE batch_id = '...' AND status = 'pending' AND id > 0
ORDER BY id LIMIT 100;

-- index benar-benar dipakai
SELECT relname, indexrelname, idx_scan FROM pg_stat_user_indexes
WHERE relname LIKE 'migration_batch%' ORDER BY idx_scan DESC;
```

## 6. Observability

Metrik hanya dilayani pada listener terpisah, aktif bila `METRICS_ADDR` diisi (default compose: `backend:9101`).

```bash
curl -sS http://localhost:9101/metrics | grep '^migration_'
```

| Metrik | Tipe | Label |
|---|---|---|
| `migration_batch_total` | counter | `status` |
| `migration_batch_items_total` | counter | `status` |
| `migration_item_duration_seconds` | histogram | `status` |
| `migration_batch_duration_seconds` | histogram | — |

Grafana: <http://localhost:3000> (admin/admin), dashboard **Batch Migrations**. Prometheus: <http://localhost:9090>.

## 7. Batasan yang diketahui

- Lock eksekusi masih `sync.Map` di dalam proses (milik Person 1). Dengan lebih dari satu instance backend, dua instance bisa menjalankan migration berbeda pada database target yang sama. Jangan scale backend lebih dari satu replika sampai lock dipindah ke database.
- AutoMigrate membuat primary key item sebagai `bigserial`, bukan `GENERATED ALWAYS AS IDENTITY`. Perilaku fungsional sama; kolom identitas bisa dirapikan lewat migration manual bila diperlukan.
- Pembatalan tidak membalik DDL yang sudah commit. `compensation_status` hanya menandai bahwa item selesai sebelum pembatalan dan tidak dikompensasi.
