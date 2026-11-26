# Stress test k6

Script: [tests/k6/migration_stress.js](../tests/k6/migration_stress.js).
Fixture: [tests/k6/seed_k6_fixtures.go](../tests/k6/seed_k6_fixtures.go).

## Cara menjalankan

Stack harus sudah berjalan (`docker compose up -d --build`) dan k6 terinstal di host.
Set `OTEL_TRACES_CONSOLE=false` di `.env` sebelum menyalakan stack.

```sh
make k6-fixtures K6_FIXTURE_ARGS="-work-ms 150"   # siapkan 5 target DB dan 5500 proposal
PROFILE=baseline make k6-stress                   # 90 detik, beban normal
make k6-fixtures K6_FIXTURE_ARGS="-work-ms 150"   # fixture baru sebelum run berikutnya
PROFILE=stress make k6-stress                     # 6 menit, profil lengkap
```

Ringkasan JSON di-output ke `tests/k6/results/summary-<profile>.json`.

| Variabel / flag       | Default               | Fungsi                                                         |
| --------------------- | --------------------- | -------------------------------------------------------------- |
| `-proposals`          | 1100                  | Proposal `pending_review` per target DB                        |
| `-dbs`                | 5                     | Jumlah target DB, DB pertama adalah pool `same_db`             |
| `-work-ms`            | 0                     | `pg_sleep` di setiap migration untuk mensimulasikan SQL berat  |
| `-targets-admin`      | localhost:5434        | Alamat `targets-db` dari host, untuk membuat database          |
| `-targets-conn`       | targets-db:5432       | Alamat yang disimpan di `db_connections`, dipakai app & worker |
| `BASE_URL`            | http://localhost:8080 | Alamat app                                                     |
| `PROFILE`             | stress                | `stress` atau `baseline`                                       |
| `MULTI_PACING_FACTOR` | 4                     | Pengali pacing submitter `multi_db`                            |
| `DRAIN_TIMEOUT_S`     | 300                   | Batas tunggu `teardown()` sampai antrean kosong                |

Setiap run fixture melakukan soft delete pada koneksi `k6 Target *` lama dan drop database `k6_target_*`. Fixture
menolak berjalan bila koneksi lama masih punya job `queued`/`running`.

## Fixture

Fixture membuat user k6_reviewer, k6_approver, k6_reader, group k6-reviewers (leader k6_reviewer) dan
k6-approvers (leader k6_approver), keduanya dengan akses edit ke setiap target. Per target DB, satu transaksi
COPY menyisipkan 1100 migration_definitions (seq_version = effective_seq = 1..1100), 1100 proposal
`apply` berstatus `pending_review`, snapshot migration_proposal_approvers untuk kedua group, dan approval dari
k6-reviewers. Approval terakhir dari k6_approver lewat API melengkapi quorum dan langsung meng-enqueue job.
Seluruh 5500 proposal tersisip dalam 385 ms.

Migration berupa `insert into k6_events (migration_seq) values (k)` dengan sql_down berupa `delete` yang sesuai,
didahului `select pg_sleep(0.150)` bila `-work-ms 150`.

Ukuran fixture dihitung dari profil `stress`:

$$
\left\lceil\left(\frac{60}{1.0} + \frac{90}{0.35} + \frac{90}{0.15}\right) \times 1.2\right\rceil =
\lceil 917 \times 1.2 \rceil \approx 1100 \text{ proposal/DB}
$$

## Desain skenario

- **Submitter**: 1 VU (`constant-vus`) per target DB. VU meng-approve slot `k+1` segera setelah respons slot
  `k` kembali sehingga `queue_seq` selalu mengikuti `effective_seq`, tanpa menunggu job selesai. Setiap iterasi
  mem-poll maksimal `K = 5` job in-flight tertua dengan `http.batch` bertag `kind:poll`.
- **Reader**: `ramping-vus` yang memanggil `/auth/me`, `/databases`, `/databases/{id}/overview`, dan
  `/databases/{id}/history` dengan tag `kind:api`.
- **`teardown()`**: menunggu sampai tidak ada eksekusi `queued`/`running`, lalu membaca `/history` setiap target
  DB. Setiap job yang di-enqueue punya tepat satu baris eksekusi, jadi migration_job_completion_rate dihitung atas
  seluruh job yang disubmit, termasuk yang tidak sempat di-poll. Job dihitung berhasil hanya bila statusnya `success`.

Pengiriman job memakai migration yang sudah di-approve: setiap proposal sudah membawa approval k6-reviewers, dan
approval k6_approver melengkapi quorum sehingga job langsung masuk antrean dalam transaksi yang sama. Respons `200`
dari approval hanya berarti job ter-enqueue. Keberhasilan migration dihitung dari status akhir job di `teardown()`.

