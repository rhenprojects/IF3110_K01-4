# Batch Manual Test — potongan row

Mode yang diuji: satu migration besar dijalankan per `batch_size` row, dengan approval leader dan worker queue.

## Prasyarat

1. Backend dan frontend jalan (`docker compose -f deploy/docker-compose.yml up -d --build`).
2. Admin/leader punya akses `edit` ke database target, dan admin (atau akun leader) adalah Team Leader dari **setiap** group yang punya akses ke database itu.
3. Buat tabel data:

```bash
docker exec if3110-db psql -U postgres -d <target_db> -c "
CREATE TABLE big_table (id bigint PRIMARY KEY, processed boolean NOT NULL DEFAULT false);
INSERT INTO big_table (id) SELECT g FROM generate_series(1, 50000) g;"
```

## Langkah

1. **Migration baru** di UI. Isi:
   - Nama: `batch-demo`
   - Batch size: `5000`
   - SQL up:
     ```sql
     UPDATE big_table SET processed = true
     WHERE id IN (SELECT id FROM big_table WHERE id > :last_id ORDER BY id LIMIT :batch_size);
     ```
   - SQL down:
     ```sql
     UPDATE big_table SET processed = false WHERE id >= :from_id AND id <= :to_id;
     ```
2. Simpan draft, lalu **Ajukan review**.
3. Login sebagai leader yang memenuhi syarat, setujui di halaman Review.
4. Buka detail migration. Panel **Batch per potongan row** menampilkan row, chunk, cursor, dan status.
5. Tunggu sampai status migration `success` dan batch `success`.
6. Verifikasi target:
   ```sql
   SELECT count(*) FROM big_table WHERE processed;
   SELECT status, cursor_id, committed_rows, committed_chunks FROM _dbmp.batch_runs;
   SELECT chunk_no, from_id, to_id, rows FROM _dbmp.batch_chunks ORDER BY chunk_no;
   ```
   Harapan: 50.000 row `processed`, 5 chunk, `committed_rows=50000`.

## Cancel dan resume

- Saat batch berjalan, klik **Hentikan batch**. Worker berhenti di batas potongan; migration menjadi `partial`.
- Untuk membatalkan committed chunk, klik **Ajukan revert batch**. Ini membuat change request `revert`; setelah leader menyetujui, worker menjalankan SQL down dari chunk terakhir. Statusnya `reverted`/`partially_reverted`.
- Setelah berhenti, klik **Lanjutkan dari checkpoint**. Worker melanjutkan dari cursor terakhir.
- Setelah batch sukses, **Ajukan revert batch** juga memakai approval + queue.

## Dataset besar

```bash
ROWS=1000000 CHUNK=10000 bash scripts/dataset-batch.sh
DB_ID=<uuid> TARGET_DB=<db> ROWS=50000 CHUNK=5000 bash scripts/chunked-batch.sh
```

Catat durasi baseline vs potongan, jumlah row/chunk, dan pastikan tidak ada row yang terlewat. Baseline harus > 1 detik agar sesuai syarat spek.
