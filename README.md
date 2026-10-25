# leafyGo // K01-18

IF3110 Milestone 1: Database Management Platform.

## Fitur

- Authentication dengan hash argon2id, session opaque dengan idle timeout, absolute
  expiration, rotasi berkala dan saat privilege naik, serta proteksi CSRF.
- Platform Admin untuk user, group, koneksi database, dan pemberian akses per group
  (View Only atau View + Edit).
- Database viewer: schema, table, column, constraint, index, dan paginated data.
- Migration editor dengan draft, EXPLAIN, eksekusi dalam satu transaksi, dan riwayat linear.
- Bonus: EXPLAIN.

## Setup

```bash
docker compose up --build
```

Buka <http://localhost:8080>, login dengan admin dari env
(`ADMIN_USERNAME`/`ADMIN_PASSWORD`).

Compose otomatis menjalankan service one-shot `seed` (`cmd/seed`) sebelum `app`.

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
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/leafygo_test?sslmode=disable' \
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

## Dokumentasi

Parameter session, rotasi, pencabutan akses, CSRF, akses efektif, dan matriks aksi terdapat di
[docs/keamanan-dan-akses.md](docs/keamanan-dan-akses.md).

Isi data seed, daftar user, dan
ketentuan reset ada di [docs/seed.md](docs/seed.md).

## Pembagian kerja

|   NIM    | Nama                    | Bagian kerja                                  |
| :------: | ----------------------- | --------------------------------------------- |
| 13524115 | Ega Luthfi Rais         | Fondasi, auth, session, CSRF, RBAC, dashboard |
| 13523085 | Muhammad Jibril Ibrahim | Admin CRUD, viewer, migration                 |
| 13523037 | Buege Mahara Putra      | Frontend, seeder                              |
