# leafyGo // K01-18

IF3110 Milestone 1: Database Management Platform.

## Fitur

- Authentication dengan hash argon2id, session opaque dengan idle timeout, absolute
  expiration, rotasi berkala dan saat privilege naik, serta proteksi CSRF.
- Platform Admin untuk user, group, koneksi database, dan pemberian akses per group
  (View Only atau View + Edit).
- Database viewer: schema, table, column, constraint, index, dan paginated data.
- SQL Query Console: eksekusi query read-only interaktif dengan visualisasi hasil tabular, pembatasan ukuran hasil, dan perlindungan multi-lapis.
- Migration editor dengan draft, EXPLAIN, eksekusi dalam satu transaksi, dan riwayat linear.
- Bonus: EXPLAIN.

## Milestone 2 — SPA, observability, dan tracing (bagian Ega)

- **SPA responsif** React 19 + TypeScript + Vite + Tailwind CSS v4 + TanStack
  Query + React Router, di-embed ke binary Go dan disajikan di
  <http://localhost:8080/app/>. UI template M1 tetap di `/` selama JSON API M2
  belum selesai.
- **Structured logging** JSON `log/slog` dengan `trace_id`/`span_id`/
  `correlation_id`, redaksi kredensial, dan kebijakan SQL per sumber
  (`platform`, `target_viewer`, `query_console`, `dry_run`, `migration`).
- **OpenTelemetry tracing** console + OTLP/HTTP, propagasi W3C
  `traceparent`/`tracestate` dari API → queue → worker → target DB.
- **Prometheus `/metrics`** untuk queue/migration, `pgxpool`, dan gauge
  `jobs_current` khusus `cmd/server`.
- **Bonus observability stack** (OTel Collector, Prometheus, Tempo, Loki,
  Grafana) dengan versi pinned dan dashboard siap pakai.

Detail serah-terima API untuk P1/P2: [docs/m2-spa-api-contract.md](docs/m2-spa-api-contract.md).
Rencana kerja: [docs/m2-ega-workplan.md](docs/m2-ega-workplan.md).

### Menjalankan SPA

```bash
make web-install
make web-dev          # http://localhost:5173/app/ dengan proxy /api/v1 ke :8080
```

Demo tanpa backend M2 (mock API di browser):

```bash
make web-build
go run ./cmd/server
# buka http://localhost:8080/app/?mock=1
```

Mode mock juga aktif pada build khusus dengan `VITE_USE_MOCKS=true`; build
default selalu memanggil `/api/v1` sungguhan. Flag `?mock=1` disimpan di
localStorage; `?mock=0` mematikannya. Login sebagai `putra` untuk approval
terakhir proposal Inventory; login sebagai `jibril` untuk melihat
`409 PREDECESSOR_NOT_READY` pada People Ops.

Akun demo: `jibril`, `putra`, `ega` dengan password `password123`; `admin`
dengan password `admin12345`.

SPA sekarang juga terhubung ke API asli hasil merge P1/P2. Untuk data seed M1/M2
gunakan akun seed seperti `andi` / `bayu` / `putri` (password `password123`) dan
`admin` / `admin12345`; mode mock dengan akun `jibril`/`putra`/`ega` hanya untuk
demo tanpa backend.

Build ulang SPA setelah mengubah `web/`:

```bash
make web-build        # hasil ke internal/web/dist, ikut ter-embed ke Go
```

### Telemetry & metrics

| Env | Default | Fungsi |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | mis. `http://otel-collector:4318`; kosong = hanya console |
| `OTEL_TRACES_CONSOLE` | true di non-production | cetak span JSON ke stdout |
| `OTEL_TRACES_SAMPLE_RATIO` | `1` | rasio sampling 0–1 |
| `METRICS_ENABLED` | `true` | endpoint `/metrics` |
| `SPA_ROOT` | `false` | `true` = `/` redirect ke `/app/` |
Jalankan stack observability (setelah `docker compose up -d`):

```bash
make obs-up
# Grafana http://localhost:3000, Prometheus http://localhost:9090
```

Panduan lengkap, contoh output log JSON, span OpenTelemetry, korelasi log/trace, dan sampel metrik Prometheus: [deploy/observability/README.md](deploy/observability/README.md).

## Setup

```bash
docker compose up --build
```

Buka <http://localhost:8080>, login dengan admin dari env
(`ADMIN_USERNAME`/`ADMIN_PASSWORD`).

Compose otomatis menjalankan service one-shot `seed` (`cmd/seed`) sebelum `app`.

