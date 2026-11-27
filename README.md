# Database Management Platform — IF3110 K01-4

<img width="1920" height="1080" alt="dbPlatform (1)" src="https://github.com/user-attachments/assets/763af006-0724-4f33-884a-4109ccb97319" />


Platform untuk mengelola akses ke database PostgreSQL bersama: pengguna masuk, melihat schema dan data database yang boleh diaksesnya, lalu membuat dan menjalankan migration secara terkontrol dengan riwayat yang tercatat.

```
Login → Access Control → Pilih Database → Lihat Schema/Data
      → Buat Migration → Review & Approval Leader → Queue → Worker
      → Commit/Rollback → Riwayat Migration
```

## Fitur

- **Autentikasi**: login dengan username dan password (bcrypt). Session disimpan di backend; browser hanya memegang id acak di cookie `HttpOnly`.
- **Session**: idle timeout, absolute expiration, rotasi berkala dengan grace period, logout yang mencabut session di backend, dan proteksi CSRF.
- **Platform Admin**: mengelola user, group, keanggotaan, koneksi database, dan pemberian akses.
- **Akses lewat group**: setiap group diberi level `view` (View Only) atau `edit` (View + Edit) per database.
- **Koneksi database**: koneksi ke satu atau lebih database PostgreSQL; password disimpan terenkripsi (AES-256-GCM); kegagalan koneksi ditangani dengan pesan yang aman.
- **Database viewer**: schema, table, column, constraint, index, dan isi tabel dengan paging.
- **Migration**: editor SQL up/down (atau ditandai irreversible), eksekusi asynchronous lewat queue dan worker dalam transaction (commit jika berhasil, rollback jika gagal), versi linear per database, dan riwayat lengkap (versi, SQL, pelaksana, waktu, status).
- **Batch per potongan row**: migration data besar dijalankan per `batch_size` row, satu transaction per potongan, checkpoint `(execution, run, cursor)` ditulis di database target pada transaction yang sama. Bisa dilanjutkan dari checkpoint atau dibatalkan dengan SQL down terbalik (`partially_reverted`).
- **Workflow & approval**: setiap perubahan (up, rollback, forward) harus disetujui Team Leader dari setiap group yang punya akses ke database tersebut sebelum masuk queue.
- **Rollback & Forward**: membatalkan migration aktif terakhir dengan SQL down, lalu menerapkannya kembali dengan forward. Riwayat punya opsi **Hide rollback**.
- **SPA responsif**: seluruh halaman dapat dipakai di desktop maupun mobile.

## Struktur repositori

| Path | Isi |
|---|---|
| `server/` | Backend Go (Fiber + GORM + pgx) |
| `server/app/main.go` | Entry point: konfigurasi, AutoMigrate, seed admin |
| `server/internal/authz/` | **Aturan hak akses** (`CanView`, `CanEdit`, akses efektif) |
| `server/internal/authctx/` | Identitas user per request (`UserID`, `IsAdmin`, `Session`) dan guard `RequireAuth` / `RequireAdmin` |
| `server/internal/middleware/` | Middleware autentikasi session (`RequireSession`), CSRF, dan perakitan dependency-nya |
| `server/internal/user_handler/` | Login, session, CRUD user |
| `server/internal/group_handler/` | CRUD group, keanggotaan, pemberian akses database |
| `server/internal/targetdb/` | Koneksi database target: enkripsi kredensial, pool, test koneksi |
| `server/internal/database_handler/` | Viewer schema/data, migration, dan change request |
| `server/internal/workflow/` | Aturan murni: status migration, evaluasi approval leader, eligibility rollback/forward, Hide rollback |
| `server/internal/review/` | Service change request: pengajuan, approve/reject, dispatch ke queue |
| `server/internal/engine/` | Queue, worker, lock per database, dan eksekusi SQL |
| `server/internal/models/` | Model tabel platform dan tipe response API |
| `frontend/` | Frontend React + Vite + TypeScript + Tailwind |
| `docs/` | Kontrak API, schema database, checklist pengujian |
| `scripts/invoker.sh` | Build, menjalankan, dan menjalankan test |
| `deploy/` | Docker Compose, Dockerfile backend/frontend, dan konfigurasi Prometheus/Grafana |

## Menjalankan dengan Docker Compose (disarankan)

Kebutuhan: Docker Desktop.

Jalankan dari root repository:

