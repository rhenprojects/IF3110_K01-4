# API Contract

Dokumen ini menjelaskan API platform: endpoint yang tersedia, bentuk request dan response, aturan input, serta cara kerja session dan hak akses.
Siapa pun yang memakai API ini (UI, backend, maupun test) mengacu ke dokumen yang sama. Kalau ada perubahan, perbarui dokumen ini lebih dulu agar tidak melenceng dari kodenya.

Isi dokumen:

1. Istilah dan cara membaca dokumen
2. Aturan umum API
3. Ketentuan sistem (session, CSRF, akses efektif, dst.)
4. Auth
5. Users (admin)
6. Groups (admin)
7. Koneksi database (admin)
8. Database untuk user biasa (daftar, schema, data)
9. Migration
10. Tabel izin ringkas

---

## 1. Istilah dan cara membaca dokumen

| Istilah | Artinya |
|---|---|
| **Platform Admin** | User yang mengelola platform: user, group, koneksi database, dan pemberian akses. Admin **bukan** tingkat akses terhadap suatu database |
| **Group** | Kumpulan user. Hak akses ke database diberikan ke group, bukan langsung ke user |
| **Koneksi database** | Data untuk terhubung ke satu database PostgreSQL target yang dikelola platform (host, port, nama database, dll.) |
| **Level akses** | Seberapa jauh sebuah group boleh memakai satu database: `view` (**View Only**, hanya melihat schema, data, dan riwayat migration) atau `edit` (**View + Edit**, ditambah membuat dan menjalankan migration) |
| **Akses efektif** | Level akses yang akhirnya berlaku untuk seorang user pada satu database (lihat 3.4) |
| **Migration** | Perubahan pada database target yang ditulis sebagai SQL, dijalankan dalam transaction, dan dicatat di riwayat |

Cara membaca:

- Semua path ditulis relatif terhadap base URL `/api/v1`. Contoh: `GET /databases` berarti `GET /api/v1/databases`.
- `:id`, `:mid`, dan sejenisnya adalah bagian path yang diisi nilai sebenarnya (misalnya ID database).
- Tanda `?` setelah nama field (`name?`) berarti field itu opsional.

---

## 2. Aturan umum API

- **Base URL:** `/api/v1`
- **Format:** semua request dan response memakai JSON, nama field `snake_case`.
- **ID:** string UUID, misalnya `"3f2b8c1e-..."`.
- **Waktu:** format ISO 8601 UTC, misalnya `"2026-10-24T09:30:00Z"`.
- **Cookie:** browser mengirim cookie otomatis. Aplikasi web dan API harus diakses dari satu alamat yang sama (saat development lewat proxy, saat dipakai lewat satu domain/port) supaya cookie dan CSRF jalan normal.
- **Request yang mengubah data** (`POST`, `PUT`, `DELETE`) wajib membawa header `X-CSRF-Token` (lihat bagian 3.2). Request `GET` tidak perlu.

### Format error

Semua error berbentuk:

```json
{ "error": "pesan singkat" }
```

Khusus error validasi input (400), ada tambahan `field` agar klien bisa menaruh pesan di bawah kolom yang salah:

```json
{ "error": "at least 8 characters", "field": "password" }
```

### Arti status code

| Code | Kapan dipakai |
|---|---|
| 200 | Berhasil, ada isi response |
| 201 | Berhasil membuat data baru |
| 204 | Berhasil, tanpa isi response (hapus, logout) |
| 400 | Input tidak valid (biasanya ada `field`) |
| 401 | Belum login, atau sesi sudah habis/dicabut. Aplikasi klien akan mengarahkan ke halaman login |
| 403 | Sudah login tapi tidak punya izin (bukan admin, akses database kurang, atau CSRF salah) |
| 404 | Data tidak ditemukan |
| 409 | Bentrok dengan kondisi saat ini (nama sudah dipakai, migration sedang berjalan, dll.) |
| 502 | Backend gagal terhubung ke database target |
| 503 | Database platform sendiri tidak terhubung |
| 504 | Koneksi ke database target timeout |

---

## 3. Ketentuan sistem

