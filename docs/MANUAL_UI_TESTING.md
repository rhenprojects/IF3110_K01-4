# Manual UI Test — semua fitur dari aplikasi

Panduan klik-per-klik untuk menguji seluruh fitur lewat SPA, dengan penekanan pada **migration per batch**. Tidak perlu curl atau SQL manual: data uji dibuat lewat migration biasa.

## Prasyarat

Yang **tidak** bisa dibuat dari aplikasi hanya database target dan akunnya (platform tidak mengelola pembuatan database). Siapkan sekali:

```bash
# contoh: database target untuk latihan, owner = role yang dipakai koneksi
docker exec if3110-db psql -U postgres -c "CREATE ROLE duren LOGIN PASSWORD 'duren';"
docker exec if3110-db psql -U postgres -c "CREATE DATABASE duren_manual OWNER duren;"
```

Sisanya dari SPA:

- Akun **Platform Admin** (default `admin` / `admin-password` dari compose).
- Akun **Team Leader** group (contoh `tokdalang`) beserta password-nya.
- Dua jendela browser: satu login admin (pembuat migration), satu login leader (approver). Ini menghindari logout berulang.

> **Penting:** backend berjalan di dalam Docker, jadi **Host koneksi harus `db`**, bukan `localhost`. `localhost` dari backend berarti container backend itu sendiri.

## Bab 1 — Koneksi database

1. Login `admin` → **Platform Admin → Koneksi Database → Tambah**.
2. Isi: Nama `duren-manual`, **Host `db`**, Port `5432`, Nama database `duren_manual`, Username `duren`, Password `duren`, SSL mode `disable`.
3. Klik **Test koneksi** → harus sukses dan menampilkan versi PostgreSQL.
4. **Simpan** → koneksi muncul di daftar.

Uji negatif:

| Isian | Harapan |
|---|---|
| Nama beda, host/port/database sama | `409` "this target database is already registered" |
| Host `localhost` lalu Test koneksi | gagal: cannot connect to target database |

## Bab 2 — Group dan akses

5. **Groups → Tambah**: nama `tim-manual`, leader `tokdalang`.
6. Tambahkan anggota `upin`, `ipin`, dan `admin`.
7. Beri akses `duren-manual` = **edit**.
8. Cek sebagai `admin`: daftar database menampilkan `duren-manual` dengan level **edit**.

## Bab 3 — Migration biasa (sekaligus menyiapkan data uji)

9. Buka `duren-manual` → **Migration baru**:
   - Nama: `1-seed-demo`
   - Batch size: `0`
   - SQL up: `CREATE TABLE demo_rows AS SELECT g::bigint AS id, false AS processed FROM generate_series(1, 100000) g;`
   - SQL down: `DROP TABLE demo_rows;`
10. **Ajukan review** → di jendela leader: **Review → Setujui**.
11. Di detail migration: stepper bergerak Draft → Review → Queue → Selesai; versi menjadi **v1**.

## Bab 4 — Bulk approve

12. Sebagai `admin`, buat dua migration kecil:
    - `2-marker-a` — up: `CREATE TABLE marker_a(id int);` down: `DROP TABLE marker_a;`
    - `3-marker-b` — up: `CREATE TABLE marker_b(id int);` down: `DROP TABLE marker_b;`
13. Ajukan review **keduanya**.
14. Di jendela leader, menu **Review** menampilkan dua baris dan tombol **"Setujui semua (2)"**. Buka modal, isi komentar opsional, lalu **Setujui**.
15. Harapan: toast "2 pengajuan disetujui"; halaman **Queue** menunjukkan dua job berjalan **berurutan** (satu database hanya satu migration pada satu waktu).

Uji negatif: setujui satu pengajuan lebih dulu dari tombol per baris, lalu gunakan "Setujui semua"; item yang sudah diputuskan dilaporkan gagal dan item lain tetap diproses.

## Bab 5 — Batch: jalan, cancel, resume, revert

Detail langkah batch (termasuk skenario cancel yang bisa diamati) ada di [`BATCH_MANUAL_TESTING.md`](BATCH_MANUAL_TESTING.md).

