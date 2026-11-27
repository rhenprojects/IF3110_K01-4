# Skenario Uji Batch — matriks

Daftar skenario khusus **migration per potongan row** (`batch_size > 0`). Kontrak SQL, checkpoint, dan metrik dijelaskan di [`BATCH_TESTING.md`](BATCH_TESTING.md); panduan klik di [`MANUAL_UI_TESTING.md`](MANUAL_UI_TESTING.md).

Legenda kolom **Jenis**: `A` = otomatis (bisa ditambah ke `server/internal/engine/integration_test.go` atau `database_handler`), `M` = manual (script/UI). Kolom **Bukti** adalah test yang sudah ada atau endpoint yang dipakai.

## A. Validasi definisi (sebelum dieksekusi)

| ID | Aksi | Harapan | Jenis | Bukti |
|---|---|---|---|---|
| B01 | `batch_size = 0` | Jalur migration biasa (bukan batch), tanpa checkpoint | A | `TestIntegrationMigrationLifecycle` |
| B02 | `batch_size = -1` atau `1000001` | `400`, field `batch_size` | A | `TestValidateDefinition` |
| B03 | `batch_size = 1000000` | Diterima (batas atas) | A | `TestValidateDefinition` |
| B04 | `batch_size > 0` + `irreversible = true` | `400`, field `batch_size` | A | `TestValidateDefinition` |
| B05 | SQL up tidak memakai `:last_id`, `:batch_size`, atau `ORDER BY` | `400`, field `sql` | A | `TestValidateDefinition` |
| B06 | SQL up diawali `SELECT`/`INSERT` | `400`, field `sql` (harus UPDATE/DELETE/WITH) | A | `TestValidateDefinition` |
| B07 | SQL down tanpa `:from_id`/`:to_id`, atau kosong | `400`, field `sql_down` | A | `TestValidateDefinition` |
| B08 | SQL up sudah punya `RETURNING kolom_lain` | Job gagal `batch SQL must return bigint id`, migration `partial` | A | `executeChunked` scan |
| B09 | Tabel target tanpa kolom `id`, atau `id` bukan bigint (mis. `uuid`) | Job gagal saat chunk (`must return bigint id`), migration `partial` | M | log backend |

## B. Izin dan approval

| ID | Aksi | Harapan | Jenis | Bukti |
|---|---|---|---|---|
| B10 | User `view` atau user tanpa akses mengajukan batch | `403` | A | `TestIntegrationChangeRequestApprovalFlow` |
| B11 | Approve (up maupun revert) oleh user bukan leader | `403` | A | `TestIntegrationBulkApproveChangeRequests` |
| B12 | Leader A setuju, leader B belum | Migration tetap `pending_review`, belum ada job | A | `TestIntegrationChangeRequestApprovalFlow` |
| B13 | Leader terakhir setuju | `approved` + job masuk queue | A | idem |
| B14 | Leader menolak | `rejected`; migration `up` kembali `draft` (batch tetap tersimpan) | A | `TestIntegrationChangeRequestReject` |
| B15 | Group pemilik akses belum punya leader | Submit `409` | A | `TestIntegrationChangeRequestRequiresLeaders` |

## C. Eksekusi normal

| ID | Data | Harapan | Jenis | Bukti |
|---|---|---|---|---|
| B16 | 2.500 row dengan batch 1.000 (sisa) atau 5.000 (lebih besar dari data) | 3 chunk (1.000/1.000/500) atau 1 chunk; `batch_rows=2500` | A | `TestIntegrationChunkedMigrationCheckpointsAndCommits` |
| B17 | Tabel kosong | `success`, `batch_rows=0`, `batch_chunks=0` | A | kode: `n == 0` |
| B18 | 3 row, batch 1 | 3 chunk, cursor naik 1 tiap chunk | A | variasi |
| B19 | id tidak kontinu (1, 10, 100, 1000), batch 2 | 2 chunk, `from_id`/`to_id` = min/max id hasil chunk | M | query `_dbmp.batch_chunks` |
| B20 | 1.000.000 row, batch 10.000 | 100 chunk, `row_salah=0`, baseline > 1 detik | M | `scripts/dataset-batch.sh` |
| B21 | UPDATE dengan subquery/`FROM` join | Sukses; setiap chunk memproses `batch_size` row | M | manual |
| B22 | Baris baru masuk saat run berjalan: id > cursor ikut diproses, id <= cursor tidak | Sesuai cakupan cursor (didokumentasikan) | M | manual |
| B23 | UPDATE mengubah kolom `id` | **Jangan dilakukan**: cursor bisa melompat/berulang; SQL harus memakai kolom id yang tidak berubah | M | aturan spek "batch by attribute" |