Bagian ini menjelaskan aturan dan cara kerja yang berlaku di seluruh API.

### 3.1 Session

Session disimpan di backend (tabel `sessions`). Browser hanya memegang **session id acak** di cookie bernama `session`.

| Hal | Nilai | Penjelasan singkat |
|---|---|---|
| Idle timeout | **30 menit** | Sesi berakhir kalau tidak ada request selama 30 menit |
| Absolute expiration | **12 jam** | Sesi berakhir 12 jam setelah login, sesering apa pun dipakai. Rotasi **tidak** memperpanjang ini |
| Interval rotasi | **15 menit** | Session id diganti dengan yang baru tiap 15 menit. Nama cookie tetap `session`, hanya nilainya yang berubah |
| Grace period | **30 detik** | Setelah rotasi, id lama masih diterima 30 detik, untuk menangani response yang gagal sampai atau request yang berjalan bersamaan |
| Rotasi paksa | saat **login** dan saat **privilege naik** | Privilege naik = user dijadikan admin |

Atribut cookie `session`: `HttpOnly`, `SameSite=Lax`, `Path=/`, dan `Secure` aktif di production (HTTPS).

Perilaku di sisi klien: begitu sesi habis atau dicabut, request apa pun dijawab `401`. Aplikasi klien membersihkan data di layar dan membuka halaman login.

### 3.2 CSRF

CSRF adalah serangan di mana situs lain membuat browser user mengirim request ke aplikasi kita memakai cookie user yang sedang login. Pencegahannya:

1. Saat login, backend membuat **token CSRF** acak, menyimpannya di sesi (`sessions.csrf_token`), dan mengirimkannya lewat cookie kedua bernama **`csrf_token`**. Cookie ini **sengaja tidak HttpOnly** supaya JavaScript di browser bisa membacanya.
2. Klien mengirim isi cookie itu di header `X-CSRF-Token` pada setiap `POST/PUT/DELETE`.
3. Backend membandingkan header dengan token di sesi. Kalau tidak cocok atau tidak ada, jawab `403` dengan `{"error": "Invalid CSRF token"}`.
4. Token CSRF **tetap sama sepanjang sesi**. Saat session id dirotasi, cookie `csrf_token` dipasang ulang dengan nilai yang sama.
5. `POST /users/login` dikecualikan karena belum ada sesi. Cookie `SameSite=Lax` tetap melindunginya.

### 3.3 Kapan sesi dicabut

- User menekan **logout**.
- User **dihapus**.
- **Password** user diganti oleh admin.
- Status **admin** user diubah (dijadikan atau dicabut dari admin).

Perubahan group dan akses database **tidak perlu** mencabut sesi, karena hak akses selalu dihitung ulang dari database di **setiap request**. Jadi kalau user dikeluarkan dari group, aksesnya hilang saat request berikutnya.

### 3.4 Akses efektif (kalau user ada di banyak group)

Satu user bisa jadi anggota beberapa group, dan tiap group bisa punya level berbeda ke database yang sama. Aturannya:

> **Ambil level tertinggi dari semua group milik user.** Urutan: `edit` > `view` > tidak ada akses.

Contoh: Budi ada di group Backend (`edit` ke database `toko`) dan group QA (`view` ke `toko`). Akses efektif Budi ke `toko` adalah `edit`.

Aturan ini dihitung di **satu tempat saja** di backend, lalu dipakai oleh pemeriksaan `CanView` dan `CanEdit`.

### 3.5 Platform Admin bukan akses database

Sesuai spek, Platform Admin hanya mengelola platform (user, group, koneksi, pemberian akses). **Menjadi admin tidak otomatis memberi akses ke isi database.**

Artinya admin yang ingin melihat atau mengubah suatu database harus menjadikan dirinya anggota group yang punya akses ke database itu. Pemeriksaan `CanView` dan `CanEdit` tidak punya jalan pintas untuk admin.

### 3.6 Aturan akun