```sh
docker compose -f deploy/docker-compose.yml up --build
```

| Layanan | Alamat |
|---|---|
| Aplikasi (frontend) | http://localhost (juga https://localhost dan http://localhost:5173) |
| Backend API | http://localhost:9000/api/v1 |
| PostgreSQL platform | `localhost:5432`, user `postgres`, password `postgres` |

Semua port compose di-bind ke `127.0.0.1` (hanya bisa diakses dari mesin ini). Untuk demo dari perangkat lain, ubah binding di `deploy/docker-compose.yml` atau gunakan SSH tunnel; jangan buka port database/Grafana/Prometheus ke publik.

Login awal: **`admin` / `admin-password`** (Platform Admin).

Menghentikan: `docker compose -f deploy/docker-compose.yml down`. Menghapus juga seluruh data: `docker compose -f deploy/docker-compose.yml down -v`.

### Menyiapkan database contoh untuk dicoba

Platform butuh database target untuk dikelola. Contoh membuat database `toko_demo` di container yang sama:

```sh
docker exec if3110-db psql -U postgres -c "CREATE ROLE toko_app LOGIN PASSWORD 'toko_app'"
docker exec if3110-db psql -U postgres -c "CREATE DATABASE toko_demo OWNER toko_app"
docker exec if3110-db psql -U toko_app -d toko_demo -c "CREATE TABLE customers (id serial PRIMARY KEY, email varchar(120) NOT NULL UNIQUE, name text); INSERT INTO customers (email, name) VALUES ('ana@mail.com', 'Ana'), ('budi@mail.com', 'Budi');"
```

Lalu di aplikasi (sebagai admin):

1. **Connections** → tambah koneksi: host **`db`** (nama service Docker, bukan `localhost`), port `5432`, database `toko_demo`, user `toko_app`, password `toko_app`, SSL `disable`. Jangan pakai user `postgres`/superuser untuk koneksi target.
2. **Users** → buat user, misalnya `budi` dan `siti`.
3. **Groups** → buat group, masukkan anggota, beri akses ke `toko_demo` (`edit` untuk `budi`, `view` untuk `siti`), lalu edit group untuk memilih **Team Leader** dari anggotanya. Setiap group yang punya akses wajib punya leader.
4. Login sebagai `budi` untuk melihat schema/data, membuat migration, dan mengajukan review. Login sebagai leader di halaman **Review** untuk menyetujui; setelah semua leader setuju, migration masuk queue dan dijalankan worker. Login sebagai `siti` untuk melihat bahwa View Only hanya bisa melihat.

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

### Test frontend

```sh
cd frontend
npm test          # unit test (node:test)
npm run test:e2e  # Playwright; aplikasi harus berjalan di localhost:5173
```

> Frontend juga punya mode data palsu (mock) untuk pengembangan UI. Mode ini aktif jika `frontend/.env` berisi `VITE_USE_MOCK=true`. Untuk memakai backend asli, hapus baris itu atau isi `VITE_USE_MOCK=false`.

## Konfigurasi backend (environment variable)

| Variable | Wajib | Keterangan |
|---|---|---|
| `DB_DSN` | ya | DSN PostgreSQL untuk database platform |
| `ADMIN_USERNAME`, `ADMIN_PASSWORD` | saat pertama kali | Membuat Platform Admin pertama jika belum ada admin sama sekali |
| `DB_ENC_KEY` | disarankan | Key AES-256 (base64, 32 byte) untuk mengenkripsi password koneksi. Tanpa ini dipakai key acak, sehingga koneksi yang tersimpan tidak bisa dibuka lagi setelah restart |
| `SESSION_HASH_KEY`, `SESSION_BLOCK_KEY` | disarankan | Key untuk menandatangani dan mengenkripsi cookie session (block key 16/24/32 byte). Tanpa ini dipakai key acak, sehingga semua user harus login ulang setelah restart |
| `APP_ENV=production` atau `COOKIE_SECURE=true` | production | Mengaktifkan `Secure` cookie, menolak `DEV_AUTH`, secret default, self-approval, dan target DB superuser |
| `ALLOW_SELF_APPROVAL=true` | tidak | Override: izinkan leader menyetujui change request miliknya sendiri (default ditolak di production) |
| `DENY_SUPERUSER_TARGET=true` | tidak | Tolak eksekusi migration bila user database target adalah superuser |
| `PORT` | tidak | Port backend, default `9000` |
| `WORKER_COUNT` | tidak | Jumlah worker migration, default `2` |
| `MIGRATION_TIMEOUT_SECONDS` | tidak | Batas eksekusi satu migration di database target, default `300` detik |
| `OTEL_TRACES_EXPORTER=none` | tidak | Matikan exporter span ke stdout (default: span OpenTelemetry ditulis ke stdout sebagai JSON) |
| `HTTP_READ_TIMEOUT_SECONDS`, `HTTP_WRITE_TIMEOUT_SECONDS`, `HTTP_IDLE_TIMEOUT_SECONDS` | tidak | Timeout HTTP, default `15`/`30`/`60` detik |
| `HTTP_BODY_LIMIT_BYTES` | tidak | Batas body request, default `1048576` (1 MB) |
| `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME_MINUTES` | tidak | Batas pool database platform, default `20`/`10`/`30` menit |
| `DEV_AUTH=true` | **jangan di production** | Hanya untuk development/test: identitas diambil dari header `X-Debug-User` |

