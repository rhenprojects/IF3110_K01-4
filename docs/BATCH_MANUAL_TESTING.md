# Uji Manual — Migration per Batch

Panduan linear untuk membuktikan mode batch bekerja dari UI, termasuk cancel, resume, dan revert. Estimasi 15 menit.

Yang dibutuhkan:

- Aplikasi berjalan (`docker compose -f deploy/docker-compose.yml up -d`).
- Satu database target yang terdaftar sebagai koneksi (contoh `duren_database`, host `db`).
- Satu akun dengan akses **edit** (contoh `upin`) dan satu **Team Leader** (contoh `tokdalang`).
- Dua jendela browser: satu login `upin` (pembuat), satu login `tokdalang` (approver).

> **Aturan singkat:** pembuat migration harus punya akses `edit`; yang boleh menyetujui hanya leader dari **setiap** group yang punya akses ke database itu. Di contoh ini `tokdalang` adalah leader satu-satunya group.

---

## 1. Siapkan data uji lewat aplikasi

Tidak perlu SQL manual: data dibuat oleh migration biasa.

- [ ] Login `upin` → **Database** → pilih database target → tab **Migrations** → **Migration baru**.
- [ ] Isi:

| Field | Nilai |
|---|---|
| Nama migration | `1-seed-demo` |
| SQL | `CREATE TABLE demo_rows AS SELECT g::bigint AS id, false AS processed FROM generate_series(1, 50000) g;` |
| SQL down (rollback) | `DROP TABLE demo_rows;` |
| Batch size | `0` |

- [ ] **Simpan** → detail migration muncul dalam status `draft`.
- [ ] Klik **Ajukan review** → status `pending_review`.
- [ ] Login `tokdalang` → menu **Review** → **Setujui** → status menjadi `approved`, lalu job masuk queue.
- [ ] Tunggu sampai status `success` dan versi menjadi **v1**.

> Harapan: tabel `demo_rows` berisi 50.000 baris dengan `processed = false`.

---

## 2. Buat migration batch

- [ ] Login `upin` → **Migration baru**.
- [ ] Isi:

| Field | Nilai |
|---|---|
| Nama migration | `2-batch-demo` |
| Batch size | `1000` |
| SQL up | lihat blok di bawah |
| SQL down | lihat blok di bawah |

```sql
-- up: satu potongan per eksekusi, wajib :last_id, :batch_size, ORDER BY id
UPDATE demo_rows SET processed = true
WHERE id IN (
  SELECT id FROM demo_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size
);
```

```sql
-- down: membalik satu potongan, wajib :from_id dan :to_id
UPDATE demo_rows SET processed = false WHERE id >= :from_id AND id <= :to_id;
```

- [ ] **Ajukan review** (oleh `upin`).
- [ ] **Setujui** oleh `tokdalang`.

> Harapan: pada detail migration muncul panel **Batch per potongan row** dengan `batch status`, jumlah row, chunk, dan cursor. Dengan 50.000 row dan batch 1.000, hasil akhirnya **10 chunk**, `batch_rows = 50000`, `cursor = 50000`.

---

## 3. Amati progres

- [ ] Buka detail migration saat job berjalan.
- [ ] Angka `row` dan `chunk` bertambah setiap potongan; `cursor` sama dengan `id` terakhir yang sudah committed.
- [ ] Setelah selesai: status migration `success`, batch `success`.

> Kalau job selesai terlalu cepat untuk diamati, itu normal untuk 50.000 row. Gunakan skenario cancel di bagian berikut untuk melihat proses berhenti di tengah.

---

## 4. Hentikan di tengah, lanjutkan, lalu batalkan

Agar tombol **Hentikan batch** sempat diklik, buat batch yang lebih lambat.

- [ ] Buat migration baru `3-batch-lambat`, batch size `1000`, SQL up:

```sql
UPDATE demo_rows SET processed = true FROM (SELECT pg_sleep(0.2)) s
WHERE id IN (
  SELECT id FROM demo_rows WHERE id > :last_id ORDER BY id LIMIT :batch_size
);
```

- [ ] SQL down sama seperti sebelumnya, lalu ajukan review dan setujui sebagai `tokdalang`.
- [ ] Setelah beberapa detik, klik **Hentikan batch**.

| Yang dicek | Harapan |
|---|---|
| Status migration | `partial` |
| Status batch | `partial` |
| Tombol | **Hentikan batch** hilang, muncul keterangan berhenti di batas potongan |
| Row | hanya potongan yang sudah committed yang terhitung |

