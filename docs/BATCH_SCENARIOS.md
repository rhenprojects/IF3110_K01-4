# Skenario Uji Batch — 73 kasus

Matriks uji untuk mode **migration per potongan row** (`batch_size > 0`). Setiap baris punya input konkret dan hasil yang diharapkan.

Legenda **Jenis**: `A` = otomatis (bisa dijadikan test Go), `M` = manual (UI/psql). Kolom **Input** merujuk ke bank SQL di bawah.

## Bank SQL

**S1 — seed 100.000 row** (migration `batch_size = 0`, SQL up):

```sql
CREATE TABLE demo_rows AS
SELECT g::bigint AS id, false AS processed
FROM generate_series(1, 100000) g;
```

SQL down: `DROP TABLE demo_rows;`

**S2 — batch up**:

```sql
UPDATE demo_rows SET processed = true
WHERE id IN (SELECT id FROM demo_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size);
```

**S3 — batch down** (wajib untuk semua batch):

```sql
UPDATE demo_rows SET processed = false
WHERE id >= :from_id AND id <= :to_id;
```

**S4 — batch lambat** (untuk uji cancel; 0,2 detik per chunk):

```sql
UPDATE demo_rows SET processed = true FROM (SELECT pg_sleep(0.2)) s
WHERE id IN (SELECT id FROM demo_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size);
```

**S5 — batch yang gagal di chunk kedua**:

```sql
-- seed: CREATE TABLE fail_rows(id bigint PRIMARY KEY, val int NOT NULL CHECK (val >= 0));
--       INSERT INTO fail_rows SELECT g, 0 FROM generate_series(1, 5000) g;
UPDATE fail_rows SET val = CASE WHEN id > 2000 THEN -1 ELSE 1 END
WHERE id IN (SELECT id FROM fail_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size);
```

**S6 — batch dengan CTE**:

```sql
WITH picked AS (
  SELECT id FROM demo_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size
)
UPDATE demo_rows d SET processed = true FROM picked p WHERE d.id = p.id;
```

**S7 — batch dengan join/FROM**:

```sql
UPDATE demo_rows d SET processed = true
FROM (SELECT id FROM demo_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size) p
WHERE d.id = p.id;
```

**S8 — id tidak kontinu** (seed): `SELECT (g * 10)::bigint AS id, false AS processed FROM generate_series(1, 5000) g`

**S9 — id mulai 0 dan negatif** (seed): `SELECT (g - 10)::bigint AS id, false AS processed FROM generate_series(1, 30) g`

**S10 — tabel tanpa kolom id**: `CREATE TABLE no_id(kunci int);` + batch SQL S2 versi `no_id`

**S11 — RETURNING bukan bigint**:

```sql
UPDATE demo_rows SET processed = true
WHERE id IN (SELECT id FROM demo_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size)
RETURNING processed;
```

**S12 — reset data**: `UPDATE demo_rows SET processed = false;`

## A. Validasi definisi

| ID | Skenario | Input | Harapan | Jenis |
|---|---|---|---|---|
| B01 | Batch size 0 | migration batch size `0` | Jalur migration biasa, tanpa checkpoint | A |
| B02 | Batch size negatif | `-1` | `400` field `batch_size` | A |
| B03 | Batch size di atas batas | `1000001` | `400` field `batch_size` | A |
| B04 | Batch size batas atas | `1000000` | Diterima | A |
| B05 | Batch + irreversible | batch size `1000` + centang irreversible | `400` field `batch_size` | A |
| B06 | Tanpa `:last_id` | S2 dihapus `:last_id` | `400` field `sql` | A |
| B07 | Tanpa `:batch_size` | S2 dihapus `:batch_size` | `400` field `sql` | A |
| B08 | Tanpa `ORDER BY` | S2 dihapus `ORDER BY` | `400` field `sql` | A |
| B09 | Bukan UPDATE/DELETE/WITH | `SELECT 1` | `400` field `sql` | A |
| B10 | Down tanpa placeholder | S3 dihapus `:from_id` | `400` field `sql_down` | A |
| B11 | Down kosong | SQL down dikosongkan | `400` field `sql_down` | A |
| B12 | `RETURNING` bukan bigint | S11 | Job `partial`, error "must return bigint id" | A |
| B13 | Tabel tanpa kolom `id` | S10 | Job `partial`, error saat chunk pertama | M |

## B. Izin dan approval

