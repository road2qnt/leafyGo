# Data seed

Database yang di-seed sebagai berikut:

| Koneksi    | Host (port host)      | Isi                                                                                                      |
| ---------- | --------------------- | -------------------------------------------------------------------------------------------------------- |
| Storefront | `targets-db` (5434)   | customers, addresses, products, orders, order_items, reviews, view `order_totals`                        |
| Inventory  | `targets-db` (5434)   | warehouses, suppliers, stock_levels, purchase_orders, `stock_movements` (tanpa primary key)              |
| Billing    | `targets-db` (5434)   | invoices, payments, refunds, payment_methods, `card_vault` (tidak bisa di-SELECT oleh user app)          |
| People     | `targets-db` (5434)   | departments, positions, employees, salaries, leave_requests                                              |
| Analytics  | `analytics-db` (5436) | schema `analytics` (daily_sales, product_metrics, materialized view) dan `public` (etl_runs, dashboards) |
| Legacy ERP | `legacy-erp.internal` | host sengaja tidak ada, untuk menunjukkan koneksi gagal                                                  |

Daftar admin dan user:

| User            | Password                    | Group         | Akses efektif                                                                                                       |
| --------------- | --------------------------- | ------------- | ------------------------------------------------------------------------------------------------------------------- |
| `admin`, `sari` | `admin12345`, `password123` |               | Platform Admin, tanpa akses database                                                                                |
| `andi`          | `password123`               | backend       | Storefront, Inventory: View + Edit. Legacy ERP: View Only                                                           |
| `bayu`          | `password123`               | backend, data | Storefront, Inventory: View + Edit (level tertinggi menang). Billing, Legacy ERP: View Only. Analytics: View + Edit |
| `putri`         | `password123`               | data          | Storefront, Inventory, Billing, Legacy ERP: View Only. Analytics: View + Edit                                       |
| `hendra`        | `password123`               | finance       | Billing: View + Edit. Storefront, Analytics: View Only                                                              |
| `rina`          | `password123`               | hr            | People: View + Edit                                                                                                 |
| `joko`          | `password123`               | support       | Storefront: View Only                                                                                               |
| `wulan`         | `password123`               |               | Tidak ada                                                                                                           |
| `eko`           | `password123`               | backend       | Akun nonaktif, tidak bisa login                                                                                     |

Riwayat migration juga di-seed dengan migration aktual.

Seed ditolak saat `APP_ENV=production`. Untuk reset total: `docker compose down -v`.
