# Migration Failure, Retry & Checkpoint Resume — Visual Testing Guide

This helper lets you trigger a **controlled mid-flight batch migration failure** so you can visually test:
1. Live progress polling climbing through batches 1–5
2. The **Failure UI** (`partial` status, `partial_hold` lock warning, error callout, and `Retry batches` / `Revert batches` actions)
3. The **Retry mechanism** and **resuming from checkpoint** without re-running already committed batches

It defaults to the seeded **`People`** database (`people` on `targets-db`) because **`rina`** (`password123`) is the sole Group Leader of `hr` — so you can create, submit, approve, retry, and revert using a single browser session.

---

## How It Works

- `setup` creates two tables in `people`:
  - `failure_demo_items`: 1,000 rows (`id` 1..1000, `counter = 0`)
  - `failure_demo_control`: single control row `(id=1, fail_divisor=0, fail_after_id=550)`
- The batch migration runs in **10 batches of 100 rows** with a `0.8s` sleep per batch so you can watch the UI progress bar move in real time.
- **Why dry-run passes:** Pre-review dry-run binds `cursor_from = 1, cursor_to = 0` (0 rows matched), so the `1 / 0` branch is never hit during dry-run.
- **Why execution fails at batch 6:** Batches 1–5 (`id 1..500`) commit and write checkpoints to `_leafygo_batch_checkpoints`. Batch 6 (`id 501..600`) hits `id = 551 > 550`, evaluates `1 / 0`, and fails with PostgreSQL `SQLSTATE 22012` (division by zero). Batch 6 rolls back cleanly and the DB enters `partial_hold`.

---

## Step-by-Step Walkthrough (PowerShell)

Make sure the stack is running (`docker compose up -d`).

### 1. Prepare target tables & create the migration draft
```powershell
.\scripts\failure-demo\failure-demo.ps1 setup
.\scripts\failure-demo\failure-demo.ps1 create
```
*(Prefer creating the migration manually in the UI? Run `.\scripts\failure-demo\failure-demo.ps1 sql` to print the SQL & batch config.)*

### 2. Trigger the failure & inspect the Failure UI
1. Open the URL printed by `create` (sign in as **`rina`** / **`password123`**).
2. Click **Submit proposal** (dry-run will pass).
3. Click **Approve** (enqueues the job immediately).
4. Open the **Job Monitor** and watch:
   - Live polling updates the progress bar: `1/10` → `2/10` → `3/10` → `4/10` → `5/10` (50%, 500 rows, Last cursor `500`).
   - On batch 6, the job turns amber (**`partial`**), shows the **`Partial hold`** callout, displays **`Last error`** (`division by zero`, `SQLSTATE 22012`), and reveals **`Retry batches`** and **`Revert batches`**.

### 3. Verify checkpoint state in the database
```powershell
.\scripts\failure-demo\failure-demo.ps1 status
```
You should see:
- `counter = 1`: 500 rows (`id 1..500`)
- `counter = 0`: 500 rows (`id 501..1000`)
- `_leafygo_batch_checkpoints`: batches 1–5 committed by the first execution.

### 4. Fix the failure & test Retry / Checkpoint Resume
```powershell
.\scripts\failure-demo\failure-demo.ps1 fix
```
Back in the **Job Monitor** UI:
1. Click **Retry batches** → enter a reason → **Request batch retry**.
2. Click **Approve** on the retry proposal.
3. Watch the new job: it **skips batches 1–5** and resumes directly at **batch 6 → 10**, reaching `10/10` (`100%`, `success`) and releasing `partial_hold`.

### 5. Prove batches 1–5 were NOT re-run
```powershell
.\scripts\failure-demo\failure-demo.ps1 status
```
You should see:
- `counter = 1`: **all 1,000 rows** (`id 1..1000`) — zero rows with `counter = 2`!
- `_leafygo_batch_checkpoints`: batches 1–5 have `written_by = <exec 1>`, batches 6–10 have `written_by = <exec 2>`.

---

## Extra Scenarios You Can Test

- **Fail again during retry (multi-stage checkpoint resume):**
  Instead of `fix`, run `.\scripts\failure-demo\failure-demo.ps1 fail-later 850` before clicking **Retry batches**. Batches 6–8 commit, batch 9 fails (`8/10`, `80%`), then run `fix` and retry one more time to finish batches 9–10!
- **Retry without fixing:**
  Click **Retry batches** before running `fix` to watch the retry fail immediately at batch 6 while preserving `partial_hold`.
- **Revert from partial hold:**
  After failing at batch 6, click **Revert batches** instead of Retry to watch the worker roll back batches 5 → 4 → 3 → 2 → 1 using `sql_down`.
- **Reset & run again:**
  ```powershell
  .\scripts\failure-demo\failure-demo.ps1 reset
  .\scripts\failure-demo\failure-demo.ps1 create
  ```
- **Clean up demo tables when done:**
  ```powershell
  .\scripts\failure-demo\failure-demo.ps1 cleanup
  ```
