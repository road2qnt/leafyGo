# SEAM — pembagian bagian (leafyGo)

| Bagian | Pemilik |
|---|---|
| Fondasi, auth, session, CSRF, RBAC, dashboard | Ega |
| Admin CRUD, viewer, migration | Jibril |
| Frontend, docs, demo seed | Putra |

Aturan main:

- `001_init.sql` jangan diedit. Jibril tambah file `002` buat tabel migration.
- Route `/admin/*` dan `/databases/*` sudah dikunci middleware yang benar. Jibril isi handlernya, jangan lepas kuncinya.
- Semua form POST wajib kirim `csrf_token`.
- Password, session, sid, DSN jangan pernah muncul di template, log, atau URL.
