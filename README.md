# Database Management Platform — IF3110 K01-4

<img width="1920" height="1080" alt="dbPlatform (1)" src="https://github.com/user-attachments/assets/b205f0e6-68a2-49c0-bacc-1b0be9ef5ae4" />


Platform untuk mengelola akses ke database PostgreSQL bersama: pengguna masuk, melihat schema dan data database yang boleh diaksesnya, lalu membuat dan menjalankan migration secara terkontrol dengan riwayat yang tercatat.

```
Login → Access Control → Pilih Database → Lihat Schema/Data
      → Buat Migration → Eksekusi → Commit/Rollback → Riwayat Migration
```

## Fitur

- **Autentikasi**: login dengan username dan password (bcrypt). Session disimpan di backend; browser hanya memegang id acak di cookie `HttpOnly`.
- **Session**: idle timeout, absolute expiration, rotasi berkala dengan grace period, logout yang mencabut session di backend, dan proteksi CSRF.
- **Platform Admin**: mengelola user, group, keanggotaan, koneksi database, dan pemberian akses.
- **Akses lewat group**: setiap group diberi level `view` (View Only) atau `edit` (View + Edit) per database.
- **Koneksi database**: koneksi ke satu atau lebih database PostgreSQL; password disimpan terenkripsi (AES-256-GCM); kegagalan koneksi ditangani dengan pesan yang aman.
- **Database viewer**: schema, table, column, constraint, index, dan isi tabel dengan paging.
- **Migration**: editor SQL, eksekusi dalam transaction (commit jika berhasil, rollback jika gagal), versi linear per database, dan riwayat lengkap (versi, SQL, pelaksana, waktu, status).

## Struktur repositori

| Path | Isi |
|---|---|
| `server/` | Backend Go (Fiber + GORM + pgx) |
| `server/app/main.go` | Entry point: konfigurasi, AutoMigrate, seed admin |
| `server/internal/authz/` | **Aturan hak akses** (`CanView`, `CanEdit`, akses efektif) |
| `server/internal/authctx/` | Middleware `RequireAuth` / `RequireAdmin` dan identitas user per request |
| `server/internal/user_handler/` | Login, session, CSRF, CRUD user |
| `server/internal/group_handler/` | CRUD group, keanggotaan, pemberian akses database |
| `server/internal/targetdb/` | Koneksi database target: enkripsi kredensial, pool, test koneksi |
| `server/internal/database_handler/` | Viewer schema/data dan migration |
| `server/internal/models/` | Model tabel platform dan tipe response API |
| `frontend/` | Frontend React + Vite + TypeScript + Tailwind |
| `docs/` | Kontrak API, schema database, checklist pengujian |
| `scripts/invoker.sh` | Build, menjalankan, dan menjalankan test |
| `docker-compose.yml` | Menjalankan database, backend, dan frontend sekaligus |

## Menjalankan dengan Docker Compose (disarankan)

Kebutuhan: Docker Desktop.

```sh
docker compose up --build
```

| Layanan | Alamat |
|---|---|
| Aplikasi (frontend) | http://localhost (juga http://localhost:5173) |
| Backend API | http://localhost:9000/api/v1 |
| PostgreSQL platform | `localhost:5433`, user `postgres`, password `postgres` (port 5433 agar tidak bentrok dengan PostgreSQL lokal) |

Login awal: **`admin` / `admin-password`** (Platform Admin).

Menghentikan: `docker compose down`. Menghapus juga seluruh data: `docker compose down -v`.

### Menyiapkan database contoh untuk dicoba

Platform butuh database target untuk dikelola. Contoh membuat database `toko_demo` di container yang sama:

```sh
docker exec if3110-db psql -U postgres -c "CREATE DATABASE toko_demo"
docker exec if3110-db psql -U postgres -d toko_demo -c "CREATE TABLE customers (id serial PRIMARY KEY, email varchar(120) NOT NULL UNIQUE, name text); INSERT INTO customers (email, name) VALUES ('ana@mail.com', 'Ana'), ('budi@mail.com', 'Budi');"
```

Lalu di aplikasi (sebagai admin):

