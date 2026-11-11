# Batch Manual Test

| Fokus | Batch, checkpoint, audit, observability, security (Person 3) |
|---|---|
| Tidak termasuk | Execution engine/locking (Person 1), workflow/rollback/k6 (Person 2) |

## 10 Migration

| # | Nama | SQL |
|---|---|---|
| 1 | `manual10-01-schema` | `CREATE SCHEMA batch_demo;` |
| 2 | `manual10-02-customers` | `CREATE TABLE batch_demo.customers (id bigserial PRIMARY KEY, name text NOT NULL, email text NOT NULL UNIQUE);` |
| 3 | `manual10-03-products` | `CREATE TABLE batch_demo.products (id bigserial PRIMARY KEY, sku text NOT NULL UNIQUE, name text NOT NULL, price numeric(12,2) NOT NULL CHECK (price >= 0));` |
| 4 | `manual10-04-orders` | `CREATE TABLE batch_demo.orders (id bigserial PRIMARY KEY, customer_id bigint NOT NULL REFERENCES batch_demo.customers(id), status text NOT NULL DEFAULT 'pending', created_at timestamptz NOT NULL DEFAULT now());` |
| 5 | `manual10-05-order-items` | `CREATE TABLE batch_demo.order_items (order_id bigint NOT NULL REFERENCES batch_demo.orders(id) ON DELETE CASCADE, product_id bigint NOT NULL REFERENCES batch_demo.products(id), qty int NOT NULL CHECK (qty > 0), unit_price numeric(12,2) NOT NULL, PRIMARY KEY (order_id, product_id));` |
| 6 | `manual10-06-phone` | `ALTER TABLE batch_demo.customers ADD COLUMN phone text;` |
| 7 | `manual10-07-created-at` | `ALTER TABLE batch_demo.customers ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();` |
| 8 | `manual10-08-index` | `CREATE INDEX idx_batch_demo_orders_customer ON batch_demo.orders (customer_id, created_at DESC);` |
| 9 | `manual10-09-seed` | `INSERT INTO batch_demo.products (sku, name, price) VALUES ('SKU-001', 'Contoh A', 1000), ('SKU-002', 'Contoh B', 2000);` |
| 10 | `manual10-10-view` | `CREATE VIEW batch_demo.v_order_totals AS SELECT o.id AS order_id, o.customer_id, COALESCE(SUM(i.qty * i.unit_price), 0)::numeric(12,2) AS total FROM batch_demo.orders o LEFT JOIN batch_demo.order_items i ON i.order_id = o.id GROUP BY o.id, o.customer_id;` |

| Langkah | Aksi |
|---|---|
| 1 | Reset schema: `docker compose -f deploy/docker-compose.yml exec db psql -U postgres -d backend_db -c "DROP SCHEMA IF EXISTS batch_demo CASCADE;"` |
| 2 | Buat 10 draft di **Migrations → Migration baru**. |
| 3 | Centang 10 draft, klik **Jalankan terpilih (10)**. |
| 4 | Cek batch: `success`, 10/10, gagal 0, versi berurutan. |
| 5 | Cek schema: `SELECT table_name, table_type FROM information_schema.tables WHERE table_schema='batch_demo';` |
| 6 | Cek versi: `SELECT version, name, status FROM migrations WHERE name LIKE 'manual10-%' ORDER BY version;` |

| Grafana / Prometheus | Query |
|---|---|
| Batch per status | `sum by (status) (increase(migration_batch_total[30m]))` |
| Item per status | `sum by (status) (increase(migration_batch_items_total[30m]))` |
| Item p50 | `histogram_quantile(0.50, sum by (le) (increase(migration_item_duration_seconds_bucket[30m])))` |
| Item p95 | `histogram_quantile(0.95, sum by (le) (increase(migration_item_duration_seconds_bucket[30m])))` |
| Gagal + partial | `sum(migration_batch_total{status="failed"}) + sum(migration_batch_total{status="partial"}) or vector(0)` |
| Rentang | Prometheus butuh durasi eksplisit, mis. `[30m]`; di Grafana boleh pakai `$__range`. |
| Catatan | `rate([5m])` bisa 0/No data setelah idle; `Durasi batch p95` masih kosong karena `migration_batch_duration_seconds_count` belum bertambah. Analisis p50/p95 = Person 2. |

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
| `POST /migrations/:mid/execute` non-draft | 409 |
| SQL error `SELECT 1/0` | 200, `status=failed`, rollback |
| Target tidak terjangkau | 502/504, status tetap `draft` |
| `GET /api/v1/metrics` | 404; metrik hanya di `:9101` |

## Troubleshooting

| Gejala | Aksi |
|---|---|
| `tidak ada database dengan level edit` | Beri akses `edit` atau set `DB_ID`. |
| `400 at most 200 migrations per batch` | Pakai `COUNT=200` atau beberapa batch. |
| Batch `failed`/`partial` | Buka detail item, baca `error`, buat migration baru. |
| Panel Grafana kosong | Pakai `increase(...[30m])` atau jalankan batch saat dashboard dibuka. |
| Metrik kembali 0 | Normal setelah restart backend. |
| Ingin data bersih | Database target baru atau `docker compose -f deploy/docker-compose.yml down -v`. |
