# Checklist Pengujian Backend Milestone 1

Terakhir dijalankan: 2026-10-24

Perintah:

```sh
sh scripts/invoker.sh test-auth   # auth, session, authorization, user, group
sh scripts/invoker.sh test-db     # koneksi PostgreSQL target dan akses
sh scripts/invoker.sh test-all    # unit + integration + API smoke
```

## Auth & Keamanan (backend)

| # | Fitur | Test utama | Status |
|---|-------|------------|--------|
| 1 | Login dan password hashing bcrypt; password/hash tidak bocor di response | `TestHashAndCheckPassword`, `TestIntegrationLogin` | PASS |
| 2 | Rotasi token session pada request terautentikasi | `TestIntegrationSessionRotationAndGrace` | PASS |
| 3 | Grace period: token lama diterima selama grace, ditolak setelah kedaluwarsa | `TestIntegrationSessionRotationAndGrace`, `TestIntegrationSessionGraceExpires` | PASS |
| 4 | Idle timeout 30 menit dan session otomatis direvoke | `TestIntegrationSessionTimeout` | PASS |
| 5 | Absolute timeout 24 jam | `TestIntegrationSessionTimeout` | PASS |
| 6 | Logout merevoke session, menghapus cookie, dan profile langsung 401 | `TestIntegrationLogout` | PASS |
| 7 | CSRF: wajib pada POST/PUT/DELETE, aman untuk GET/HEAD/OPTIONS | `TestRequireCSRF`, `TestIntegrationCSRF` | PASS |
| 8 | Middleware `RequireAuth`, `RequireAdmin`, `DevAuth`, dan `SessionAuth` | `TestRequireAdmin`, `TestRequireAuth`, `TestDevAuthDisabledIgnoresHeaders`, integration login/profile | PASS |
| 9 | Authorization `CanView`/`CanEdit`: none, view, edit, admin, gabungan group, membership dicabut | `TestIntegrationLevelMatrix`, `TestCanViewAndCanEditWithoutAccess` | PASS |
| 10 | CRUD user: create, list, get, update, delete, duplicate, validasi, proteksi self/last admin | `TestIntegrationUserCRUD`, `TestIntegrationCreateUserByAdmin`, `TestIntegrationCreateUserDenied` | PASS |
| 11 | CRUD group dan keanggotaan | `TestIntegrationGroupCRUD`, `TestIntegrationMembership` | PASS |
| 12 | Unit dan integration test tersedia dan dapat dijalankan terpisah | `scripts/invoker.sh test-auth` | PASS |

## Database Target (backend)

| # | Fitur | Test utama | Status |
|---|-------|------------|--------|
| 13 | CRUD koneksi PostgreSQL target | `TestIntegrationConnectionLifecycle` | PASS |
| 14 | Kredensial target dienkripsi AES-GCM; nonce acak; tidak ada plaintext di DB/response | `TestCipherRoundTrip`, `TestCipherUsesRandomNonce`, `TestIntegrationConnectionLifecycle` | PASS |
| 15 | Penanganan gagal koneksi: auth gagal, database tidak ada, host unreachable/timeout | `TestClassifyConnError`, `TestIntegrationConnectionFailures` | PASS |
| 16 | Assign group ke database dan level akses `view`/`edit` | `TestIntegrationGroupAccessAndAuthorization`, `TestIntegrationLevelMatrix` | PASS |
| 17 | Effective access gabungan beberapa group (edit menang) dan pencabutan akses | `TestIntegrationLevelMatrix`, `TestIntegrationUserConnectionAccess` | PASS |
| 18 | Enforcement View Only: user hanya bisa melihat dan test koneksi, update ditolak | `TestIntegrationUserConnectionAccess`, API smoke test | PASS |
| 19 | Enforcement View + Edit: user bisa update koneksi | `TestIntegrationUserConnectionAccess`, API smoke test | PASS |
| 20 | Unit dan integration test tersedia dan dapat dijalankan terpisah | `scripts/invoker.sh test-db` | PASS |
| 21 | Viewer schema database target | Belum ada implementasi/service backend | FAIL |
| 22 | Viewer data database target | Belum ada implementasi/service backend | FAIL |
| 23 | Migration dalam transaction | Belum ada implementasi/service backend | FAIL |
| 24 | Migration versi linear per koneksi | Belum ada implementasi/service backend | FAIL |
| 25 | Riwayat migration | Belum ada implementasi/service backend | FAIL |

## Ringkasan

- PASS: 20
- FAIL: 5
- Catatan: item FAIL adalah fitur yang belum diimplementasikan di backend, bukan regresi Milestone 1. Item tersebut perlu dikerjakan sebelum test perilakunya bisa dibuat.