Nilai di `deploy/docker-compose.yml` dan `scripts/invoker.sh` hanya untuk development dan pengujian.

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
- View Only: melihat schema, data, serta riwayat dan detail migration. View + Edit: seluruh akses View Only, ditambah membuat, mengubah, dan mengajukan migration (up, rollback, forward) untuk dijalankan. Persetujuan dilakukan oleh Team Leader.

### Database viewer

- Semua query viewer berjalan di transaction `READ ONLY` dengan batas waktu 10 detik.
- Data ditampilkan per halaman (default 50 baris, maksimum 200), diurutkan berdasarkan primary key jika ada.
- Nama schema dan tabel dicocokkan dulu ke katalog PostgreSQL lalu di-quote, sehingga tidak bisa dipakai untuk SQL injection.

### Migration

- Migration dibuat sebagai `draft` dan hanya draft yang bisa diubah atau diajukan untuk review.
- Eksekusi berjalan dalam satu transaction: jika ada statement yang gagal, seluruh perubahan di-rollback dan migration berstatus `failed` beserta pesan errornya. Migration yang gagal bisa dijalankan ulang lewat Forward (perlu approval lagi) atau diganti dengan migration baru.
- Nomor versi (1, 2, 3, …) diberikan hanya untuk migration yang berhasil, sehingga riwayat setiap database linear.
- Riwayat dicatat sebelum commit di database target, sehingga riwayat dan isi database tidak bisa berbeda.
- Dalam satu waktu hanya satu migration yang boleh berjalan per database.
- Perintah yang tidak bisa berjalan di dalam transaction (misalnya `CREATE INDEX CONCURRENTLY`, `VACUUM`) tidak didukung.

### Workflow, approval, rollback, dan forward

- Status: `Draft → Pending Review → Approved → Queue → Running → Success/Failed`.
- Yang mengajukan harus View + Edit. Pengajuan ditolak jika ada group dengan akses yang belum punya leader.
- Perlu persetujuan leader dari **setiap group** yang punya akses (view maupun edit); leader yang memimpin beberapa group cukup menyetujui sekali. Satu penolakan membatalkan pengajuan dan migration up kembali menjadi `draft`.
- Setelah leader terakhir menyetujui, permintaan otomatis masuk queue. Rollback dan forward juga lewat alur yang sama.
- **Rollback** memakai SQL down asli dan hanya untuk migration aktif terakhir yang reversible (urutan A → B → C: rollback C dulu). Migration irreversible tidak bisa di-rollback.
- **Forward** menjalankan ulang SQL up migration yang di-rollback atau gagal, mengikuti urutan linear, dan tidak otomatis menjalankan migration berikutnya.
- SQL migration yang sudah dieksekusi tidak bisa diubah; perbaikan dibuat sebagai migration baru.
- Halaman **Riwayat eksekusi** menampilkan semua attempt, termasuk yang gagal. **Hide rollback** menyembunyikan eksekusi yang dibatalkan beserta rollback suksesnya (dipasangkan lewat execution ID, bukan nama migration). Data riwayat tidak dihapus.

### Queue, worker, dan lock

