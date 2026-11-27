# Batch Migration Testing Guide

Mode batch yang berlaku sekarang adalah **satu migration data besar diproses per potongan row** (`batch_size`). Migration tetap harus lewat **change request + approval leader** dan dieksekusi oleh **worker queue**. Endpoint lama `POST /databases/:id/batches` (banyak migration sekaligus) sudah dinonaktifkan dan menjawab `410 Gone`.

## 1. Kontrak SQL batch

Isi `batch_size > 0` pada migration. SQL up dan down memakai placeholder angka:

```sql
-- up: satu potongan, wajib ORDER BY id
UPDATE big_table SET processed = true
WHERE id IN (
  SELECT id FROM big_table
  WHERE id > :last_id
  ORDER BY id
  LIMIT :batch_size
);

-- down: membalik satu potongan
UPDATE big_table SET processed = false
WHERE id >= :from_id AND id <= :to_id;
```

- SQL up harus `UPDATE`, `DELETE`, atau `WITH`, memakai `:last_id` + `:batch_size`, dan `ORDER BY id`. Platform menambahkan `RETURNING id` bila belum ada.
- SQL down wajib dan memakai `:from_id` + `:to_id`.
- Migration batch tidak boleh `irreversible`.
- Setiap potongan punya transaction sendiri. Checkpoint `(execution_id, run_id, chunk_no, from_id, to_id, cursor)` dan data ditulis dalam **transaction yang sama** di database target (`_dbmp.batch_runs`, `_dbmp.batch_chunks`).
- Approval + queue tetap wajib; tidak ada jalur eksekusi langsung.

## 2. Uji end-to-end lewat API

Prasyarat: backend/frontend jalan, database target punya akses `edit`, dan akun yang dipakai script adalah Team Leader dari **setiap** group yang punya akses (kalau ada satu group tanpa leader, pengajuan ditolak `409`).

```bash
ADMIN_USERNAME=<akun-leader> ADMIN_PASSWORD=<password> \
DB_ID=<uuid-koneksi> TARGET_DB=<nama-db-target> ROWS=50000 CHUNK=5000 \
  bash scripts/chunked-batch.sh
```

Script membuat tabel `batch_bench`, migration batch, change request, approve sebagai akun itu, lalu menunggu worker. Tabel dibuat sebagai user `postgres` karena worker migration memakai role koneksi target; script memindahkan ownership ke `PG_USER` (default `duren`) supaya chunk tidak kena `permission denied`. Kalau ada migration lain berstatus `partial` di database yang sama, queue sengaja menahan job baru: script berhenti dengan timeout dan menyebutkan penyebabnya. Output yang diharapkan:

```
status=success batch=success rows=50000 chunks=10
Selesai: 50000 row, 10 chunk, ...s, row_salah=0, checkpoint_chunk=10
```

## 3. Uji dataset besar (baseline vs potongan)

```bash
ROWS=1000000 CHUNK=10000 bash scripts/dataset-batch.sh
```

Script ini mengukur baseline satu `UPDATE` penuh vs chunk manual di level database, lalu mencetak row, durasi, row/detik, jumlah chunk, dan row yang belum terupdate. Syarat spek: baseline > 1 detik; naikkan `ROWS` bila belum. Untuk membandingkan lewat API, gunakan `chunked-batch.sh` di atas.

Hasil uji 1 juta row pada environment ini:

| Jalur | Row | Durasi | Chunk |
|---|---:|---:|---:|
| Baseline satu UPDATE penuh (API script, tabel yang sama) | 1.000.000 | 2.826,5 ms | — |
| Batch via API (`chunked-batch.sh`, 10.000/chunk) | 1.000.000 | 6,44 s | 100 |
| Batch DB manual (`dataset-batch.sh`, 10.000/chunk) | 1.000.000 | 14,8 s | 100 |

`row_salah=0` dan `checkpoint_chunk=100` pada uji API. Baseline > 1 detik, dataset sama (tabel `batch_bench`, payload teks).

## 4. Verifikasi checkpoint di database target

```sql
SELECT run_id, status, cursor_id, committed_rows, committed_chunks
FROM _dbmp.batch_runs ORDER BY updated_at DESC LIMIT 5;

SELECT run_id, chunk_no, from_id, to_id, rows, committed_at
FROM _dbmp.batch_chunks ORDER BY run_id, chunk_no;
```

`committed_rows` harus sama dengan jumlah row yang sudah diproses; `cursor_id` sama dengan `max(to_id)` chunk terakhir. Kalau worker mati, baris run tetap ada dan job baru bisa melanjutkan dari cursor.

## 5. Cancel dan resume

```bash
# hentikan di batas potongan, lalu SQL down dijalankan terbalik
curl -b cookies.txt -H "X-CSRF-Token: $CSRF" -X POST \
  "$BASE_URL/databases/$DB_ID/migrations/$MID/batch/cancel"

# ajukan lanjutan dari checkpoint (change request; perlu approval leader)
curl -b cookies.txt -H "X-CSRF-Token: $CSRF" -X POST \
  "$BASE_URL/databases/$DB_ID/migrations/$MID/batch/resume"
```

