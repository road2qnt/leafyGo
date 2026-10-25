# Keamanan, Session, dan Akses

## Authentication

- Login memakai username dan password. Password disimpan sebagai hash argon2id (memori 64 MiB, 3 iterasi, paralelisme 2, salt 16 byte).
- Username yang tidak ada tetap menjalankan verifikasi hash dummy supaya waktu respons tidak membocorkan keberadaan akun.
- Percobaan login dibatasi 8 kegagalan per kombinasi IP dan username dalam 5 menit. Counter hanya ada di memori proses, sehingga reset saat server restart dan tidak dibagi antar-instance.
- Akun nonaktif tidak bisa login, dan session yang sedang aktif langsung dicabut saat akun dinonaktifkan.
- Kredensial koneksi database target dienkripsi di database platform (`ENCRYPTION_KEY`, 32 byte).

## Session

Cookie bernama `sid` berisi identifier acak 32 byte (base64 URL). Database hanya menyimpan hash SHA-256 dari `sid`.

| Parameter           | Env                        | Default  | Arti                                                                                                                                   |
| ------------------- | -------------------------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| Idle timeout        | `SESSION_IDLE_TIMEOUT`     | 30 menit | Session berakhir bila tidak dipakai selama durasi ini. Setiap request yang lolos memperpanjang timeout, paling sering sekali per menit |
| Absolute expiration | `SESSION_ABSOLUTE_TIMEOUT` | 8 jam    | Umur maksimum sejak login, tidak pernah diperpanjang                                                                                   |
| Interval rotasi     | `SESSION_ROTATE_INTERVAL`  | 15 menit | Identifier diganti bila session lebih tua dari ini                                                                                     |
| Grace period        | `SESSION_GRACE_PERIOD`     | 10 detik | Identifier lama masih diterima sesingkat ini setelah rotasi                                                                            |

Atribut cookie: `HttpOnly`, `SameSite=Lax`, `Path=/`, dan `Secure` bila `APP_ENV=production` atau `COOKIE_SECURE=true`.

### Rotasi

Rotasi terjadi pada tiga kondisi:

1. **Saat login:** Session lama pada browser yang sama dicabut, lalu session baru dibuat (mencegah session fixation).
2. **Berkala:** Pada request pertama setelah interval rotasi terlewati.
3. **Saat privilege naik:** Lihat bagian pencabutan dan rotasi di bawah.

### Pencabutan dan rotasi akibat perubahan akses

Hak akses dihitung dari database pada setiap request, tidak disimpan di session, sehingga perubahan akses berlaku seketika. Session tetap diperlakukan sesuai arah perubahannya:

| Event                                  | Tindakan pada session user terdampak |
| -------------------------------------- | ------------------------------------ |
| Logout                                 | Dicabut                              |
| Akun dinonaktifkan                     | Dicabut                              |
| Password direset admin                 | Dicabut                              |
| User dikeluarkan dari group            | Dicabut                              |
| Akses group diturunkan atau dihapus    | Dicabut untuk semua anggota group    |
| Group dihapus                          | Dicabut untuk semua anggota          |
| User ditambahkan ke group              | Rotasi paksa                         |
| Akses group dinaikkan atau ditambahkan | Rotasi paksa untuk semua anggota     |

## CSRF

- Semua request selain GET, HEAD, dan OPTIONS pada area terautentikasi wajib membawa token CSRF (field `csrf_token` atau header `X-CSRF-Token`) yang cocok dengan token milik session, dibandingkan secara constant-time.
- Form login memakai token terpisah (double submit cookie `csrf_login`) karena belum ada session.
- Header keamanan: Content-Security-Policy ketat (`script-src 'self'`, `style-src 'self'`), `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`.

## Model akses

- **Platform Admin** (`is_platform_admin`) mengelola user, group, koneksi database, dan pemberian akses. Admin **tidak** memiliki akses ke database mana pun. Level efektifnya selalu None, dan admin tidak bisa menjadi anggota group.
- **User biasa** menjadi anggota nol atau lebih group. Akses database diberikan ke group, tidak langsung ke user.
- Setiap pasangan (group, database) punya satu level: **View Only** atau **View + Edit**.

### Akses efektif

Jika user berada di beberapa group yang punya akses ke database yang sama, **level tertinggi menang**:

### Matriks aksi

Definisi terdapat di `internal/access/policy.go`.