| ID | Skenario | Input | Harapan | Jenis |
|---|---|---|---|---|
| B14 | Viewer mengajukan batch | login `upin` dengan akses view saja | `403` | A |
| B15 | Tanpa akses mengajukan | akun di luar group | `403` | A |
| B16 | Non-leader approve (up) | `upin` klik Setujui | `403` | A |
| B17 | Non-leader approve (revert) | `upin` approve CR revert | `403` | A |
| B18 | Satu dari dua leader approve | group A setuju, group B belum | Tetap `pending_review`, belum ada job | A |
| B19 | Leader terakhir approve | semua group setuju | `approved` + job masuk queue | A |
| B20 | Leader menolak | klik Tolak | `rejected`, migration `up` kembali `draft` | A |
| B21 | Group tanpa leader | hapus leader group | Submit `409` | A |
| B22 | Approve dua kali | approve request yang sudah diputuskan | `409` | A |

## C. Eksekusi dan checkpoint

| ID | Skenario | Input | Harapan | Jenis |
|---|---|---|---|---|
| C01 | Chunk dengan sisa | 2.500 row, batch `1000` | 3 chunk: 1000/1000/500 | A |
| C02 | Data lebih kecil dari batch | 2.500 row, batch `5000` | 1 chunk | A |
| C03 | Tabel kosong | tabel 0 row, batch `1000` | `success`, rows 0, chunks 0 | A |
| C04 | Batch size 1 | 3 row, batch `1` | 3 chunk | A |
| C05 | Data besar | 100.000 row, batch `5000` (S1+S2) | 20 chunk, `batch_rows=100000` | M |
| C06 | Id tidak kontinu | S8, batch `1000` | Chunk memakai min/max id hasil query | M |
| C07 | Id 0 dan negatif | S9, batch `10` | Row dengan id <= 0 tidak pernah diproses (celah #1) | M |
| C08 | CTE batch | S6 | Sukses, jumlah chunk sama seperti S2 | M |
| C09 | Batch dengan join/FROM | S7 | Sukses, tiap chunk `batch_size` row | M |
| C10 | SQL sudah punya `RETURNING id` | S2 + `RETURNING id` | Sukses, tidak dobel `RETURNING` | M |
| C11 | Baris baru saat run berjalan | insert id > cursor di tengah run | Ikut diproses chunk berikutnya | M |
| C12 | Baris baru id <= cursor | insert id lebih kecil di tengah run | Tidak diproses | M |
| C13 | SQL mengubah kolom id | update `id = id + 1000000` | **Jangan dipakai**: cursor melompat | M |

## D. Kegagalan dan pemulihan

| ID | Skenario | Input | Harapan | Jenis |
|---|---|---|---|---|
| D01 | Gagal di chunk kedua | S5, batch `1000` | Chunk 1 tetap, chunk 2 rollback, status `partial`, cursor = akhir chunk 1 | A |
| D02 | Gagal di chunk pertama | S5 dengan constraint sejak id 1 | `partial`, rows 0, chunks 0 | A |
| D03 | Timeout per chunk | S4 + `MIGRATION_TIMEOUT_SECONDS=1` | `partial`, error timeout, chunk sebelumnya tetap | M |
| D04 | Worker mati setelah commit | hentikan backend saat run | Checkpoint target tetap, resume tidak mengulang chunk | A |
| D05 | Worker mati sebelum commit | hentikan backend di tengah chunk | Chunk itu rollback, diulang saat resume | A |
| D06 | Migration lain `partial` | jalankan migration kedua di DB sama | Job kedua ditahan queue | A |
| D07 | Retry attempt > 1 | job gagal sementara | `migration_retries_total` naik | A |
| D08 | Hasil commit tidak pasti | job `uncertain` setelah crash | Diperiksa lewat bukti `_dbmp.executions` sebelum retry | A |

## E. Cancel, resume, revert

| ID | Skenario | Input | Harapan | Jenis |
|---|---|---|---|---|
| E01 | Cancel saat running | S4 batch `1000`, klik **Hentikan batch** | `202`, berhenti di batas chunk, `partial` | A |
| E02 | Cancel dua kali | klik dua kali | Tetap `202`, idempoten | M |
| E03 | Cancel saat queued | klik sebelum worker mulai | `409` (belum ada checkpoint) | A |
| E04 | Cancel migration biasa | batch size `0` | `400` field `batch_size` | A |
| E05 | Resume saat bukan partial | migration `success` | `409` | A |
| E06 | Resume saat partial | S4 lalu cancel, klik **Ajukan lanjutkan** | `201` CR `up`, belum ada job | A |
| E07 | Resume ganda | klik dua kali sebelum approve | `409` | A |
| E08 | Resume setelah approve | approve CR resume | Lanjut dari cursor, chunk lama tidak diulang | A |
| E09 | Revert setelah success | S2 selesai, klik **Ajukan revert batch** | `201` CR `revert`; setelah approve `reverted` | A |
| E10 | Revert setelah partial | cancel lalu revert | `reverted` (semua) atau `partially_reverted` | A |
| E11 | Revert dua kali | klik dua kali | `409` | A |
| E12 | Resume setelah revert | revert penuh lalu resume | `409` | A |
| E13 | Cancel setelah success | klik Hentikan setelah sukses | `201` CR `revert` | A |
| E14 | Viewer cancel/revert | login akses view | `403` | A |
| E15 | Data setelah revert | S2 lalu revert | `processed` kembali `false`, versi dilepas | M |

## F. Konkurensi dan lock

| ID | Skenario | Input | Harapan | Jenis |
|---|---|---|---|---|
| F01 | Dua batch database sama | dua job batch | Job kedua menunggu, tidak tumpang tindih | A |
| F02 | Batch di dua database | dua koneksi berbeda | Berjalan paralel | A |
| F03 | Batch + migration biasa | satu DB | Serial | A |
| F04 | Bulk approve dua batch | `POST /change-requests/approve` dua id | Dua job antre berurutan | A |
| F05 | Dua koneksi database fisik sama | POST koneksi target sama | `409` "already registered" | A |

## G. Observability dan audit

| ID | Skenario | Input | Harapan | Jenis |
|---|---|---|---|---|
| G01 | Metrik batch | `curl :9101/metrics` setelah S2 | `migration_batch_total{status="success"}` naik | M |
| G02 | Metrik item = chunk | S2 100k/5000 | `migration_batch_items_total{status="success"}` = 20 | M |
| G03 | Waktu tunggu queue | jalankan setelah backend restart | `migration_queue_wait_seconds_count` bertambah | M |
| G04 | Job per status | `migration_jobs{status=...}` | `success` bertambah, `queued/running` kembali 0 | M |
| G05 | Audit approve | `GET /admin/audit` | Ada entri `POST .../approve` status 200 | M |

## H. UI

| ID | Skenario | Input | Harapan | Jenis |
|---|---|---|---|---|
| H01 | Penanda batch di daftar | migration batch size `5000` | Badge **Batch 5.000** di kolom nama | M |
| H02 | Panel batch | buka detail migration batch | Panel "Batch per potongan row": rows, chunk, cursor | M |
| H03 | Tombol Hentikan | batch `running` | Tombol muncul; hilang setelah cancel diminta | M |
| H04 | Tombol resume/revert | status `partial` / `success` | Resume hanya `partial`; revert pada `partial` dan `success` | M |
| H05 | Peringatan SQL | batch size > 0 tanpa `:last_id` | Peringatan muncul sebelum submit | M |

## Celah dan batasan yang diketahui

| # | Perilaku sekarang | Dampak | Kandidat perbaikan |
|---|---|---|---|
| 1 | `id <= 0` tidak pernah diproses (cursor mulai 0) | Data id 0/negatif terlewat | Dokumentasikan "id harus positif", atau cursor awal `MIN(id)-1` |
| 2 | Cancel saat job `queued` → `409` | Belum bisa membatalkan sebelum worker mulai | Cancel job queue |
| 3 | `RETURNING` non-bigint / tabel tanpa `id` | Gagal saat chunk pertama (`partial`) | Validasi awal saat submit |
| 4 | Batch `DELETE` diizinkan | Revert bergantung SQL down yang dirancang khusus | Peringatan di editor |
| 5 | Revert penuh **tidak** melepas slot aktif | Migration sebelumnya tetap tidak bisa di-rollback (`409`) | Anggap revert penuh sebagai pembatalan di `ActiveExecutions` |

## Menjalankan

```bash
# otomatis
cd server && TEST_PG_DSN="host=localhost port=5432 user=postgres password=postgres dbname=web_backend_test sslmode=disable" \
  go test -p 1 ./internal/engine ./internal/database_handler ./internal/targetdb

# manual cepat lewat API
DB_ID=<uuid> TARGET_DB=<db> ROWS=50000 CHUNK=5000 bash scripts/chunked-batch.sh

# dataset 1 juta row (baseline > 1 detik)
ROWS=1000000 CHUNK=10000 bash scripts/dataset-batch.sh
```