Untuk mengubah jumlah worker migrasi:

```bash
docker compose up -d --scale worker=3 worker
```

Untuk rebuild app tanpa menjalankan seed (database harus sudah siap):

```bash
docker compose up -d --build --no-deps --force-recreate app
```

## Cara menjalankan tanpa Docker

```bash
createdb leafygo
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/leafygo?sslmode=disable'
export ENCRYPTION_KEY="$(openssl rand -base64 32)"
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD=admin12345
go run ./cmd/server
```

Skema metadata (`internal/platform/migrations`) diterapkan otomatis saat startup.

Untuk mengisi data seed, jalankan database dengan `docker compose up -d db targets-db analytics-db`, lalu
`make seed` dengan env yang sama seperti server (lihat `.env.example` untuk `SEED_*`).

## Test

```bash
go test ./...                                   # unit (integration auto-skip)
cd web && npm test                              # Vitest (status + hide_rollback)
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/leafygo_test?sslmode=disable' \
TEST_TARGET_DATABASE_URL='postgres://postgres:postgres@localhost:5432/leafygo_target_test?sslmode=disable' \
  go test -p 1 ./...                            # unit + integration
```

## Konfigurasi

Semua parameter terdapat di `.env.example`. Yang paling relevan:

| Env                        | Default                        | Fungsi                                      |
| -------------------------- | ------------------------------ | ------------------------------------------- |
| `SESSION_IDLE_TIMEOUT`     | 30m                            | Idle timeout session                        |
| `SESSION_ABSOLUTE_TIMEOUT` | 8h                             | Umur maksimum session                       |
| `SESSION_ROTATE_INTERVAL`  | 15m                            | Interval rotasi identifier                  |
| `SESSION_GRACE_PERIOD`     | 10s                            | Masa berlaku identifier lama setelah rotasi |
| `COOKIE_SECURE`            | true bila `APP_ENV=production` | Atribut Secure pada cookie                  |
| `MIGRATION_TIMEOUT`        | 30s                            | Batas total eksekusi migration              |
| `QUERY_TIMEOUT`            | 15s                            | Batas waktu eksekusi query SQL Console      |
| `QUERY_MAX_ROWS`           | 1000                           | Batas baris hasil query console             |
| `QUERY_MAX_CELL_BYTES`     | 4096                           | Batas ukuran string per sel (4 KiB)         |
| `QUERY_MAX_RESULT_BYTES`   | 2097152                        | Batas akumulasi ukuran hasil (2 MiB)        |
| `QUERY_MAX_CONCURRENT`     | 4                              | Batas query console konkuren global         |
| `WORKER_MAX_CONCURRENT_JOBS` | 4                            | Job paralel per proses worker               |

## Dokumentasi & Keamanan (Milestone 2 CodeQL SAST)

Parameter session, rotasi, pencabutan akses, CSRF, akses efektif, dan matriks aksi terdapat di
[docs/keamanan-dan-akses.md](docs/keamanan-dan-akses.md).

Hasil lengkap audit Static Application Security Testing (SAST) menggunakan **GitHub CodeQL** (`security-extended` & `security-and-quality` untuk `go` dan `javascript-typescript`), pemetaan CWE / OWASP Top 10, mitigasi kontrol berlapis (`RejectTxControl`, `pgx.Identifier.Sanitize()`, `BEGIN ... ROLLBACK` dry-run, Multi-Group Leader Approval, dan database immutability triggers), serta bukti re-scan didokumentasikan di:
- Workflow CI: [.github/workflows/codeql.yml](.github/workflows/codeql.yml)
- Laporan Audit SAST: [tests/security/CODEQL_REPORT.md](tests/security/CODEQL_REPORT.md)

Isi data seed, daftar user, dan
ketentuan reset ada di [docs/seed.md](docs/seed.md).

Worker migrasi, pemulihan lock, dan dataset uji besar dijelaskan di [docs/worker.md](docs/worker.md).

## Stress test (k6)

Script, cara menjalankan, hasil per tingkat beban, dan analisisnya ada di [docs/stress-test.md](docs/stress-test.md).

## Pembagian kerja

|   NIM    | Nama                    | Bagian kerja                                  |
| :------: | ----------------------- | --------------------------------------------- |
| 13524115 | Ega Luthfi Rais         | Fondasi, auth, session, CSRF, RBAC, dashboard |
| 13523085 | Muhammad Jibril Ibrahim | Admin CRUD, viewer, migration                 |
| 13523037 | Buege Mahara Putra      | Frontend, seeder                              |
