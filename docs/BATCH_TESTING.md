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

Prasyarat: backend/frontend jalan, database target punya akses `edit`, dan admin adalah Team Leader semua group yang punya akses (atau gunakan akun leader).

```bash
DB_ID=<uuid-koneksi> TARGET_DB=<nama-db-target> ROWS=50000 CHUNK=5000 \
  bash scripts/chunked-batch.sh
```

Script membuat tabel `batch_bench`, migration batch, change request, approve sebagai admin, lalu menunggu worker. Output yang diharapkan:

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

# lanjutkan dari checkpoint
curl -b cookies.txt -H "X-CSRF-Token: $CSRF" -X POST \
  "$BASE_URL/databases/$DB_ID/migrations/$MID/batch/resume"
```

- **Stop batch yang sedang berjalan**: `POST /batch/cancel` hanya meminta worker berhenti di batas potongan. Tidak ada SQL down otomatis; migration menjadi `partial`.
- **Revert**: `POST /batch/revert` membuat **change request kind `revert`**. Setelah semua leader menyetujui, worker queue menjalankan SQL down per chunk dari yang terakhir. Hasil `batch_status=reverted` (semua) atau `partially_reverted` (sebagian); migration tetap `partial`.
- **Resume**: worker membaca `_dbmp.batch_runs.cursor_id` dan melanjutkan; tidak mengulang chunk yang sudah committed.

## 6. Observability

Setiap migration tetap tercatat di `migration_executions` dan `migration_jobs`. Mode chunked juga mengisi `migration_batch_total{status=success|partial|cancelled}`, `migration_batch_duration_seconds`, dan `migration_item_duration_seconds` per potongan, sehingga dashboard batch tetap terisi. `migration_batch_items_total` sekarang menghitung potongan (chunk), bukan migration.

## 7. Batasan yang diketahui

- Checkpoint disimpan di database target, bukan database platform. Ini dipilih agar checkpoint dan potongan data berada dalam satu transaction.
- Satu batch run aktif per migration; resume memakai `batch_run_id` yang sama.
- Cancel setelah batch sukses penuh belum ada endpoint khusus.
- Worker masih satu proses per replika; jangan scale backend lebih dari satu replika tanpa memindahkan lock.