## D. Kegagalan di tengah proses

| ID | Aksi | Harapan | Jenis | Bukti |
|---|---|---|---|---|
| B24 | Error pada chunk ke-2 (mis. `1/0` untuk id > 1.000) | Chunk 1 tetap committed, chunk 2 rollback, migration `partial`, cursor = `max(to_id)` chunk 1 | A | `TestIntegrationBatchResumeRequiresApproval` (gate) |
| B25 | Error pada chunk pertama | `partial` dengan `batch_rows=0`, `batch_chunks=0`, `error` terisi | A | variasi |
| B26 | Statement/job timeout pada satu chunk | `partial` dengan `error` timeout; chunk sebelumnya tetap | M | log backend |
| B27 | Worker mati **setelah** commit chunk tapi sebelum mirror cursor | Checkpoint di target sudah commit; resume tidak mengulang chunk itu | A | `TestIntegrationChunkedMigrationResumesFromTargetCheckpoint` |
| B28 | Worker mati **sebelum** commit | Chunk itu rollback; resume mengulang chunk tersebut; hasil akhir tetap benar | A | idem |
| B29 | Ada migration lain `partial` di database yang sama | Job migration lain ditahan queue sampai resume/revert | A | `worker.claimSQL` |

## E. Cancel, resume, revert

| ID | Aksi | Harapan | Jenis | Bukti |
|---|---|---|---|---|
| B30 | Cancel saat `batch_status=running`, sekali atau dua kali | `202` `stop_requested` (idempoten); berhenti di batas chunk; migration `partial`; flag direset | A | `TestIntegrationChunkedMigrationCancelRevertsCommittedChunks` |
| B31 | Cancel saat job masih `queued` (belum mulai) | `409` (belum ada checkpoint untuk revert) — lihat "Celah" | A | perilaku sekarang |
| B32 | Cancel pada migration non-batch | `400`, field `batch_size` | A | handler |
| B33 | Resume saat status bukan `partial`, atau resume ganda selagi CR berjalan | `409` | A | `TestIntegrationBatchResumeRequiresApproval` |
| B34 | Resume pada `partial` | `201` change request `up`; **belum ada job** sampai leader approve | A | idem |
| B35 | Resume setelah disetujui | Lanjut dari `cursor_id`; chunk committed tidak diulang; akhirnya `success` | A | `TestIntegrationChunkedMigrationResumesFromTargetCheckpoint` |
| B36 | Revert setelah `success` atau `partial` | `201` CR `revert`; setelah approve SQL down per chunk dari terakhir; `reverted` atau `partially_reverted` | A | `TestIntegrationChunkedMigrationRevertAfterSuccess`, `...CancelRevertsCommittedChunks` |
| B37 | Revert dua kali, resume setelah revert, atau cancel setelah batch `success` | Dua yang pertama `409`; cancel setelah sukses membuat CR `revert` | A | handler `CancelBatchMigration` |

## F. Konkurensi dan lock