**Beban normal** ditetapkan sebagai stage warmup, dengan 10 reader VU dan 1 job/s ke setiap pool. Profil `baseline`
menjalankan ini selama 60 detik sebagai acuan. Stage berikutnya menaikkan beban di atas normal, lalu drain dan
cooldown menurunkannya kembali untuk mengamati pemulihan.

| Stage    | Waktu            | Reader VU | Pacing `same_db` | Pacing per DB `multi_db` |
| -------- | ---------------- | --------- | ---------------- | ------------------------ |
| warmup   | 0:00 sampai 1:00 | 0 ke 10   | 1.00 s           | 4.00 s                   |
| ramp     | 1:00 sampai 2:30 | 10 ke 30  | 0.35 s           | 1.40 s                   |
| peak     | 2:30 sampai 4:00 | 30 ke 60  | 0.15 s           | 0.60 s                   |
| drain    | 4:00 sampai 5:30 | 60 ke 10  | berhenti submit  | berhenti submit          |
| cooldown | 5:30 sampai 6:00 | 10 ke 0   | berhenti submit  | berhenti submit          |

Cookie `sid` dikirim manual karena atribut `Secure` (saat `COOKIE_SECURE=true`) membuat cookie jar k6 tidak
mengirimnya lewat HTTP biasa. Login diberi tag `kind:auth` karena hash argon2id sengaja lambat.

## Hasil

**Environment testing**: semua container di satu host (12 thread, RAM 14 GiB), PostgreSQL 18, satu worker dengan
`WORKER_MAX_CONCURRENT_JOBS=4`, k6 v2.3.0 di host. Keduanya memakai `-work-ms 150`.

| Metrik                                       | Baseline (90 s) | Stress (6 menit) |
| -------------------------------------------- | --------------- | ---------------- |
| Request HTTP total                           | 908 (10 req/s)  | 19818 (55 req/s) |
| `http_req_duration{kind:api}` p50 / p95      | 3.3 / 22.6 ms   | 3.4 / 30.0 ms    |
| `http_req_duration{kind:poll}` p50 / p95     | 2.1 / 4.3 ms    | 1.6 / 3.6 ms     |
| `api_error_rate`                             | 0%              | 0%               |
| Job disubmit `same_db` / `multi_db`          | 60 / 60         | 905 / 915        |
| `migration_job_completion_rate` (kedua pool) | 100%            | 100%             |
| Throughput penyelesaian job `same_db`        | 1.01 job/s      | 3.53 job/s       |
| Throughput penyelesaian job `multi_db`       | 1.07 job/s      | 3.81 job/s       |
| `migration_exec_duration_ms` p95 `same_db`   | 202 ms          | 214 ms           |
| `migration_exec_duration_ms` p95 `multi_db`  | 186 ms          | 225 ms           |
| `migration_queue_wait_ms` p95 `same_db`      | 109 ms          | 15626 ms         |
| `migration_queue_wait_ms` p95 `multi_db`     | 39 ms           | 102 ms           |

Throughput penyelesaian job adalah jumlah job `success` dibagi rentang dari job pertama di-enqueue sampai job
terakhir selesai.

### Per tingkat beban (run stress)

API (`kind:api`, gabungan reader dan approval):

| Stage    | Reader VU | Throughput API | p50    | p95     | Error rate  |
| -------- | --------- | -------------- | ------ | ------- | ----------- |
| warmup   | 0 ke 10   | 7.97 req/s     | 3.4 ms | 22.3 ms | 0% (0/478)  |
| ramp     | 10 ke 30  | 31.26 req/s    | 3.8 ms | 24.0 ms | 0% (0/2813) |
| peak     | 30 ke 60  | 72.16 req/s    | 3.6 ms | 34.1 ms | 0% (0/6494) |
| drain    | 60 ke 10  | 46.64 req/s    | 2.9 ms | 39.9 ms | 0% (0/4198) |
| cooldown | 10 ke 0   | 7.27 req/s     | 3.6 ms | 49.2 ms | 0% (0/218)  |

Job per pool. Laju masuk dihitung dari job yang disubmit pada stage itu, throughput selesai dari job yang selesai
pada stage itu, dan waktu tunggu dikelompokkan menurut stage saat job disubmit:

| Stage  | Pool       | Laju masuk | Throughput selesai  | Queue wait p50 | Queue wait p95 | Queue wait max |
| ------ | ---------- | ---------- | ------------------- | -------------- | -------------- | -------------- |
| warmup | `same_db`  | 1.00 job/s | 1.00 job/s          | 12 ms          | 61 ms          | 69 ms          |
| warmup | `multi_db` | 1.00 job/s | 1.00 job/s          | 9 ms           | 26 ms          | 86 ms          |
| ramp   | `same_db`  | 2.83 job/s | 2.82 job/s          | 10 ms          | 20 ms          | 115 ms         |
| ramp   | `multi_db` | 2.86 job/s | 2.84 job/s          | 10 ms          | 65 ms          | 107 ms         |
| peak   | `same_db`  | 6.56 job/s | 5.49 job/s          | 9900 ms        | 16012 ms       | 16671 ms       |
| peak   | `multi_db` | 6.64 job/s | 6.64 job/s          | 11 ms          | 109 ms         | 252 ms         |
| drain  | `same_db`  | 0          | 1.08 job/s (97 job) |                |                |                |
| drain  | `multi_db` | 0          | 0.01 job/s (1 job)  |                |                |                |

Semua threshold lolos pada kedua run:

```javascript
'http_req_duration{kind:api}': ['p(95)<500'],
'api_error_rate': ['rate<0.01'],
'migration_job_completion_rate{pool:multi_db}': ['rate>0.99'],
'migration_job_completion_rate{pool:same_db}': ['rate>0.99'],
'migration_queue_wait_ms{pool:multi_db}': ['p(95)<3000'],
'migration_queue_wait_ms{pool:same_db}': ['p(95)<30000'],
```

## Analisis

**Beban normal (warmup)**: Kedua pool menyelesaikan job secepat job masuk (1 job/s) dengan waktu tunggu p95 di
bawah 70 ms. API p95 22 ms pada 8 req/s. Angka ini sama dengan profil `baseline`, jadi dijadiksn acuan untuk stage
berikutnya.

**Ramp**: Laju naik hampir 3 kali (2.8 job/s, 31 req/s), tetapi masih di bawah kapasitas serial satu database.
Throughput selesai mengikuti laju masuk dan waktu tunggu tidak berubah. p95 API hanya naik dari 22 ms ke 24 ms.

**Peak**: Kapasitas serial `db_target_A` terukur dari `migration_jobs`: setiap job butuh 174 ms eksekusi ditambah
rata-rata 6 ms jeda sampai job berikutnya mulai, atau 5.54 job/s. Jeda sekecil itu berasal dari drain loop
yang langsung memanggil `ClaimNextJob` dan `NOTIFY` saat lock dilepas, tanpa menunggu `WORKER_POLL_INTERVAL` 2 s.
Laju masuk 6.56 job/s melebihi kapasitas, sehingga throughput selesai tertahan di 5.49 job/s dan backlog tumbuh
sekitar 1 job/s. Waktu tunggu naik linear sampai 16.7 s untuk job terakhir yang disubmit saat peak. Pool `multi_db`
menerima laju total yang sama, tetapi tersebar ke 4 lock DB (masing-masing 1.66 job/s), sehingga throughput selesai
sama dengan laju masuk dan p95 waktu tunggu hanya 109 ms, sekitar 147 kali lebih rendah daripada `same_db`.
Kenaikan kecil dari 26 ms ke 109 ms berasal dari 5 database yang berebut 4 slot `WORKER_MAX_CONCURRENT_JOBS`, bukan
dari lock database. Di sisi API, 60 reader VU menghasilkan 72 req/s dengan p95 34 ms dan tanpa error. Approval tidak
melambat meskipun Predecessor Gate mengunci seluruh 1100 definisi per DB pada setiap approval.

**Drain (pemulihan)**: Setelah submit berhenti, `db_target_A` menyelesaikan sisa backlog 97 job pada kapasitas penuh
dan antrean kosong 16.8 detik setelah submit terakhir, sesuai backlog dibagi 5.54 job/s. Pool `multi_db` sudah
kosong sejak akhir peak. Tidak ada job yang gagal, dan urutan FIFO terjaga karena tidak ada approval yang ditolak
Predecessor Gate. p95 API pada drain dan cooldown sedikit lebih tinggi (40 ms dan 49 ms) walaupun request lebih
sedikit, karena respons `/history` semakin besar seiring riwayat bertambah. `/history` tidak berpaginasi. Pada akhir
run, satu respons untuk `db_target_A` berukuran 835 KB, dan reader menerima total sekitar 2.5 GB selama 6 menit.
Endpoint ini kandidat pertama untuk paginasi bila riwayat terus bertambah.

**Penskalaan**: Menambah worker (`docker compose up -d --scale worker=2 worker`) menghapus batas 4 slot untuk
`multi_db`, tetapi tidak menambah throughput `same_db` karena lock per database tetap menyerialkan job. Untuk satu
database, throughput hanya naik bila durasi tiap migration turun, misalnya dengan mode batch.

**Polling terbatas**: Dengan `K = 5`, polling menghasilkan 5546 request (p95 3.6 ms) untuk 1820 job. Karena
antrean FIFO, job tertua selalu selesai lebih dulu sehingga hampir semua job tetap ter-sampling, sementara jumlah
request polling tetap linear terhadap jumlah job.