| Aksi                               | View Only | View + Edit |
| ---------------------------------- | --------- | ----------- |
| Lihat overview, schema, data       | Ya        | Ya          |
| Buka & jalankan SQL Console        | Ya        | Ya          |
| Lihat riwayat dan detail migration | Ya        | Ya          |
| Buat, ubah, hapus draft migration  | Tidak     | Ya          |
| Jalankan EXPLAIN                   | Tidak     | Ya          |
| Eksekusi migration                 | Tidak     | Ya          |

Penolakan akses dicatat di log sebagai `access denied` dengan actor, target, dan aksi.

## SQL Query Console & Keamanan Read-Only

SQL Query Console dirancang untuk eksplorasi data interaktif bagi role `View Only` dan `View + Edit`. Untuk membedakan kebutuhan eksplorasi dengan perubahan data, sistem memberlakukan pemisahan ketat antara Console dan Migration:
- **Query Console**: Eksklusif untuk pembacaan data (`SELECT`, `WITH`, `TABLE`, `VALUES`, `SHOW`, `EXPLAIN`). Pernyataan pengubah data ditolak dan pengguna `View + Edit` diarahkan untuk membuat draft migration via handoff button.
- **Migration**: Tempat tunggal untuk DDL dan manipulasi data (`INSERT`, `UPDATE`, `DELETE`, `ALTER`, dll.) dengan audit riwayat linear dan transaksi atomik.

### Jaminan Read-Only (Defense-in-Depth)
1. **Pre-connection Guard**: Memvalidasi SQL sebelum membuka koneksi target. SQL harus berupa 1 pernyataan tunggal, bebas karakter NUL, tidak melebihi `MAX_QUERY_BYTES` (default 64 KiB), tidak mengandung kata kunci kontrol transaksi (`BEGIN`, `COMMIT`, `SET TRANSACTION`), dan kata kunci pertama harus termasuk dalam allowlist.
2. **PostgreSQL Engine Enforcement**: Setiap query dieksekusi dalam transaksi terisolasi `BEGIN READ ONLY` dengan rollback otomatis saat selesai atau pemutusan koneksi cepat saat pembatasan tercapai. PostgreSQL menolak modifikasi data (error code `25006` `read_only_sql_transaction`), pembuatan tabel sementara (`SELECT INTO`), fungsi penulisan sequence, serta CTE yang memodifikasi data.

### Batasan dan Rekomendasi Least-Privilege
- **Superuser & Side-effect Functions**: Modifikasi data tertentu di PostgreSQL tetap dapat dipicu oleh fungsi khusus (misal `dblink` eksternal atau eksekusi fungsi volatile berhak akses superuser). Target database **wajib dikonfigurasi menggunakan role berprinsip least-privilege** (non-superuser, hak akses baca sesuai kebutuhan).
- **Signal Functions Risk**: Fungsi bawaan seperti `pg_terminate_backend()` atau `pg_cancel_backend()` dapat dipanggil dalam klausa `SELECT` jika role target memiliki hak sinyal ke proses lain. Penggunaan akun target terdedikasi sangat direkomendasikan untuk mengisolasi dampak sinyal.
- **Lock Contention**: Query konsol dibatasi secara otomatis oleh `statement_timeout` (default 15s) dan `lock_timeout` (3s) agar pembacaan tabel besar tidak menahan lock yang memblokir migrasi skema atau operasi aplikasi lain.
- **Zero Storage Rationale**: LeafyGo secara sengaja **tidak menyimpan teks query pengguna maupun data hasil query** di database platform. Log sistem hanya mencatat metadata operasional yang aman (durasi, jumlah baris, byte count, status) tanpa merekam payload SQL atau isi baris data demi perlindungan privasi dan kerahasiaan data target.

## Penanganan kegagalan

- Koneksi target yang gagal (host tidak ada, kredensial salah, timeout) ditampilkan sebagai pesan yang sudah disanitasi tanpa DSN atau password. Batas koneksi diatur `TARGET_CONNECT_TIMEOUT` (5 detik).
- Query viewer dibatasi `VIEWER_STATEMENT_TIMEOUT` (5 detik) dan ukuran halaman `VIEWER_PAGE_SIZE` (50, maksimum 100).
- Query console dibatasi oleh `QUERY_TIMEOUT` (default 15 detik), semaphore konkurensi `QUERY_MAX_CONCURRENT` (default 4), batas baris `QUERY_MAX_ROWS` (default 1.000 baris), batas sel `QUERY_MAX_CELL_BYTES` (default 4 KiB), dan total hasil `QUERY_MAX_RESULT_BYTES` (default 2 MiB).