| ID | Aksi | Harapan | Jenis | Bukti |
|---|---|---|---|---|
| B38 | Dua batch, atau batch + migration biasa, di database yang sama | Job kedua menunggu; span waktu tidak tumpang tindih | A | `TestIntegrationOneMigrationPerDatabaseAndParallelAcrossDatabases` |
| B39 | Batch di dua database berbeda | Berjalan paralel | A | idem |
| B40 | Bulk approve dua pengajuan batch | Dua job antre, dieksekusi berurutan | A | `TestIntegrationBulkApproveChangeRequests` |
| B41 | Worker crash saat memegang lock | Advisory lock lepas bersama sesi; job lain lanjut | A | `TestIntegrationLockIsExclusiveAcrossProcessesAndReleasedOnCrash` |
| B42 | Dua koneksi ke database fisik yang sama | Ditolak `409` saat membuat koneksi (lock per koneksi) | A | `TestIntegrationDuplicateTargetDatabase` |

## G. Observability dan audit

| ID | Aksi | Harapan | Jenis | Bukti |
|---|---|---|---|---|
| B43 | Scrape `/metrics` setelah satu batch | `migration_batch_total{status="success"}`, `migration_batch_items_total` = jumlah chunk, `migration_item_duration_seconds_count` = jumlah chunk | M | `curl :9101/metrics` |
| B44 | Riwayat eksekusi dan audit log | Satu attempt per run (gagal/timeout tetap tercatat, append-only); submit/approve/reject tercatat di audit | A/M | `migration_executions`, `GET /admin/audit` |
| B45 | Checkpoint di target | `_dbmp.batch_runs`: `cursor_id`, `committed_rows`, `committed_chunks`; `_dbmp.batch_chunks`: `from_id`, `to_id`, `rows`, `down_sql` | A | `TestIntegrationChunkedMigrationCheckpointsAndCommits` |

## H. UI

| ID | Aksi | Harapan | Jenis | Bukti |
|---|---|---|---|---|
| B46 | Migration `batch_size = 0` | Panel batch tidak muncul | M | `MigrationDetail` |
| B47 | Batch berjalan, lalu cancel diminta | Panel menampilkan row/chunk/cursor dan tombol **Hentikan batch**; setelah cancel diminta tombol hilang dan muncul keterangan berhenti di batas potongan | M | `MigrationDetail` |
| B48 | Batch `partial` atau `success` | Muncul **Ajukan lanjutkan dari checkpoint** (partial) dan **Ajukan revert batch** (partial/success) | M | `MigrationDetail` |

## Celah dan batasan yang ditemukan

Ini bukan bug yang disembunyikan — sengaja dicatat supaya tidak dilaporkan sebagai kejutan saat demo:

| # | Perilaku sekarang | Dampak | Kandidat perbaikan |
|---|---|---|---|
| 1 | `id <= 0` tidak pernah diproses (cursor mulai 0, `WHERE id > :last_id`) | Data dengan id 0/negatif terlewat | dokumentasikan "id harus positif", atau cursor awal `MIN(id)-1` |
| 2 | Cancel saat job masih `queued` → `409` | Belum bisa membatalkan batch sebelum worker mulai | endpoint cancel job queue, atau perlakukan `queued` sebagai revert |
| 3 | `RETURNING kolom_lain` / tabel tanpa kolom `id` | Job gagal saat chunk pertama (`partial`) | validasi awal dengan `EXPLAIN`/cek kolom |
| 4 | Batch `DELETE` diizinkan | Revert hanya bisa mengembalikan cakupan id bila SQL down dirancang khusus; data bisa hilang | pesan peringatan di editor |
| 5 | Bulk approve lintas database | UI memanggil per database, jadi bukan satu request atomik | dokumentasi (tidak melanggar spek) |

## Cara menjalankan

```bash
# otomatis (butuh PostgreSQL untuk test)
cd server && TEST_PG_DSN="host=localhost port=5432 user=postgres password=postgres dbname=web_backend_test sslmode=disable" \
  go test -p 1 ./internal/engine ./internal/database_handler ./internal/targetdb -count=1

# skenario besar lewat API
DB_ID=<uuid> TARGET_DB=<db> ROWS=50000 CHUNK=5000 bash scripts/chunked-batch.sh

# baseline vs potongan 1 juta row
ROWS=1000000 CHUNK=10000 bash scripts/dataset-batch.sh
```

`-p 1` wajib: paket-paket itu memakai database test yang sama dan saling menghapus tabel bila berjalan paralel.