- Password minimal **8 karakter**, maksimal **72 byte** (batas bcrypt). Disimpan dalam bentuk hash (bcrypt), tidak pernah dikirim balik.
- Pesan gagal login **sama** untuk username yang tidak ada dan password yang salah.
- Tidak ada registrasi publik. Akun dibuat oleh admin. Admin pertama dibuat dari env `ADMIN_USERNAME` dan `ADMIN_PASSWORD`.
- Admin **tidak bisa** menghapus dirinya sendiri, mencabut status admin dirinya sendiri, atau membuat platform tanpa admin sama sekali (jawab `409`).
- Password, session id, dan token CSRF **tidak boleh** muncul di URL, response, maupun log.

### 3.7 Aturan migration

- **Satu riwayat linear per database.** Versi hanya diberikan ke migration yang **berhasil**, urut `1, 2, 3, ...` tanpa lompatan.
- Migration punya 3 status:
  - `draft`: baru disimpan, belum dijalankan. Boleh diedit.
  - `success`: sudah dijalankan dan di-commit. Punya nomor versi. Tidak boleh diubah.
  - `failed`: dijalankan tapi gagal dan di-rollback. Tidak punya versi. Tidak boleh dijalankan ulang. Kalau mau mencoba lagi, buat migration baru (SQL-nya bisa disalin dari yang gagal).
- Saat dijalankan, seluruh SQL berjalan dalam **satu transaction**: semua berhasil maka `COMMIT`, ada satu yang gagal maka `ROLLBACK`.
- Dalam satu database, **hanya satu migration yang boleh berjalan pada satu waktu**. Kalau ada yang sedang berjalan, jawab `409`.
- Riwayat migration **tidak bisa dihapus**.
- SQL yang tidak bisa berjalan di dalam transaction (misalnya `CREATE INDEX CONCURRENTLY`) akan gagal. Itu perilaku yang diterima.

### 3.8 Koneksi yang dihapus

Menghapus koneksi database menghapus semua akses group ke database itu. Riwayat migration-nya tetap tersimpan (koneksi di-soft delete).

---

## 4. Auth

### `POST /users/login`

Masuk dengan username dan password. Tidak perlu header CSRF.

Request:

```json
{ "username": "budi", "password": "rahasia123" }
```

Response `200` (backend juga memasang cookie `session` dan `csrf_token`):

```json
{
  "message": "Login successful",
  "user": {
    "id": "…", "username": "budi", "name": "Budi", "is_admin": false,
    "created_at": "…", "updated_at": "…"
  }
}
```

Error: `401` `"Invalid username or password"`, `400` body tidak valid.

### `POST /users/logout`

Mencabut sesi di backend dan menghapus kedua cookie. Response `204`.

### `GET /users/profile`

Mengembalikan data user yang sedang login (bentuk sama seperti `user` di atas). Dipakai saat aplikasi dibuka untuk mengecek apakah sesi masih hidup. `401` kalau belum login, sesi habis, atau dicabut.

---

## 5. Users (admin)

Semua endpoint bagian ini hanya untuk Platform Admin (`403` untuk selain admin).

Bentuk data `User`:

```json
{ "id": "…", "username": "budi", "name": "Budi", "is_admin": false, "created_at": "…", "updated_at": "…" }
```

Aturan input:

| Field | Aturan |
|---|---|
| `username` | 3–50 karakter: huruf, angka, `_`, `.`, `-`. Tidak bisa diubah setelah dibuat |
| `name` | Maksimal 100 karakter. Kalau kosong saat membuat user, diisi sama dengan username |
| `password` | 8–72 byte |
| `is_admin` | `true` atau `false`, default `false` |

| Endpoint | Fungsi |
|---|---|
| `GET /admin/users` | Daftar user. Response `{ "users": [User, ...] }` (belum ada paginasi) |
| `POST /admin/users` | Buat user. Body `{ username, name?, password, is_admin? }`. Response `201` + `User`. `409` kalau username sudah ada |
| `PUT /admin/users/:id` | Ubah user. Body `{ name?, password?, is_admin? }`. Field yang tidak dikirim tidak berubah. Response `200` + `User` |
| `DELETE /admin/users/:id` | Hapus user. Response `204`. `409` kalau menghapus diri sendiri atau admin terakhir |