- [ ] Klik **Ajukan lanjutkan dari checkpoint** → membuat change request `up` (migration tetap `partial` selama review).
- [ ] Setujui sebagai `tokdalang` → worker lanjut dari cursor terakhir, **chunk yang sudah committed tidak diulang**.
- [ ] Tunggu sampai `success`.
- [ ] Klik **Ajukan revert batch** → change request `revert`.
- [ ] Setujui sebagai `tokdalang` → SQL down dijalankan per chunk dari yang terakhir.

| Yang dicek | Harapan |
|---|---|
| `batch_status` | `reverted` (semua chunk) atau `partially_reverted` (sebagian) |
| Kolom `processed` | kembali `false` untuk cakupan yang di-revert |
| Status migration | tetap `partial` setelah revert |

---

## 5. Verifikasi di database target

```sql
-- jumlah baris yang sudah diproses vs total
SELECT count(*) FILTER (WHERE processed) AS diproses, count(*) AS total FROM demo_rows;

-- checkpoint run terakhir
SELECT status, cursor_id, committed_rows, committed_chunks
FROM _dbmp.batch_runs ORDER BY updated_at DESC LIMIT 1;

-- potongan yang sudah committed
SELECT chunk_no, from_id, to_id, rows
FROM _dbmp.batch_chunks ORDER BY chunk_no;
```

| Kondisi | `cursor_id` | `committed_rows` | `committed_chunks` |
|---|---:|---:|---:|
| Selesai 50.000 row / 1.000 | 50000 | 50000 | 10 |
| Setelah cancel | = `id` terakhir yang committed | sesuai | sesuai |
| Setelah revert penuh | 0 | 0 | 0 |

---

## 6. Uji penolakan (harus gagal dengan rapi)

| Aksi | Hasil yang diharapkan | Di mana terlihat |
|---|---|---|
| Batch size `0` | Bukan mode batch; panel batch tidak muncul | Editor / detail |
| Batch size > 0, SQL tanpa `:last_id` | `400`, pesan pada field `sql` | Form submit |
| Batch size > 0, SQL tanpa `ORDER BY id` | `400`, pesan pada field `sql` | Form submit |
| Batch size > 0 + centang **tidak bisa dibatalkan** | `400`, pesan pada field `batch_size` | Form submit |
| SQL down kosong | `400`, pesan pada field `sql_down` | Form submit |
| Setujui sebagai `upin` (bukan leader) | `403` / tombol setujui tidak tersedia | Review |
| **Ajukan lanjutkan** saat status bukan `partial` | `409` | Tombol tidak muncul |
| Klik **Ajukan revert batch** dua kali | `409` (sudah ada pengajuan berjalan) | Toast / error |
| Buat koneksi ke host+port+database yang sama | `409` "this target database is already registered" | Admin → Koneksi |

---

## 7. Bukti untuk laporan

Simpan tangkapan layar / catatan untuk:

- [ ] Panel batch saat berjalan (`row`, `chunk`, `cursor`).
- [ ] Status `partial` setelah **Hentikan batch**.
- [ ] Status `success` setelah resume.
- [ ] `batch_status = reverted` setelah revert.
- [ ] Hasil tiga query verifikasi di bagian 5.
- [ ] Metrik: `curl -s http://localhost:9101/metrics | grep migration_batch` → `migration_batch_total{status="success"}`, `migration_batch_items_total`, `migration_item_duration_seconds_count`.

---

## 8. Kalau ada yang tidak sesuai

| Gejala | Penyebab umum | Perbaikan |
|---|---|---|
| Panel batch tidak muncul | Batch size masih `0` saat disimpan | Edit draft, isi batch size > 0, simpan ulang |
| Tombol **Setujui** tidak ada | Login bukan leader group pemilik akses | Login sebagai leader; cek Admin → Groups |
| `/databases` kosong untuk leader | Leader belum menjadi **anggota** group | Admin → Groups → tambah leader sebagai anggota |
| `permission denied for table demo_rows` | Tabel dibuat sebagai user `postgres`, worker memakai role koneksi | Buat tabel lewat migration (seperti langkah 1) atau `ALTER TABLE ... OWNER TO <user koneksi>` |
| Job tidak mau jalan setelah batch `partial` | Ada migration `partial` lain di database yang sama; queue sengaja menahan | Resume atau revert migration `partial` itu dulu |
| Koneksi gagal saat **Test koneksi** | Host diisi `localhost` padahal backend di Docker | Isi host `db` |

---

## Catatan

- Riwayat migration **tidak bisa dihapus** (append-only), jadi lakukan uji ini di database demo/bench, bukan database produksi.
- Panduan seluruh fitur (bukan hanya batch) ada di [`MANUAL_UI_TESTING.md`](MANUAL_UI_TESTING.md).
- Matriks 48 skenario batch ada di [`BATCH_SCENARIOS.md`](BATCH_SCENARIOS.md).
