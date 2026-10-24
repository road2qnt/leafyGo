# SEAM — kontrak antar slice (leafyGo)

Repo ini dikerjakan 3 orang paralel. File ini satu-satunya kontrak yang
mengikat. Selama kontrak di bawah tidak dilanggar, tiap slice bisa
dikerjakan tanpa menunggu slice lain.

## Pemilik slice

| Slice | Pemilik | Scope |
|---|---|---|
| Fondasi + auth + RBAC | Ega | `config`, `platform` (001), `domain`, `store` (users/groups/sessions/databases), `auth`, `access`, `secret`, `seed.EnsureAdmin`, `web` (middleware + login/logout/dashboard + stub), `cmd/server` |
| Admin + viewer + migration | Jibril | `platform` (002 migration_history), admin CRUD, `targetdb`, migration service + handler, isi stub `/admin/*` dan `/databases/*` |
| Frontend + docs + demo | Putra | `templates`, `static`, `seed` demo (siti/budi/group/koneksi), README/demo, `docker-compose` demo DB |

## Yang Ega sediakan (jangan diubah tanpa izin Ega)

- `auth.Manager`: `Create(ctx, userID) (raw, sess, err)`,
  `Resolve(ctx, raw) (sess, newRaw, err)`, `Revoke(ctx, raw) error`.
- Middleware: `authenticate`, `csrfProtect`, `requirePlatformAdmin`,
  `requireDBLevel(level)`. Semua route `/admin/*` dan `/databases/*`
  sudah terbungkus middleware yang benar.
- Di handler Jibril: `currentUser(r) *domain.User`,
  `currentSession(r) *domain.Session`, `currentDB(r) *domain.DBConnection`,
  `currentLevel(r) domain.AccessLevel`.
- `store`: `CreateUser/GetUserByUsername/GetUserByID/ListUsers/SetUserAdmin/UpdateUserPassword`,
  `CreateGroup/ListGroups/AddGroupMember/RemoveGroupMember/ListGroupMembers`,
  `SetGroupAccess/RemoveGroupAccess`,
  `CreateDBConnection/GetDBConnection/ListDBConnections`,
  `ListAccessibleDBs/EffectiveLevel`,
  session store (`CreateSession/GetSession/TouchSession/RotateSession/RevokeSession/...`).
- `secret.Box`: `New(key)`, `Encrypt`, `Decrypt`. Jibril dekrip password
  target DB hanya di dalam `targetdb`, jangan pernah render ke template/log.
- Form POST wajib kirim `csrf_token` (hidden field) — CSRF dicek di
  middleware untuk SEMUA POST.

## Yang Jibril boleh/tidak boleh lakukan

- BOLEH: tambah `internal/platform/migrations/002_migration_history.sql`
  (tabel `migration_history`), tambah package `internal/targetdb` dan
  `internal/migration`, ganti isi stub handler di `web` (fungsi baru di
  file `handlers_admin.go`, `handlers_database.go` buatannya sendiri).
- BOLEH: tambah kolom/baris seed admin? Tidak — seed admin milik Ega.
- JANGAN: edit `001_init.sql`, ubah signature `auth.Manager`/`access.Policy`,
  melepas `requireDBLevel`/`requirePlatformAdmin` dari route, menyimpan
  password plaintext, me-render password/sid/DSN ke template atau log.

## Yang Putra boleh/tidak boleh lakukan

- BOLEH: restyle total `templates/` + `static/` (nama file halaman yang
  dirender Ega: `login.html`, `dashboard.html`, `error.html`, `stub.html`;
  kalau rename, kabari Ega), tambah `seed.Demo` + koneksi demo di compose,
  perluas README + panduan demo.
- JANGAN: ubah logika middleware/handler, ubah nama route, menghapus
  `csrf_token` dari form (itu merusak semua POST).

## Aturan session & RBAC (jangan dilanggar)

- Cookie hanya berisi identifier acak 256-bit; DB menyimpan SHA-256-nya.
- Rotasi tiap 15 menit + saat login; grace period 10 detik untuk ID lama.
- Idle timeout 30 menit, absolute 8 jam (tidak diperpanjang rotasi).
- Akses DB hanya lewat group; effective level = tertinggi antar group.
- Platform admin BUKAN akses database — admin akses DB hanya via group.