Catatan `PUT`: `409` kalau admin mencabut status admin dirinya sendiri atau admin terakhir. Mengganti `password` atau `is_admin` mencabut sesi user itu (bagian 3.3).

---

## 6. Groups (admin)

Hanya untuk Platform Admin.

Bentuk data `Group`:

```json
{
  "id": "…", "name": "Tim Backend", "description": "Developer backend",
  "member_ids": ["<id user>", "…"],
  "created_at": "…", "updated_at": "…"
}
```

Aturan: `name` wajib, maksimal 100 karakter, harus unik (`409` kalau sudah ada). `description` maksimal 255 karakter.

| Endpoint | Fungsi |
|---|---|
| `GET /admin/groups` | Daftar group. Response `{ "groups": [Group, ...] }` |
| `POST /admin/groups` | Buat group. Body `{ name, description? }`. Response `201` + `Group` |
| `PUT /admin/groups/:id` | Ubah nama/deskripsi. Body `{ name, description? }`. Response `200` + `Group` |
| `DELETE /admin/groups/:id` | Hapus group beserta keanggotaan dan aksesnya. Response `204` |
| `PUT /admin/groups/:id/members` | **Mengganti seluruh** daftar anggota. Body `{ "user_ids": ["…"] }`. Response `200` + `Group`. `400` (`field: "user_ids"`) kalau ada ID user yang tidak dikenal |
| `GET /admin/groups/:id/access` | Lihat akses database group ini. Response `{ "access": [{ "connection_id": "…", "level": "view" }] }` |
| `PUT /admin/groups/:id/access` | **Mengganti seluruh** daftar akses. Body `{ "access": [{ "connection_id": "…", "level": "edit" }] }`. Response `200` dengan bentuk sama seperti GET |

Tentang `PUT` yang "mengganti seluruh daftar": database yang **tidak ada** di daftar kiriman berarti group itu tidak punya akses ke database tersebut.

Nilai `level`: `"view"` (tampil di UI sebagai **View Only**) atau `"edit"` (**View + Edit**). Nilai lain dijawab `400` (`field: "level"`). `connection_id` yang tidak dikenal dijawab `400` (`field: "connection_id"`).

---

## 7. Koneksi database (admin)

Hanya untuk Platform Admin.

Bentuk data `Connection` (**password tidak pernah dikembalikan**):

```json
{
  "id": "…", "name": "toko", "host": "localhost", "port": 5432,
  "db_name": "toko", "username": "postgres", "ssl_mode": "prefer",
  "created_by": "<id user>", "created_at": "…", "updated_at": "…"
}
```

Aturan input:

| Field | Aturan |
|---|---|
| `name` | Wajib, maksimal 100 karakter, unik (`409` kalau sudah ada) |
| `host` | Wajib, maksimal 255 karakter, hanya satu hostname atau IP (tanpa spasi, koma, atau `/`) |
| `port` | 1–65535, default `5432` |
| `db_name` | Wajib, maksimal 100 karakter |
| `username` | Wajib, maksimal 100 karakter |
| `password` | Wajib saat membuat. Saat mengubah, **kosong berarti tidak diganti** |
| `ssl_mode` | `disable`, `allow`, `prefer` (default), `require`, `verify-ca`, atau `verify-full` |

| Endpoint | Fungsi |
|---|---|
| `GET /admin/connections` | Daftar. Response `{ "connections": [Connection, ...] }` |
| `GET /admin/connections/:id` | Satu koneksi. `404` kalau tidak ada |
| `POST /admin/connections` | Buat. Response `201` + `Connection` |
| `PUT /admin/connections/:id` | Ubah. Response `200` + `Connection` |
| `DELETE /admin/connections/:id` | Hapus. Response `204` (lihat bagian 3.8) |
| `POST /admin/connections/test` | Tes koneksi dari data form yang **belum disimpan**. Body sama seperti `POST` |
| `POST /admin/connections/:id/test` | Tes koneksi yang **sudah disimpan** (memakai password tersimpan). Tanpa body |