- **Queue** adalah tabel `migration_jobs` di database platform, jadi job tetap ada setelah restart. API hanya membuat job lalu mengembalikan job ID. Worker (`WORKER_COUNT`, default 2) mengambil job dengan `SELECT ... FOR UPDATE SKIP LOCKED`, sehingga satu job tidak pernah diambil dua worker.
- **Lock per database target**: session advisory lock PostgreSQL `pg_try_advisory_lock(hashtextextended('dbmp:<connection_id>', 0))` di database platform, dipegang lewat koneksi khusus milik worker selama job berjalan. Karena semua worker dan proses memakai database platform yang sama, lock berlaku lintas worker, proses, dan container. Database berbeda punya lock berbeda, jadi bisa berjalan paralel.
- Worker yang tidak mendapat lock mengembalikan job ke queue **tanpa** memakan attempt. Job juga tidak diambil selama database yang sama masih punya job `running`/`uncertain` atau migration batch `partial`.
- **Revalidation**: setelah lock didapat, sebelum SQL dijalankan, worker memeriksa ulang keadaan terkini: status migration masih `approved`, definisi SQL down/irreversible masih valid, target rollback masih migration aktif terakhir, dan urutan forward masih linear. Aturannya sama dengan saat review (`review.StillValid`). Jika tidak lagi sah, attempt dicatat gagal dengan `revalidation failed: ...` tanpa menjalankan SQL, tanpa retry, dan tanpa mengubah migration.
- **Retry**: hanya error sementara (SQLSTATE `08xxx`, `40001`, `40P01`, `55P03`, `57P0x`, dan koneksi terputus), maksimal 3 attempt dengan jeda eksponensial 2 → 4 → … detik (maksimal 30 detik). Error SQL dari user (mis. `42P01`) dan timeout langsung `failed`.
- **Ledger anti double commit**: setiap eksekusi menulis `execution_id` ke tabel `_dbmp.executions` di database target **dalam transaction yang sama** dengan SQL migration. Sebelum menjalankan SQL, worker memeriksa ledger; job duplikat dengan execution ID yang sudah ada dicatat sebagai sudah ter-commit dan tidak dijalankan lagi. Schema `_dbmp` inilah yang terlihat di viewer.

**Pemulihan lock ketika worker gagal**

1. Worker yang crash (proses mati atau koneksi putus) otomatis melepas lock: advisory lock terikat ke sesi database, dan PostgreSQL melepasnya begitu koneksi worker tertutup. Tidak ada lock menggantung dan tidak perlu TTL. Transaction migration yang belum di-commit juga otomatis di-rollback oleh PostgreSQL.
2. Selama berjalan, worker memperbarui `heartbeat_at` setiap 5 detik. Reaper di setiap proses backend memeriksa job `running` tanpa heartbeat lebih dari 30 detik, serta job `uncertain`.
3. Reaper mencoba mengambil lock database tersebut. Kalau lock masih dipegang, pemiliknya masih hidup dan job dibiarkan. Kalau lock didapat, reaper memeriksa ledger `_dbmp.executions` di database target:
   - `execution_id` ada → commit sudah terjadi sebelum crash → job `success` (dicatat `recovered`).
   - `execution_id` tidak ada → commit tidak terjadi → attempt dicatat gagal sementara, lalu job di-queue ulang dan diambil worker lain.
   - Ledger tidak bisa dibaca (target tidak terjangkau) → job `uncertain`, dan migration berikutnya di database yang sama ditahan sampai hasilnya dapat dipastikan.
4. Migration batch per potongan row tidak memakai ledger per execution. Checkpoint (`run`, potongan, cursor) ditulis dalam transaction yang sama dengan setiap potongan, sehingga job yang di-queue ulang melanjutkan dari potongan terakhir yang committed tanpa mengulang potongan sebelumnya.

### Logging dan tracing

Semua log adalah JSON satu baris lewat `log/slog` ke stdout. Log yang terjadi di dalam request atau job otomatis membawa `trace_id` dan `span_id`. Setiap eksekusi SQL dicatat, termasuk query read-only viewer (`sql.query`). Mulai, hasil, serta commit/rollback dicatat sebagai event terpisah (`sql.start`, `tx.commit`/`tx.rollback`, `sql.failed`, `job.finished`). Password di SQL disamarkan (`PASSWORD '***'`); session id dan kredensial database tidak pernah ditulis ke log.

