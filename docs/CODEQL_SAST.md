# CodeQL SAST

## Status

| Item | Nilai |
|---|---|
| Workflow | `.github/workflows/codeql.yml` |
| Status | Aktif |
| Trigger | Push/PR ke `main`, jadwal mingguan |
| CodeQL | 2.27.4 |
| Query suite | Default (tidak memakai `security-extended`) |
| Repository | `rhenprojects/IF3110_K01-4` |

## Bukti scan terakhir

Run: [42983148889](https://github.com/rhenprojects/IF3110_K01-4/actions/runs/42983148889)  
Commit: `5b71acb`  
Tanggal: 2026-11-11 07:57 UTC

| Language | Analysis ID | Rules | Results | Status |
|---|---|---:|---:|---|
| Go (`server/`) | `2142198346` | 34 | 0 | success |
| JavaScript/TypeScript (`frontend/`) | `2142196926` | 87 | 0 | success |

Code scanning alerts:

| State | Jumlah |
|---|---:|
| Open | 0 |
| Closed | 0 |
| Dismissed | 0 |

Security tab: <https://github.com/rhenprojects/IF3110_K01-4/security/code-scanning>

## Temuan

Tidak ada temuan pada scan terakhir.

| Rule | Severity | Lokasi | Dampak | Keputusan | Alasan |
|---|---|---|---|---|---|
| — | — | — | — | Tidak ada perbaikan | Tidak ada alert CodeQL yang dihasilkan |

Karena tidak ada temuan, tidak ada commit perbaikan dan tidak ada scan ulang khusus. Scan ulang tetap berjalan otomatis pada setiap push/PR berikutnya.

## Cakupan

| Path | Bahasa | Build mode | Rules | Hasil |
|---|---|---|---:|---:|
| `server/` | Go | autobuild | 34 | 0 |
| `frontend/` | JavaScript/TypeScript | none | 87 | 0 |

## Tidak tercakup CodeQL

- SQL migration dan query database
- `docker-compose.yml`, Dockerfile, nginx config
- Shell script (`scripts/*.sh`)
- Dependency vulnerability/SCA (`go mod`, `npm audit`)
- Secret scanning
- DAST/runtime testing
- Business logic, IDOR, authorization matrix, TLS configuration

Hasil 0 alert berarti tidak ada temuan dari query CodeQL yang dijalankan. Ini **bukan** jaminan aplikasi bebas celah.

## Reproduksi

```bash
gh run list --workflow=codeql.yml --limit 5
gh api 'repos/rhenprojects/IF3110_K01-4/code-scanning/alerts?state=open&per_page=100'
gh api 'repos/rhenprojects/IF3110_K01-4/code-scanning/alerts?state=closed&per_page=100'
gh api 'repos/rhenprojects/IF3110_K01-4/code-scanning/analyses?per_page=10'
```

## Rekomendasi

- Tambahkan `queries: security-extended` jika ingin cakupan query lebih luas.
- Tambahkan SCA/secret scanning di CI.
- Lengkapi dengan test manual authorization/IDOR dan DAST.