Response tes berhasil (`200`): `{ "status": "ok", "server_version": "PostgreSQL 16.4 ..." }`

Kalau koneksi gagal, pesan error-nya salah satu dari ini:

| Code | Pesan | Penyebab |
|---|---|---|
| 502 | `authentication to target database failed` | Username atau password salah |
| 502 | `target database does not exist` | Nama database tidak ada |
| 502 | `cannot connect to target database` | Host/port tidak terjangkau |
| 504 | `timed out connecting to target database` | Timeout |
| 500 | `...; please re-enter the password` | Password tersimpan tidak bisa didekripsi (misalnya `DB_ENC_KEY` berubah). Admin perlu mengisi ulang password |

Error 502/504 yang sama juga muncul di endpoint viewer dan migration (bagian 8 dan 9), jadi UI cukup menampilkan satu jenis pesan, misalnya "Tidak bisa terhubung ke database".

---

## 8. Database untuk user biasa

Bagian ini dipakai semua user yang sudah login. Hasilnya dibatasi oleh **akses efektif** user (bagian 3.4).

Untuk user biasa, detail koneksi seperti `host`, `port`, dan `username` **tidak ditampilkan** supaya infrastruktur tidak terbuka. Hanya endpoint admin (bagian 7) yang menampilkannya.

Aturan penolakan:

- Database tidak ada: `404`.
- Database ada tapi user tidak punya akses: `403`.
- Semua endpoint di bagian ini minimal butuh level `view`.

### `GET /databases`

Daftar database yang boleh diakses user, lengkap dengan level efektifnya:

```json
{
  "databases": [
    { "id": "…", "name": "toko", "db_name": "toko", "level": "edit" },
    { "id": "…", "name": "hr",   "db_name": "hr",   "level": "view" }
  ]
}
```

User yang tidak punya akses ke database mana pun mendapat `{ "databases": [] }`.

### `GET /databases/:id`

Satu database. Bentuknya sama seperti satu item di atas.

### `GET /databases/:id/schema`

Seluruh struktur database dalam satu response (schema bawaan sistem seperti `pg_catalog` dan `information_schema` tidak disertakan):

```json
{
  "schemas": [
    {
      "name": "public",
      "tables": [
        {
          "name": "orders",
          "type": "table",
          "columns": [
            { "name": "id", "type": "integer", "nullable": false, "default": "nextval('orders_id_seq')", "is_primary_key": true },
            { "name": "customer_id", "type": "integer", "nullable": false, "default": null, "is_primary_key": false }
          ],
          "constraints": [
            { "name": "orders_pkey", "type": "primary_key", "definition": "PRIMARY KEY (id)" },
            { "name": "orders_customer_fk", "type": "foreign_key", "definition": "FOREIGN KEY (customer_id) REFERENCES customers(id)" }
          ],
          "indexes": [
            { "name": "orders_pkey", "is_unique": true, "definition": "CREATE UNIQUE INDEX orders_pkey ON public.orders USING btree (id)" }
          ]
        }
      ]
    }
  ]
}
```

`type` table: `"table"` atau `"view"`. `type` constraint: `primary_key`, `foreign_key`, `unique`, atau `check`.

### `GET /databases/:id/tables/:schema/:table/rows?limit=50&offset=0`

Isi tabel, per halaman:

```json
{
  "columns": [ { "name": "id", "type": "integer" }, { "name": "note", "type": "text" } ],
  "rows": [ [1, "halo"], [2, null] ],
  "total": 120,
  "limit": 50,
  "offset": 0
}
```

- Tiap baris di `rows` adalah array yang urutannya sama dengan `columns`.
- Nilai kosong (`NULL`) dikirim sebagai `null`. Tipe yang tidak punya padanan JSON (misalnya `bytea`) dikirim sebagai string.
- `limit` default 50, maksimal 200. `offset` default 0.
- `404` kalau schema atau table tidak ada. Nama schema dan table **harus dicocokkan dengan katalog database** sebelum dipakai di query (jangan disambung mentah ke SQL).

Kalau database target tidak terjangkau, semua endpoint di atas yang membaca database target menjawab `502` atau `504` (lihat bagian 7).