- **Stop batch yang sedang berjalan**: `POST /batch/cancel` hanya meminta worker berhenti di batas potongan. Tidak ada SQL down otomatis; migration menjadi `partial`.
- **Revert**: `POST /batch/revert` membuat **change request kind `revert`**. Setelah semua leader menyetujui, worker queue menjalankan SQL down per chunk dari yang terakhir. Hasil `batch_status=reverted` (semua) atau `partially_reverted` (sebagian); migration tetap `partial`.
- **Resume**: `POST /batch/resume` membuat **change request kind `up`** untuk migration `partial`; migration tetap `partial` selama review. Setelah semua leader menyetujui, worker membaca `_dbmp.batch_runs.cursor_id` dan melanjutkan; tidak mengulang chunk yang sudah committed.

## 6. Observability

Setiap migration tetap tercatat di `migration_executions` dan `migration_jobs`. Mode chunked juga mengisi `migration_batch_total{status=success|partial|cancelled}`, `migration_batch_duration_seconds`, dan `migration_item_duration_seconds` per potongan, sehingga dashboard batch tetap terisi. `migration_batch_items_total` sekarang menghitung potongan (chunk), bukan migration.

## 7. Batasan yang diketahui

- Checkpoint disimpan di database target, bukan database platform. Ini dipilih agar checkpoint dan potongan data berada dalam satu transaction.
- Satu batch run aktif per migration; resume memakai `batch_run_id` yang sama.
- Satu database fisik hanya boleh punya **satu koneksi** (unique `host+port+db_name`), karena lock "satu migration per database" di-key per koneksi. Mendaftarkan target yang sama dua kali dijawab `409`.
- Cancel hanya menghentikan worker di batas potongan; membalik chunk yang sudah committed tetap butuh `change request revert` terpisah.
- Lock memakai advisory lock di database platform dan worker mengambil job lewat queue, jadi beberapa replika backend berbagi lock yang sama. Yang harus sama adalah database platform dan `WORKER_COUNT` yang wajar.

## 8. Checklist uji

Jalankan otomatis (butuh PostgreSQL untuk test):

```bash
cd server && TEST_PG_DSN="host=localhost port=5432 user=postgres password=postgres dbname=web_backend_test sslmode=disable" \
  go test -p 1 ./internal/engine ./internal/database_handler ./internal/targetdb -count=1
```

`-p 1` penting: paket-paket ini memakai database test yang sama dan akan saling menghapus tabel bila jalan paralel.

| # | Skenario | Cara | Harapan | Bukti |
|---|---|---|---|---|
| 1 | Kontrak SQL batch divalidasi | Buat migration `batch_size>0` tanpa `:last_id`/`ORDER BY id` | `400` pada field `sql`; tidak bisa diajukan | `TestValidateDefinition` |
| 2 | Batch tidak boleh irreversible | `batch_size>0` + `irreversible=true` | `400` | `TestValidateDefinition` |
| 3 | Approval wajib sebelum eksekusi | Ajukan, lalu setujui oleh sebagian leader | Belum ada job, `job_id` kosong, migration tetap `pending_review`/`approved` | `TestIntegrationChangeRequestApprovalFlow`, `TestIntegrationBatchResumeRequiresApproval` |
| 4 | Eksekusi chunked sukses | `chunked-batch.sh` (50k/5k) | `status=success batch=success rows=50000 chunks=10 row_salah=0` | `TestIntegrationChunkedMigrationCheckpointsAndCommits` |
| 5 | Checkpoint di database target | Query `_dbmp.batch_runs` / `_dbmp.batch_chunks` | `committed_rows` = row diproses, `cursor_id` = `max(to_id)`, jumlah chunk konsisten | test #4 + §4 dokumen ini |
| 6 | Berhenti di batas potongan | `POST /batch/cancel` saat running | migration `partial`, batch `partial`, row hanya yang sudah committed | `TestIntegrationChunkedMigrationCancelRevertsCommittedChunks` |
| 7 | Resume dari checkpoint | `POST /batch/resume` lalu approve | Membuat change request `up`; setelah approve lanjut dari cursor, chunk lama tidak diulang | `TestIntegrationChunkedMigrationResumesFromTargetCheckpoint`, `TestIntegrationBatchResumeRequiresApproval` |
| 8 | Revert batch | `POST /batch/revert` lalu approve | SQL down per chunk dari terakhir; `reverted` (semua) atau `partially_reverted` | `TestIntegrationChunkedMigrationRevertAfterSuccess`, `TestIntegrationChunkedMigrationCancelRevertsCommittedChunks` |
| 9 | Satu migration per database | 3 job di DB A + 1 job di DB B | Span waktu A tidak tumpang tindih; B beririsan dengan A | `TestIntegrationOneMigrationPerDatabaseAndParallelAcrossDatabases` |
| 10 | Lock lepas saat worker mati | Hentikan worker di tengah job | Advisory lock lepas bersama sesi; job diambil worker lain | `TestIntegrationLockIsExclusiveAcrossProcessesAndReleasedOnCrash` |
| 11 | Satu database fisik satu koneksi | `POST /admin/connections` dua kali dengan host/port/db sama | `409` "this target database is already registered" | `TestIntegrationDuplicateTargetDatabase` |
| 12 | Baseline > 1 detik | `ROWS=1000000 CHUNK=10000 bash scripts/dataset-batch.sh` | Durasi baseline > 1 detik pada dataset yang sama | §3 dokumen ini |
| 13 | Metrik batch terisi | Scrape `/metrics` setelah satu batch | `migration_batch_total{status="success"}`, `migration_item_duration_seconds`, `migration_batch_duration_seconds` naik | manual, lihat §6 |
