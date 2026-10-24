# leafyGo — IF3110 Milestone 1 (slice: backend/auth/RBAC — Ega)

> Status: slice fondasi + authentication + RBAC. Stub route untuk
> admin/viewer/migration tersedia (501) dan menunggu slice Jibril.
> Styling/template final + demo seed menunggu slice Putra.

## Cara jalan (Docker)

```bash
docker compose up --build
```

Buka <http://localhost:8080>, login dengan admin dari env
(`ADMIN_USERNAME`/`ADMIN_PASSWORD`).

## Cara jalan tanpa Docker

```bash
createdb leafygo
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/leafygo?sslmode=disable'
export ENCRYPTION_KEY="$(openssl rand -base64 32)"
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD=admin12345
go run ./cmd/server
```

Skema metadata (`001_init.sql`) diterapkan otomatis saat startup.

## Test

```bash
go test ./...                                   # unit (integration auto-skip)
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/leafygo_test?sslmode=disable' \
  go test -p 1 ./...                            # unit + integration
```

`-p 1` diperlukan karena integration test memakai satu database dan truncate.

## Pembagian kerja (tubes 3 orang)

| Nama | Slice | Bukti kontribusi |
|---|---|---|
| Ega | Fondasi, auth, session, CSRF, RBAC, dashboard | repo/commit slice ini |
| Jibril | Admin CRUD, viewer, migration | menyusul di repo ini |
| Putra | Frontend, docs, demo seed | menyusul di repo ini |

Kontrak antar slice: lihat `SEAM.md`.