---

## 9. Migration

Bentuk data `Migration`:

```json
{
  "id": "…",
  "connection_id": "…",
  "version": 3,
  "name": "tambah kolom email",
  "sql": "ALTER TABLE customers ADD COLUMN email text;",
  "status": "success",
  "error": "",
  "created_by": "<id user>",
  "created_by_name": "Budi",
  "executed_by": "<id user>",
  "executed_by_name": "Budi",
  "created_at": "…",
  "updated_at": "…",
  "executed_at": "…",
  "duration_ms": 42
}
```

Catatan field:

- `version`, `executed_by`, `executed_by_name`, `executed_at`, `duration_ms` bernilai `null` untuk migration `draft` (dan `version` juga `null` untuk `failed`).
- `error` berisi pesan dari Postgres kalau `status` adalah `failed`.
- `created_by_name` dan `executed_by_name` ditambahkan oleh backend supaya user biasa bisa melihat nama pelaksana tanpa memanggil endpoint admin.

Izin:

- **Melihat** daftar dan detail: minimal `view`.
- **Membuat, mengubah, dan menjalankan**: harus `edit`. Selain itu `403`.

| Endpoint | Fungsi |
|---|---|
| `GET /databases/:id/migrations` | Riwayat. Response `{ "migrations": [Migration, ...] }`, urut dari yang terbaru dibuat. Isinya semua status |
| `GET /databases/:id/migrations/:mid` | Detail satu migration |
| `POST /databases/:id/migrations` | Buat draft. Body `{ "name": "…", "sql": "…" }`. Response `201` + `Migration` berstatus `draft` |
| `PUT /databases/:id/migrations/:mid` | Ubah draft. Body `{ "name": "…", "sql": "…" }`. Response `200`. `409` kalau statusnya bukan `draft` |
| `POST /databases/:id/migrations/:mid/execute` | Jalankan draft |

Aturan input `POST` dan `PUT`: `name` wajib, maksimal 200 karakter. `sql` wajib, tidak boleh kosong.

### Alur `execute`

1. Cek user punya akses `edit`. Kalau tidak, `403`.
2. Kalau statusnya bukan `draft`, `409`.
3. Kalau ada migration lain di database yang sama yang sedang berjalan, `409`.
4. Buka transaction di database target dan jalankan SQL.
5. Hasilnya:

| Hasil | Yang terjadi | Response |
|---|---|---|
| SQL berhasil | `COMMIT`. Status `success`, `version` = versi sukses terakhir + 1, `executed_by`, `executed_at`, dan `duration_ms` terisi | `200` + `Migration` |
| SQL error | `ROLLBACK`. Status `failed`, `error` berisi pesan Postgres, `version` tetap `null` | `200` + `Migration` |
| Database target tidak terjangkau | Tidak ada yang dijalankan. Status tetap `draft` | `502` atau `504` |

Kenapa SQL gagal tetap `200`? Karena permintaannya sendiri sudah diproses dengan benar dan hasilnya tercatat di riwayat. Klien cukup membaca field `status`.

---

## 10. Tabel izin ringkas

✅ = boleh, ❌ = ditolak (`401` kalau belum login, `403` kalau sudah login).

| Aksi | Belum login | User tanpa akses | `view` | `edit` | Platform Admin (tanpa group) |
|---|---|---|---|---|---|
| Login | ✅ | ✅ | ✅ | ✅ | ✅ |
| Endpoint `/admin/*` | ❌ | ❌ | ❌ | ❌ | ✅ |
| Daftar database (`GET /databases`) | ❌ | ✅ (kosong) | ✅ | ✅ | ✅ (kosong) |
| Lihat schema dan data | ❌ | ❌ | ✅ | ✅ | ❌ |
| Lihat riwayat migration | ❌ | ❌ | ✅ | ✅ | ❌ |
| Buat, ubah, jalankan migration | ❌ | ❌ | ❌ | ✅ | ❌ |

Catatan: user yang admin **dan** anggota group dengan akses `edit` mendapat izin `edit` lewat group-nya, bukan karena admin (bagian 3.5).