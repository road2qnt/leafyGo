# Outside PostgreSQL Test Database

This directory contains a completely separate Docker Compose environment running an isolated PostgreSQL 18 instance with rich multi-schema test data. It is specifically designed to test connecting to an external database outside of leafyGo's internal Docker network.

---

## Quick Start

### 1. Start the Outside Database
From within this directory (`./test_outside_db`):
```bash
docker compose up -d
```
The container will start, initialize the `outside_corp` database, and automatically run `seed.sql`.

### 2. Check Database Status
```bash
docker compose ps
```
The health check will report `healthy` once PostgreSQL is ready.

### 3. Re-seed or Reset Data
- **Re-run seed script without data wipe:**
  - PowerShell: `.\seed.ps1`
  - Bash: `./seed.sh`
- **Full reset (wipes volumes and re-runs seed from scratch):**
  ```bash
  docker compose down -v
  docker compose up -d
  ```

---

## Connection Credentials

| Parameter | When leafyGo runs in Docker | When leafyGo runs on Host (`go run ./cmd/server`) |
| :--- | :--- | :--- |
| **Host** | `host.docker.internal` *(see note below)* | `localhost` or `127.0.0.1` |
| **Port** | `5438` | `5438` |
| **Database Name** | `outside_corp` | `outside_corp` |
| **Username** | `outside_admin` | `outside_admin` |
| **Password** | `outside_secret_123` | `outside_secret_123` |
| **SSL Mode** | `disable` | `disable` |

> **Note on `host.docker.internal`:**
> - On **Docker Desktop (Windows & macOS)**, `host.docker.internal` works automatically out of the box to resolve the host machine.
> - On **Linux native Docker engine**, if leafyGo container needs to resolve `host.docker.internal`, ensure `extra_hosts: ["host.docker.internal:host-gateway"]` is present under the `app` service in leafyGo's `docker-compose.yml`.

---

## How to Test Connecting in leafyGo

### 1. Register the Database in leafyGo Admin Platform
1. Log in to leafyGo as platform admin (`admin` / `admin12345`).
2. Navigate to **Platform Admin** > **Databases** (`/admin/databases`).
3. Click **Add Database Connection** (or **New Database**).
4. Fill in the connection details:
   - **Connection Name**: `Outside Enterprise Corp`
   - **Host**: `host.docker.internal` (if leafyGo is in Docker) or `localhost` (if leafyGo runs locally)
   - **Port**: `5438`
   - **Database Name**: `outside_corp`
   - **Username**: `outside_admin`
   - **Password**: `outside_secret_123`
   - **SSL Mode**: `disable`
5. Save the connection.

### 2. Grant Group Access
1. Go to **Platform Admin** > **Groups** (`/admin/groups`).
2. Pick or create a group (e.g. `backend` or `data`).
3. Assign access to `Outside Enterprise Corp`:
   - `View Only` (for introspection and read-only query testing)
   - `View + Edit` (to test running migrations and schema edits)
4. Ensure your test user belongs to this group.

### 3. Verify leafyGo Capabilities

#### A. Database Viewer & Schema Introspection
- Open the database viewer for `Outside Enterprise Corp`.
- Switch between the `public` and `logistics` schemas.
- Inspect tables:
  - `public.suppliers`: check rating constraint (`CHECK (rating >= 1.00 AND rating <= 5.00)`), custom enum `supplier_tier`, and indexes.
  - `public.warehouses`: inspect `metadata` JSONB column and GIN index.
  - `public.products`: foreign key to suppliers, price check.
  - `public.inventory_records`: composite primary key `(warehouse_id, product_id)`.
  - `logistics.carriers`: active status, tracking url template.
  - `logistics.shipments`: foreign keys with `ON DELETE RESTRICT`, custom enum `shipment_status`.
  - `logistics.shipment_events`: timeline tracking events.
- Inspect views:
  - `public.v_low_stock_products` (standard view)
  - `logistics.mv_shipment_summary` (materialized view)
- Verify data browsing & table pagination.

#### B. SQL Query Console
- Navigate to the Query Console for `Outside Enterprise Corp`.
- Execute test read queries:
  ```sql
  SELECT p.name, p.sku, s.name as supplier, p.unit_price, p.attributes->>'voltage' as voltage
  FROM products p
  JOIN suppliers s ON p.supplier_id = s.id;
  ```
  ```sql
  SELECT * FROM logistics.mv_shipment_summary;
  ```
  ```sql
  SELECT s.tracking_number, c.name as carrier, s.status, s.destination_city
  FROM logistics.shipments s
  JOIN logistics.carriers c ON s.carrier_id = c.id
  ORDER BY s.created_at DESC;
  ```

#### C. Migration Service & Schema Updates
- Switch to the Migrations tab for `Outside Enterprise Corp`.
- Create a new migration draft, for example:
  ```sql
  ALTER TABLE public.products ADD COLUMN warranty_months INTEGER DEFAULT 12;
  CREATE INDEX idx_products_warranty ON public.products (warranty_months);
  ```
- Run **EXPLAIN** to review the execution plan safely.
- Execute the migration and verify linear execution in the Migration History.
- Verify that `warranty_months` now appears in the table viewer!
