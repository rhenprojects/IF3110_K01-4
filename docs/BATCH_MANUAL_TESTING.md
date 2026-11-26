# Batch Manual Test

| Fokus | Batch, checkpoint, audit, observability, security (Person 3) |
|---|---|
| Tidak termasuk | Execution engine/locking (Person 1), workflow/rollback/k6 (Person 2) |

## Prasyarat

Jalankan dari root repository:

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

Siapkan database target dan role-nya (lewati jika sudah ada). Contoh yang dipakai di dokumen ini: `duren_database` milik `duren`.

```bash
docker compose -f deploy/docker-compose.yml exec db psql -U postgres -c "CREATE ROLE duren LOGIN PASSWORD 'duren'"
docker compose -f deploy/docker-compose.yml exec db psql -U postgres -c "CREATE DATABASE duren_database OWNER duren"
```

Daftarkan lewat **Admin → Koneksi Database**:

| Field | Nilai |
|---|---|
| Host | `db` (nama service Docker, bukan `localhost`) |
| Port | `5432` |
| Nama database | `duren_database` |
| Username | `duren` |
| Password | `duren` |
| SSL mode | `disable` (Postgres compose tidak mengaktifkan TLS) |

Klik **Test koneksi** sampai muncul `Terhubung: PostgreSQL ...`, lalu **Simpan**. Saat edit, Test memakai isi form; password boleh dikosongkan dan backend memakai password tersimpan.

Beri akses level `edit` ke database tersebut lewat **Admin → Groups**, lalu login sebagai anggota group itu untuk membuat migration dan batch. Kalau database target berbeda, ganti `duren_database` pada perintah `psql` di bawah dengan `Nama database` koneksimu.

## 10 Migration

| # | Nama | SQL up | SQL down |
|---|---|---|---|
| 1 | `manual10-01-schema` | `CREATE SCHEMA batch_demo;` | `DROP SCHEMA batch_demo CASCADE;` |
| 2 | `manual10-02-customers` | `CREATE TABLE batch_demo.customers (id bigserial PRIMARY KEY, name text NOT NULL, email text NOT NULL UNIQUE);` | `DROP TABLE batch_demo.customers;` |
| 3 | `manual10-03-products` | `CREATE TABLE batch_demo.products (id bigserial PRIMARY KEY, sku text NOT NULL UNIQUE, name text NOT NULL, price numeric(12,2) NOT NULL CHECK (price >= 0));` | `DROP TABLE batch_demo.products;` |
| 4 | `manual10-04-orders` | `CREATE TABLE batch_demo.orders (id bigserial PRIMARY KEY, customer_id bigint NOT NULL REFERENCES batch_demo.customers(id), status text NOT NULL DEFAULT 'pending', created_at timestamptz NOT NULL DEFAULT now());` | `DROP TABLE batch_demo.orders;` |
| 5 | `manual10-05-order-items` | `CREATE TABLE batch_demo.order_items (order_id bigint NOT NULL REFERENCES batch_demo.orders(id) ON DELETE CASCADE, product_id bigint NOT NULL REFERENCES batch_demo.products(id), qty int NOT NULL CHECK (qty > 0), unit_price numeric(12,2) NOT NULL, PRIMARY KEY (order_id, product_id));` | `DROP TABLE batch_demo.order_items;` |
| 6 | `manual10-06-phone` | `ALTER TABLE batch_demo.customers ADD COLUMN phone text;` | `ALTER TABLE batch_demo.customers DROP COLUMN phone;` |
| 7 | `manual10-07-created-at` | `ALTER TABLE batch_demo.customers ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();` | `ALTER TABLE batch_demo.customers DROP COLUMN created_at;` |
| 8 | `manual10-08-index` | `CREATE INDEX idx_batch_demo_orders_customer ON batch_demo.orders (customer_id, created_at DESC);` | `DROP INDEX batch_demo.idx_batch_demo_orders_customer;` |
| 9 | `manual10-09-seed` | `INSERT INTO batch_demo.products (sku, name, price) VALUES ('SKU-001', 'Contoh A', 1000), ('SKU-002', 'Contoh B', 2000);` | `DELETE FROM batch_demo.products WHERE sku IN ('SKU-001', 'SKU-002');` |
| 10 | `manual10-10-view` | `CREATE VIEW batch_demo.v_order_totals AS SELECT o.id AS order_id, o.customer_id, COALESCE(SUM(i.qty * i.unit_price), 0)::numeric(12,2) AS total FROM batch_demo.orders o LEFT JOIN batch_demo.order_items i ON i.order_id = o.id GROUP BY o.id, o.customer_id;` | `DROP VIEW batch_demo.v_order_totals;` |