1. **Connections** → tambah koneksi: host **`db`** (nama service Docker, bukan `localhost`), port `5432`, database `toko_demo`, user `postgres`, password `postgres`, SSL `disable`.
2. **Users** → buat user, misalnya `budi` dan `siti`.
3. **Groups** → buat group, masukkan anggota, dan beri akses ke `toko_demo`: `edit` untuk `budi`, `view` untuk `siti`.
4. Login sebagai `budi` untuk melihat schema/data serta membuat dan menjalankan migration. Login sebagai `siti` untuk melihat bahwa View Only hanya bisa melihat.

Admin sendiri **tidak** otomatis bisa membuka isi database (lihat [Keputusan desain](#keputusan-desain)); masukkan admin ke group jika perlu.

## Menjalankan tanpa Docker (development)

Kebutuhan: Go 1.26+, Node.js 22+, PostgreSQL.

**1. Database** — buat database kosong untuk platform, misalnya `web_backend_db`. Tabel dibuat otomatis saat backend start.

**2. Backend** (dari folder `server/`)

```sh
export DB_DSN="host=localhost port=5432 user=postgres password=postgres dbname=web_backend_db sslmode=disable"
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD=admin-password
export DB_ENC_KEY="$(openssl rand -base64 32)"   # simpan nilainya; dibutuhkan lagi setelah restart
go run ./app
```

Di PowerShell gunakan `$env:DB_DSN = "..."` dan seterusnya. Backend berjalan di http://localhost:9000.

**3. Frontend** (dari folder `frontend/`)

```sh
npm install
npm run dev
```

Buka http://localhost:5173. Permintaan `/api` diteruskan ke `localhost:9000` oleh Vite.

> Frontend juga punya mode data palsu (mock) untuk pengembangan UI. Mode ini aktif jika `frontend/.env` berisi `VITE_USE_MOCK=true`. Untuk memakai backend asli, hapus baris itu atau isi `VITE_USE_MOCK=false`.

## Konfigurasi backend (environment variable)

| Variable | Wajib | Keterangan |
|---|---|---|
| `DB_DSN` | ya | DSN PostgreSQL untuk database platform |
| `ADMIN_USERNAME`, `ADMIN_PASSWORD` | saat pertama kali | Membuat Platform Admin pertama jika belum ada admin sama sekali |
| `DB_ENC_KEY` | disarankan | Key AES-256 (base64, 32 byte) untuk mengenkripsi password koneksi. Tanpa ini dipakai key acak, sehingga koneksi yang tersimpan tidak bisa dibuka lagi setelah restart |
| `SESSION_HASH_KEY`, `SESSION_BLOCK_KEY` | disarankan | Key untuk menandatangani dan mengenkripsi cookie session (block key 16/24/32 byte). Tanpa ini dipakai key acak, sehingga semua user harus login ulang setelah restart |
| `APP_ENV=production` atau `COOKIE_SECURE=true` | production | Mengaktifkan atribut `Secure` pada cookie (wajib untuk HTTPS) |
| `PORT` | tidak | Port backend, default `9000` |
| `DEV_AUTH=true` | **jangan di production** | Hanya untuk development/test: identitas diambil dari header `X-Debug-User` |

Nilai di `docker-compose.yml` dan `scripts/invoker.sh` hanya untuk development dan pengujian.

## Pengujian

Automated test mencakup unit test dan integration test (dengan PostgreSQL asli) untuk alur berhasil, kegagalan, dan penolakan akses. Status per fitur ada di [`docs/TEST_CHECKLIST.md`](docs/TEST_CHECKLIST.md).

Jalankan dari root repositori (di Windows gunakan **Git Bash**). Script otomatis menyalakan PostgreSQL khusus test di Docker (port 55432), terpisah dari data development.

```sh
sh scripts/invoker.sh test-unit          # unit test saja, tanpa database
sh scripts/invoker.sh test-auth          # auth, session, CSRF, authorization, user, group
sh scripts/invoker.sh test-db            # koneksi database target, viewer, migration
sh scripts/invoker.sh test-all           # unit + integration + API contract test
sh scripts/invoker.sh test-frontend      # typecheck dan build frontend
sh scripts/invoker.sh test-e2e           # frontend → backend → database lewat proxy Vite
sh scripts/invoker.sh db-down            # hapus container database test
```

Tanpa Docker, arahkan test ke PostgreSQL yang sudah ada dengan `TEST_PG_DSN` (gunakan database khusus test, karena tabelnya dihapus dan dibuat ulang):

```sh
TEST_PG_DSN="host=localhost port=5432 user=postgres password=... dbname=web_backend_test sslmode=disable" \
  sh scripts/invoker.sh test-all
```

## Keputusan desain

Detail lengkap ada di [`docs/api-contract.md`](docs/api-contract.md) bagian 3.

### Session

| Hal | Nilai |
|---|---|
| Idle timeout | 30 menit tanpa request |
| Absolute expiration | 12 jam sejak login (tidak diperpanjang oleh rotasi) |
| Interval rotasi | 15 menit; nama cookie tetap `session`, nilainya diganti |
| Rotasi paksa | saat login dan saat privilege naik (user dijadikan admin) |
| Grace period | 30 detik: id lama masih diterima setelah rotasi |
| Pencabutan session | logout, user dihapus, password diganti admin, status admin diubah |

Session id acak disimpan di backend dalam bentuk hash. Cookie `session` memakai `HttpOnly`, `SameSite=Lax`, dan `Secure` di production. Password dan session id tidak pernah muncul di URL, response, maupun log.

### CSRF

Saat login backend membuat token CSRF per session dan mengirimnya lewat cookie `csrf_token` (bisa dibaca JavaScript). Setiap `POST`/`PUT`/`DELETE` wajib mengirim nilai itu di header `X-CSRF-Token`; jika tidak cocok, backend menjawab `403`.

### Hak akses

- Pemeriksaan hak akses selalu dilakukan di backend, terpusat di `server/internal/authz`.
- Akses database diberikan ke **group**, bukan langsung ke user.
- **Akses efektif**: jika user ada di beberapa group yang mengakses database yang sama, diambil level tertinggi (`edit` > `view` > tanpa akses). Dihitung ulang di setiap request, sehingga perubahan group langsung berlaku.
- **Platform Admin bukan tingkat akses database**: admin mengelola platform tetapi tidak otomatis bisa melihat atau mengubah isi database.
- View Only: melihat schema, data, serta riwayat dan detail migration. View + Edit: seluruh akses View Only, ditambah membuat, mengubah, dan menjalankan migration.

### Database viewer

- Semua query viewer berjalan di transaction `READ ONLY` dengan batas waktu 10 detik.
- Data ditampilkan per halaman (default 50 baris, maksimum 200), diurutkan berdasarkan primary key jika ada.
- Nama schema dan tabel dicocokkan dulu ke katalog PostgreSQL lalu di-quote, sehingga tidak bisa dipakai untuk SQL injection.

### Migration

- Migration dibuat sebagai `draft` dan hanya draft yang bisa diubah atau dijalankan.
- Eksekusi berjalan dalam satu transaction: jika ada statement yang gagal, seluruh perubahan di-rollback dan migration berstatus `failed` beserta pesan errornya. Migration yang gagal tidak bisa dijalankan ulang; buat migration baru.
- Nomor versi (1, 2, 3, …) diberikan hanya untuk migration yang berhasil, sehingga riwayat setiap database linear.
- Riwayat dicatat sebelum commit di database target, sehingga riwayat dan isi database tidak bisa berbeda.
- Dalam satu waktu hanya satu migration yang boleh berjalan per database.
- Perintah yang tidak bisa berjalan di dalam transaction (misalnya `CREATE INDEX CONCURRENTLY`, `VACUUM`) tidak didukung.

## Dokumentasi lain

- [`docs/api-contract.md`](docs/api-contract.md) — kontrak API (endpoint, request, response, kode error) dan ketentuan sistem
- [`docs/schema.sql`](docs/schema.sql) — struktur tabel database platform
- [`docs/TEST_CHECKLIST.md`](docs/TEST_CHECKLIST.md) — daftar fitur dan test yang membuktikannya