Trace OpenTelemetry (W3C Trace Context) menyambungkan API → queue → worker → lock → query. Trace context ikut disimpan di job (`migration_jobs.trace_parent`), sehingga span worker menjadi bagian dari trace request yang menyetujui migration. Span ditulis ke stdout oleh exporter console. Contoh di bawah diambil dari `sh scripts/invoker.sh test-api`.

Contoh log satu job migration (trace `568b8b93…`, dari request approve leader terakhir sampai worker selesai):

```json
{"time":"2026-11-27T10:58:23.4705705+07:00","level":"INFO","msg":"job enqueued","component":"migration_engine","event":"job.enqueued","actor":"35d05785-720f-4982-b6b9-45d995c3e0c6","database":"c65e39c9-3a2c-406e-8830-8ffc2504de1e","status":"queued","job_id":"1d7e562f-8ca5-4158-97f2-dc24ca1858f4","execution_id":"86885212-53d2-4772-82e8-23b6564857de","migration_id":"f7da6a48-b737-4e74-af3a-8e0768a43c1b","kind":"up","trace_id":"568b8b938aac0b831d44a35d660aba03","span_id":"9ed1f7c1df8405ac"}
{"time":"2026-11-27T10:58:23.4785009+07:00","level":"INFO","msg":"database lock acquired","component":"migration_engine","execution_id":"86885212-53d2-4772-82e8-23b6564857de","attempt":1,"migration_id":"f7da6a48-b737-4e74-af3a-8e0768a43c1b","database":"c65e39c9-3a2c-406e-8830-8ffc2504de1e","kind":"up","actor":"35d05785-720f-4982-b6b9-45d995c3e0c6","job_id":"1d7e562f-8ca5-4158-97f2-dc24ca1858f4","worker":"asus-55696-w2","event":"lock.acquired","trace_id":"568b8b938aac0b831d44a35d660aba03","span_id":"a100397bf7f0e9c1"}
{"time":"2026-11-27T10:58:23.4930262+07:00","level":"INFO","msg":"migration SQL started","component":"migration_engine","execution_id":"86885212-53d2-4772-82e8-23b6564857de","attempt":1,"migration_id":"f7da6a48-b737-4e74-af3a-8e0768a43c1b","database":"c65e39c9-3a2c-406e-8830-8ffc2504de1e","kind":"up","actor":"35d05785-720f-4982-b6b9-45d995c3e0c6","job_id":"1d7e562f-8ca5-4158-97f2-dc24ca1858f4","event":"sql.start","query":"CREATE TABLE api_smoke_1108 (id serial PRIMARY KEY, note text); CREATE INDEX api_smoke_1108_note_idx ON api_smoke_1108 (note);","trace_id":"568b8b938aac0b831d44a35d660aba03","span_id":"7308fa74ff0a6e80"}
{"time":"2026-11-27T10:58:23.522457+07:00","level":"INFO","msg":"transaction committed","component":"migration_engine","execution_id":"86885212-53d2-4772-82e8-23b6564857de","attempt":1,"migration_id":"f7da6a48-b737-4e74-af3a-8e0768a43c1b","database":"c65e39c9-3a2c-406e-8830-8ffc2504de1e","kind":"up","actor":"35d05785-720f-4982-b6b9-45d995c3e0c6","job_id":"1d7e562f-8ca5-4158-97f2-dc24ca1858f4","event":"tx.commit","status":"success","duration_ms":39,"trace_id":"568b8b938aac0b831d44a35d660aba03","span_id":"7308fa74ff0a6e80"}
{"time":"2026-11-27T10:58:23.5436272+07:00","level":"INFO","msg":"job attempt finished","component":"migration_engine","execution_id":"86885212-53d2-4772-82e8-23b6564857de","attempt":1,"migration_id":"f7da6a48-b737-4e74-af3a-8e0768a43c1b","database":"c65e39c9-3a2c-406e-8830-8ffc2504de1e","kind":"up","actor":"35d05785-720f-4982-b6b9-45d995c3e0c6","job_id":"1d7e562f-8ca5-4158-97f2-dc24ca1858f4","worker":"asus-55696-w2","event":"job.finished","status":"success","execution_status":"success","duration_ms":39,"trace_id":"568b8b938aac0b831d44a35d660aba03","span_id":"a100397bf7f0e9c1"}
```

Contoh attempt gagal (SQL user salah, tidak di-retry) dan query read-only viewer:

```json
{"time":"2026-11-27T10:58:31.3728093+07:00","level":"WARN","msg":"migration attempt failed","component":"migration_engine","execution_id":"73dc2dad-9159-4aea-8ec6-a5e6f42bae89","attempt":1,"migration_id":"09570705-d91b-4bdb-b142-155b4a7e1686","database":"c65e39c9-3a2c-406e-8830-8ffc2504de1e","kind":"up","actor":"35d05785-720f-4982-b6b9-45d995c3e0c6","job_id":"6f2b7378-39e1-496f-acf8-160f2e2a807f","event":"sql.failed","phase":"execute","status":"failed","error":"ERROR: syntax error at or near \"THIS\" (SQLSTATE 42601)","sqlstate":"42601","transient":false,"duration_ms":17,"trace_id":"b4ea55575b6f1d5ae183aa14ddccd58a","span_id":"3bee7ef232a5e0af"}
{"time":"2026-11-27T10:58:26.7256872+07:00","level":"INFO","msg":"viewer query executed","event":"sql.query","actor":"35d05785-720f-4982-b6b9-45d995c3e0c6","database":"c65e39c9-3a2c-406e-8830-8ffc2504de1e","operation":"rows","query":"SELECT \"id\", \"note\" FROM \"public\".\"api_smoke_1108\" ORDER BY \"id\" LIMIT $1 OFFSET $2","params":[50,0],"rows":0,"duration_ms":2,"status":"success","trace_id":"d15e56e5ae4d200b496ea06abf1dd4b5","span_id":"eee7d16894764389"}
```

Span dalam trace yang sama (`SpanKind`: 1 internal, 2 server, 3 client, 4 producer, 5 consumer):

| Span | Span ID | Parent | Durasi |
|---|---|---|---|
| `POST /api/v1/databases/:id/change-requests/:rid/approve` | `9bbde374cd418e2e` | (root) | 85,4 ms |
| `queue.enqueue` | `9ed1f7c1df8405ac` | `9bbde374cd418e2e` | 15,4 ms |
| `job.process` | `a100397bf7f0e9c1` | `9ed1f7c1df8405ac` | 71,9 ms |
| `lock.acquire` | `56600b710072f5f4` | `a100397bf7f0e9c1` | 3,9 ms |
| `migration.execute` | `7308fa74ff0a6e80` | `a100397bf7f0e9c1` | 44,4 ms |
| `db.query` | `4cf9b875b2adeb2b` | `7308fa74ff0a6e80` | 19,6 ms |
| `db.commit` | `084977c8ae35a472` | `7308fa74ff0a6e80` | 3,4 ms |
| `history.record` | `d0e679fe0f76f062` | `a100397bf7f0e9c1` | 20,7 ms |

Contoh satu span mentah dari exporter console:

```json
{"Name":"db.query","SpanContext":{"TraceID":"568b8b938aac0b831d44a35d660aba03","SpanID":"4cf9b875b2adeb2b","TraceFlags":"01","TraceState":"","Remote":false},"Parent":{"TraceID":"568b8b938aac0b831d44a35d660aba03","SpanID":"7308fa74ff0a6e80","TraceFlags":"01","TraceState":"","Remote":false},"SpanKind":3,"StartTime":"2026-11-27T10:58:23.4964655+07:00","EndTime":"2026-11-27T10:58:23.5160516+07:00","Attributes":[{"Key":"db.system.name","Value":{"Type":"STRING","Value":"postgresql"}},{"Key":"db.query.text","Value":{"Type":"STRING","Value":"CREATE TABLE api_smoke_1108 (id serial PRIMARY KEY, note text); CREATE INDEX api_smoke_1108_note_idx ON api_smoke_1108 (note);"}}],"Status":{"Code":"Unset","Description":""}}
```

Query viewer menghasilkan span `viewer.read` dengan anak `db.query` per query. Error dan durasi tercatat di setiap span.

## Stress test dengan k6 (bonus)

Script: [`scripts/k6-stress.js`](scripts/k6-stress.js), dijalankan lewat [`scripts/k6-stress.sh`](scripts/k6-stress.sh) (memakai binary `k6` bila ada, atau image `grafana/k6` di network Docker yang sama dengan backend).

Yang diukur sekaligus:

- **Akses API** (`GET /databases`, `/migrations`, `/migrations/:mid`, `/jobs`) dengan beban yang naik bertahap lalu turun.
- **Pengiriman migration** (`POST /migrations` + `POST /migrations/:mid/change-requests`) berjalan bersamaan.
- **Eksekusi** diverifikasi terpisah: script menyetujui pengajuan lewat bulk approve sebagai `LEADER_USERNAME`, lalu memantau queue sampai kosong dan menghitung waktu tunggu queue + durasi job. Respons pengiriman tidak dianggap bukti eksekusi.

```sh
LEADER_USERNAME=tokdalang LEADER_PASSWORD=... DB_ID=<uuid-koneksi> ./scripts/k6-stress.sh
# atur beban: PEAK_VUS=20 HOLD=40s SUBMIT_VUS=2 PREPARE=12
# threshold bisa diubah: READ_P95_MS=800 SUBMIT_P95_MS=1500 ERROR_RATE=0.01
```

Threshold default: `http_req_failed < 1%`, p95 baca `< 800 ms`, p95 pengiriman `< 1500 ms`.

Hasil pada mesin pengembangan (Docker Compose, satu database target, 1 worker pool 2 goroutine; job memakai `SELECT 1`):

| Beban | Throughput | p95 baca | p95 kirim | Error | Queue wait p95 | Durasi job p95 |
|---|---:|---:|---:|---:|---:|---:|
| 5 VU | 12,9 req/s | 3,02 ms | 10,03 ms | 0% | 12,09 ms | 2,04 s* |
| 10 VU | 23,1 req/s | 3,16 ms | 9,63 ms | 0% | 11,39 ms | 2,04 s* |
| 20 VU | 44,3 req/s | 3,58 ms | 9,73 ms | 0% | 10,69 ms | 2,04 s* |
| Ramp 1→10→0 (40 s) | 21,6 req/s | 2,36 ms | 10,59 ms | 0% | 12,79 ms | 2,04 s* |

\* median durasi job 4 ms; nilai p90/p95 2,04 s berasal dari satu job pertama yang menunggu siklus poll worker. Bukan bottleneck.

Analisis:

- Latency baca hanya naik 3,02 → 3,58 ms saat beban 4×, dan throughput naik hampir linear (12,9 → 44,3 req/s). Backend belum mendekati saturasi pada beban normal tim (20 VU); threshold p95 800 ms menyisakan headroom besar.
- Jalur pengiriman stabil ±10 ms di semua tingkat, jadi penambahan beban baca tidak memperlambat create/submit.
- Queue wait tetap ±10 ms karena job uji selesai seketika. Untuk melihat serialisasi yang sebenarnya, jalankan dengan job lebih berat: `JOB_SQL="SELECT pg_sleep(0.5)"` — waktu tunggu queue akan mendekati kelipatan durasi job karena satu database hanya menjalankan satu migration.
- Tidak ada error di semua tingkat (0 dari 3.517 request); seluruh threshold lulus.
- Di luar cakupan run ini: pengujian dua database berbeda. Jalankan script dua kali dengan `DB_ID` berbeda — secara desain keduanya dieksekusi paralel, sedangkan di database yang sama job diantrikan.

Catatan: setiap run membuat migration `k6-stress-*` dan `k6-submit-*` yang **permanen** di riwayat (riwayat tidak bisa dihapus), jadi jalankan pada database demo/bench khusus.

## Dokumentasi lain

- [`docs/api-contract.md`](docs/api-contract.md) — kontrak API (endpoint, request, response, kode error) dan ketentuan sistem
- [`docs/schema.sql`](docs/schema.sql) — struktur tabel database platform
- [`docs/TEST_CHECKLIST.md`](docs/TEST_CHECKLIST.md) — daftar fitur dan test yang membuktikannya
- [`docs/MANUAL_UI_TESTING.md`](docs/MANUAL_UI_TESTING.md) — panduan klik-per-klik semua fitur dari SPA (fokus batch)
- [`docs/BATCH_SCENARIOS.md`](docs/BATCH_SCENARIOS.md) — 73 skenario uji batch beserta harapan, jenis (otomatis/manual), dan buktinya
- [`docs/BATCH_TESTING.md`](docs/BATCH_TESTING.md) — checklist uji batch, kontrak SQL, checkpoint, metrik
- [`docs/BATCH_MANUAL_TESTING.md`](docs/BATCH_MANUAL_TESTING.md) — uji batch teknis (script, cancel/resume/revert)