Form migration mewajibkan **SQL down** sebelum draft bisa disimpan. Kalau suatu migration memang tidak bisa dibatalkan, centang **irreversible** dan isi alasannya, bukan mengosongkan SQL down.

| Langkah | Aksi |
|---|---|
| 1 | Reset schema di database target: `docker compose -f deploy/docker-compose.yml exec db psql -U postgres -d duren_database -c "DROP SCHEMA IF EXISTS batch_demo CASCADE;"` |
| 2 | Buat 10 draft di **Databases → duren_database → Migrations → Migration baru**. Isi Nama migration, SQL up, dan SQL down sesuai tabel, lalu **Simpan draft**. |
| 3 | Centang 10 draft, klik **Jalankan terpilih (10)**. |
| 4 | Cek batch: `success`, 10/10, gagal 0, versi berurutan. |
| 5 | Cek schema di database target: `docker compose -f deploy/docker-compose.yml exec db psql -U postgres -d duren_database -c "SELECT table_name, table_type FROM information_schema.tables WHERE table_schema='batch_demo';"` |
| 6 | Cek versi di database platform: `docker compose -f deploy/docker-compose.yml exec db psql -U postgres -d web_backend_db -c "SELECT version, name, status FROM migrations WHERE name LIKE 'manual10-%' ORDER BY version;"` |

| Grafana / Prometheus | Query |
|---|---|
| Batch per status | `sum by (status) (rate(migration_batch_total[5m]))` |
| Item per status | `sum by (status) (rate(migration_batch_items_total[5m]))` |
| Item p50 | `histogram_quantile(0.50, sum by (le) (migration_item_duration_seconds_bucket))` |
| Item p95 | `histogram_quantile(0.95, sum by (le) (migration_item_duration_seconds_bucket))` |
| Item p99 | `histogram_quantile(0.99, sum by (le) (migration_item_duration_seconds_bucket))` |
| Batch p95 | `histogram_quantile(0.95, sum by (le) (migration_batch_duration_seconds_bucket))` |
| Gagal + partial | `sum(migration_batch_total{status="failed"}) + sum(migration_batch_total{status="partial"}) or vector(0)` |
| Catatan | Panel durasi memakai bucket kumulatif tanpa `rate`, jadi tetap terisi setelah batch selesai. Panel "selesai per status" memakai `rate([5m])`, jadi bisa 0 setelah idle; jalankan batch saat dashboard dibuka. |

## 200 Migration

| Mode | Perilaku |
|---|---|
| `all` (default) | Buat 200 draft + jalankan batch. |
| `migration` | Buat draft saja, simpan ID ke `IDS_FILE`. |
| `batch` | Jalankan batch dari `IDS_FILE`. |

| Variabel | Default | Keterangan |
|---|---|---|
| `BASE_URL` | `http://localhost:9000/api/v1` | Base API |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | `admin` / `admin-password` | Kredensial admin |
| `DB_ID` | database `edit` pertama | UUID koneksi target |
| `COUNT` | `200` | 1–200 |
| `IDS_FILE` | `/tmp/batch-200-ids.txt` | ID untuk mode `migration` / `batch` |
| `NAME_PREFIX` | `batch200` | Prefix nama migration |
| `SQL_TEMPLATE` | `SELECT {n}` | `{n}` diganti nomor urut |