16. Migration baru `4-batch-demo`, **Batch size `1000`**.
    - SQL up (jeda 0,2 detik per potongan supaya tombol Hentikan sempat diklik):
      ```sql
      UPDATE demo_rows SET processed = true FROM (SELECT pg_sleep(0.2)) s
      WHERE id IN (SELECT id FROM demo_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size);
      ```
    - SQL down:
      ```sql
      UPDATE demo_rows SET processed = false WHERE id >= :from_id AND id <= :to_id;
      ```
17. **Ajukan review** → approve sebagai leader.
18. Buka detail migration. Panel **"Batch per potongan row"** muncul; `row`, `chunk`, dan `cursor` bertambah setiap potongan.
19. Klik **Hentikan batch** sekitar 5 detik setelah jalan.
    - Harapan: migration `partial`, batch `partial`, hanya potongan yang sudah committed yang terhitung.
20. Klik **"Ajukan lanjutkan dari checkpoint"** → approve sebagai leader.
    - Harapan: worker lanjut dari cursor terakhir, chunk yang sudah committed tidak diulang, akhirnya `success`.
21. Klik **"Ajukan revert batch"** → approve sebagai leader.
    - Harapan: SQL down dijalankan per chunk dari yang terakhir; `batch_status` menjadi `reverted` dan semua row kembali `processed = false`.

Uji negatif:

| Aksi | Harapan |
|---|---|
| Resume saat migration bukan `partial` | `409` |
| Resume ganda saat change request masih berjalan | `409` |
| Revert sebelum migration pernah jalan | `409` |
| SQL batch tanpa `:last_id`/`:batch_size`/`ORDER BY id` | `400` pada field `sql` |
| Batch size > 0 dan migration ditandai irreversible | `400` |
| Approve oleh user yang bukan leader | `403` |

## Bab 6 — Fitur lain

22. **Queue**: saat job berjalan, status `queued`/`running`; setelah selesai `success`/`failed`.
23. **Rollback dan forward** pada `3-marker-b` (harus migration aktif terakhir): Rollback → sukses, lalu **Forward**.
24. **Riwayat eksekusi**: centang **Hide rollback** → pasangan eksekusi yang dibatalkan beserta rollback-nya hilang, forward tetap tampil. Hilangkan centang → semua attempt terlihat.
25. **Akses**: login `upin` (bukan leader) → menu Review kosong dan tombol approve tidak tersedia.
26. **Review ganda**: ajukan review dua kali untuk migration yang sama → `409`.
27. **Immutable**: SQL migration yang sudah dieksekusi tidak bisa diubah (form edit tidak tersedia / `409`).

## Ringkasan hasil untuk laporan

| # | Fitur | Harapan | Hasil |
|---|---|---|---|
| 1 | Tambah koneksi + Test koneksi | sukses, versi PostgreSQL tampil | ☐ |
| 2 | Duplikat database target | `409` | ☐ |
| 3 | Group + leader + akses edit | user melihat database level edit | ☐ |
| 4 | Migration biasa + approval | `success`, versi berurutan | ☐ |
| 5 | Bulk approve 2 pengajuan | keduanya `approved`, 2 job antre | ☐ |
| 6 | Batch progress | row/chunk/cursor bertambah | ☐ |
| 7 | Hentikan batch | `partial` + checkpoint | ☐ |
| 8 | Resume dari checkpoint | lanjut tanpa mengulang chunk | ☐ |
| 9 | Revert batch | `reverted`, data kembali | ☐ |
| 10 | Rollback + forward | sukses, riwayat bertambah | ☐ |
| 11 | Hide rollback | pasangan E2/E3 tersembunyi | ☐ |
| 12 | Non-leader approve | `403` / tombol tidak ada | ☐ |
| 13 | Validasi SQL batch & irreversible | `400` | ☐ |
| 14 | Queue | job berurutan per database | ☐ |

## Kalau ada yang tidak sesuai

Lihat log backend dan job terakhir:

```bash
docker logs --tail 50 if3110-backend
docker exec if3110-db psql -U postgres -d web_backend_db \
  -c "select name,status,error,batch_status,batch_rows,batch_chunks from migrations order by created_at desc limit 5;"
```

Detail kontrak dan arti kode error ada di [`api-contract.md`](api-contract.md); panduan batch yang lebih teknis (script, checkpoint, metrik) ada di [`BATCH_MANUAL_TESTING.md`](BATCH_MANUAL_TESTING.md) dan [`BATCH_TESTING.md`](BATCH_TESTING.md).
