# Worker migrasi asynchronous

Eksekusi migration diantrekan ke `migration_jobs` dan dijalankan `cmd/worker` (container `worker`),
FIFO per database dengan lock lintas worker.

## Pemulihan lock saat worker gagal

Lease diperpanjang setiap `WORKER_HEARTBEAT_INTERVAL`. Lease yang kedaluwarsa diambil alih worker lain,
yang memastikan sesi lama sudah mati lalu membaca bukti di target (`_leafygo_executions`,
`_leafygo_batch_checkpoints`): mode normal sukses atau diantrekan ulang, mode batch dilanjutkan dari
checkpoint. Bila hasil tidak pasti, database ditahan `uncertain_hold` dan di-probe ulang berkala.

## Dataset uji

Seeder membuat `bench.large_migration_bench` (1.000.000 baris) dan mencatat durasi satu `UPDATE` tanpa
batching di log `seed` (`unbatched_update_ms`). SQL migration batch memakai placeholder `{{cursor_from}}`
dan `{{cursor_to}}`.

Hasil pengukuran pada lingkungan pengujian (PostgreSQL 18 di Docker):

| Skenario                                                        | Row       | Durasi   |
| :-------------------------------------------------------------- | :-------- | :------- |
| `UPDATE` seluruh tabel tanpa batching (satu transaksi)          | 1.000.000 | 3.717 ms |
| Migration mode batch, `batch_size` 10.000 (100 batch/transaksi) | 1.000.000 | 5.206 ms |

Dataset yang sama digunakan untuk menguji migration per batch di `TestBatchMigrationOnBenchDataset`
(`internal/worker/worker_integration_test.go`), yang membuat tabel dengan generator seeder
(`targets.CreateBenchTable`), mengukur `UPDATE` tanpa batching, lalu menjalankan perubahan yang sama
sebagai migration batch lewat worker. Ulangi pengukuran dengan:

```sh
make test-integration  # atau: go test -count=1 -run TestBatchMigrationOnBenchDataset -v ./internal/worker/
```

Migration batch pada dataset ini:

```sql
-- sql_up
update large_migration_bench set amount = amount + 1, updated_at = now()
where id >= {{cursor_from}} and id <= {{cursor_to}}
-- sql_down
update large_migration_bench set amount = amount - 1, updated_at = now()
where id >= {{cursor_from}} and id <= {{cursor_to}}
-- batch_config
{"table_name": "large_migration_bench", "cursor_col": "id", "batch_size": 10000}
```