| Tujuan | Perintah |
|---|---|
| Default migration + batch | `bash scripts/batch-200.sh` |
| Migration saja | `bash scripts/batch-200.sh --migration-only` |
| Batch dari file ID | `bash scripts/batch-200.sh --batch-only` |
| Target tertentu | `DB_ID=<uuid> bash scripts/batch-200.sh` |
| Reset metrik | `docker compose -f deploy/docker-compose.yml restart backend` |
| Cek metrik | `curl -sS http://localhost:9101/metrics` |

| Metrik | Target setelah 200 item |
|---|---|
| `migration_batch_total{status="success"}` | +1 |
| `migration_batch_items_total{status="success"}` | +200 |
| `migration_item_duration_seconds_count{status="success"}` | +200 |

| Catatan | |
|---|---|
| Riwayat | Permanen; gunakan database baru/reset volume untuk run bersih. |
| Counter | Reset saat backend restart; riwayat batch tetap di `migration_batches`. |
| Batas | Maksimal 200 migration per batch. |

## Security

| Otomatis | Cakupan |
|---|---|
| `bash scripts/security-batch.sh` | Auth, CSRF, IDOR, validasi batch, audit read-only, view-only, spoof execution ID |
| `bash scripts/invoker.sh test-all` | Unit, integration, API smoke |
| `bash scripts/codeql.sh` | SAST Go + TypeScript (butuh CodeQL CLI) |
| `.github/workflows/codeql.yml` | CodeQL otomatis push/PR/schedule |

| Uji | Harapan |
|---|---|
| `POST /databases/:id/batches` tanpa sesi | 401 |
| `POST` dengan sesi tanpa `X-CSRF-Token` | 403 |
| `POST` dengan sesi + CSRF | 202 |
| User `view`: `GET /batches` | 200 |
| User `view`: `POST /batches` | 403 |
| ID batch koneksi lain (IDOR) | 404 |
| Batch ID acak | 404 |
| Batch ID bukan UUID | 400/404, bukan 5xx |
| `migration_ids: []` | 400 |
| ID migration koneksi lain | 409 |
| Migration `success`/`failed` dijadikan batch | 409 |
| Lebih dari 200 ID | 400 |
| `execution_id` palsu di body | 202, ID server dipakai |
| `PUT`/`DELETE /executions` | 405 |
| `POST /migrations/:mid/change-requests` (`up`) non-draft | 409 |
| SQL error `SELECT 1/0` | 200, `status=failed`, rollback |
| Target tidak terjangkau | 502/504, status tetap `draft` |
| `GET /api/v1/metrics` | 404; metrik hanya di `:9101` |

## Troubleshooting

| Gejala | Aksi |
|---|---|
| `cannot connect to target database` saat Test | Pastikan Host `db` (bukan `localhost`) dan SSL `disable`; Postgres compose tidak mengaktifkan TLS. |
| `authentication to target database failed` | Role atau password salah; buat role di container `db` dan isi ulang password di form. |
| `target database does not exist` | Buat database target dulu (`CREATE DATABASE ...`), lalu Test lagi. |
| `tidak ada database dengan level edit` | Beri akses `edit` atau set `DB_ID`. |
| `400 at most 200 migrations per batch` | Pakai `COUNT=200` atau beberapa batch. |
| Batch `failed`/`partial` | Buka detail item, baca `error`, buat migration baru. |
| Panel Grafana kosong | Pakai `increase(...[30m])` atau jalankan batch saat dashboard dibuka. |
| Metrik kembali 0 | Normal setelah restart backend. |
| Ingin data bersih | Database target baru atau `docker compose -f deploy/docker-compose.yml down -v`. |