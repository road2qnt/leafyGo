<#
.SYNOPSIS
  Interactive helper for visually testing migration failure UI, retry mechanism,
  and resuming from batch checkpoints in leafyGo.

.EXAMPLE
  .\scripts\failure-demo\failure-demo.ps1 setup
  .\scripts\failure-demo\failure-demo.ps1 create
  .\scripts\failure-demo\failure-demo.ps1 status
  .\scripts\failure-demo\failure-demo.ps1 fix
  .\scripts\failure-demo\failure-demo.ps1 fail-later 850
  .\scripts\failure-demo\failure-demo.ps1 reset
  .\scripts\failure-demo\failure-demo.ps1 cleanup
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("setup", "create", "fix", "break", "fail-later", "status", "reset", "cleanup", "sql", "help")]
    [string]$Action = "help",

    [Parameter(Position = 1)]
    [int]$FailAfterId = 550,

    [string]$ApiBase = "http://localhost:8080",
    [string]$Username = "rina",
    [string]$Password = "password123",
    [string]$ConnectionName = "People",
    [string]$ComposeService = "targets-db",
    [string]$PgUser = "leafygo_app",
    [string]$PgDatabase = "people",
    [double]$BatchSleepSec = 0.8
)

$ErrorActionPreference = "Stop"

$SqlUp = @"
SELECT pg_sleep($BatchSleepSec);
UPDATE failure_demo_items
SET counter = counter + CASE
        WHEN id > (SELECT fail_after_id FROM failure_demo_control WHERE id = 1)
            THEN 1 / (SELECT fail_divisor FROM failure_demo_control WHERE id = 1)
        ELSE 1
    END,
    updated_at = now()
WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};
"@

$SqlDown = @"
SELECT pg_sleep(0.5);
UPDATE failure_demo_items
SET counter = counter - 1,
    updated_at = now()
WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};
"@

function Invoke-TargetPsql {
    param([Parameter(Mandatory)][string]$Sql)
    $Sql | docker compose exec -T $ComposeService psql -v ON_ERROR_STOP=1 -U $PgUser -d $PgDatabase
    if ($LASTEXITCODE -ne 0) {
        throw "psql failed against service '$ComposeService' (db='$PgDatabase', user='$PgUser'). Is 'docker compose up' running?"
    }
}

function Show-Help {
    Write-Host ""
    Write-Host "leafyGo Migration Failure & Checkpoint Resume Demo Helper" -ForegroundColor Cyan
    Write-Host "=========================================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "Usage:" -ForegroundColor Yellow
    Write-Host "  .\scripts\failure-demo\failure-demo.ps1 <action> [options]"
    Write-Host ""
    Write-Host "Actions:" -ForegroundColor Yellow
    Write-Host "  setup                Create failure_demo_items (1000 rows) + failure_demo_control (armed to fail at batch 6)"
    Write-Host "  create               Create the batch migration draft in leafyGo UI via API (default user: rina / People DB)"
    Write-Host "  status               Inspect control flag, row counter distribution, and _leafygo_batch_checkpoints"
    Write-Host "  fix                  Disarm failure (fail_divisor = 1) so Retry resumes cleanly from checkpoint"
    Write-Host "  fail-later [id]      Keep failure armed, move threshold (e.g. 850 -> fail again at batch 9 on retry)"
    Write-Host "  break [id]           Re-arm failure (fail_divisor = 0, default threshold 550 -> batch 6)"
    Write-Host "  reset                Reset all item counters to 0 and re-arm failure at id > 550"
    Write-Host "  cleanup              Drop failure_demo_items and failure_demo_control tables"
    Write-Host "  sql                  Print the SQL Up / SQL Down / Batch Config for manual copy-paste in the UI"
    Write-Host ""
    Write-Host "Typical visual walkthrough:" -ForegroundColor Green
    Write-Host "  1. .\scripts\failure-demo\failure-demo.ps1 setup"
    Write-Host "  2. .\scripts\failure-demo\failure-demo.ps1 create"
    Write-Host "  3. Open leafyGo in browser, sign in as rina / password123, open People -> Migrations"
    Write-Host "  4. Submit proposal -> Approve -> Watch progress bar climb to 5/10 (50%) and fail at batch 6!"
    Write-Host "  5. .\scripts\failure-demo\failure-demo.ps1 status   (see id 1..500 updated, 501..1000 untouched, 5 checkpoints)"
    Write-Host "  6. .\scripts\failure-demo\failure-demo.ps1 fix"
    Write-Host "  7. In the Job Monitor UI, click 'Retry batches' -> Submit -> Approve -> Watch it resume at batch 6..10!"
    Write-Host "  8. .\scripts\failure-demo\failure-demo.ps1 status   (all 1000 rows have counter = 1, zero double-applied!)"
    Write-Host ""
}

function Invoke-Setup {
    Write-Host "Setting up failure demo tables in '$PgDatabase' (service: $ComposeService)..." -ForegroundColor Cyan
    $sql = @"
DROP TABLE IF EXISTS failure_demo_items;
DROP TABLE IF EXISTS failure_demo_control;

CREATE TABLE failure_demo_control (
    id            int PRIMARY KEY,
    fail_divisor  int NOT NULL,
    fail_after_id bigint NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

INSERT INTO failure_demo_control (id, fail_divisor, fail_after_id)
VALUES (1, 0, $FailAfterId);

CREATE TABLE failure_demo_items (
    id         bigint PRIMARY KEY,
    label      text NOT NULL,
    counter    int NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO failure_demo_items (id, label, counter)
SELECT g, 'item-' || lpad(g::text, 4, '0'), 0
FROM generate_series(1, 1000) AS g;
"@
    Invoke-TargetPsql $sql
    Write-Host "Ready! 1,000 rows created in failure_demo_items." -ForegroundColor Green
    Write-Host "Failure armed: fail_divisor = 0 for id > $FailAfterId (batches 1..5 commit, batch 6 fails with SQLSTATE 22012)." -ForegroundColor Yellow
}

function Invoke-CreateMigration {
    Write-Host "Logging into $ApiBase as '$Username'..." -ForegroundColor Cyan

    $loginBody = @{ username = $Username; password = $Password } | ConvertTo-Json
    $loginWebResp = Invoke-WebRequest `
        -Uri "$ApiBase/api/v1/auth/login" `
        -Method Post `
        -ContentType "application/json" `
        -Body $loginBody `
        -UseBasicParsing

    $loginResp = $loginWebResp.Content | ConvertFrom-Json
    $csrf = $loginResp.csrf_token
    if (-not $csrf) {
        throw "Login succeeded but csrf_token was missing in response."
    }

    $setCookie = @($loginWebResp.Headers["Set-Cookie"]) -join "; "
    if ($setCookie -notmatch 'sid=([^;,\s]+)') {
        throw "Login succeeded but 'sid' session cookie was missing in Set-Cookie header."
    }
    $sid = $Matches[1]

    $uri = [Uri]$ApiBase
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $cookie = New-Object System.Net.Cookie("sid", $sid, "/", $uri.Host)
    $session.Cookies.Add($uri, $cookie)

    $authHeaders = @{
        "X-CSRF-Token" = $csrf
    }

    $dbsResp = Invoke-RestMethod `
        -Uri "$ApiBase/api/v1/databases" `
        -Method Get `
        -Headers $authHeaders `
        -WebSession $session

    $targetDb = $dbsResp.databases | Where-Object { $_.name -ieq $ConnectionName } | Select-Object -First 1
    if (-not $targetDb) {
        $avail = ($dbsResp.databases | ForEach-Object { $_.name }) -join ", "
        throw "Database '$ConnectionName' not found for user '$Username'. Available: $avail"
    }

    $stamp = Get-Date -Format "HHmmss"
    $payload = @{
        title           = "Demo: Partial Batch Failure & Resume ($stamp)"
        description     = "Updates failure_demo_items in 10 batches of 100. Fails at batch 6 while failure_demo_control.fail_divisor = 0, then resumes from checkpoint after fix."
        sql_up          = $SqlUp
        sql_down        = $SqlDown
        is_irreversible = $false
        execution_mode  = "batch"
        batch_config    = @{
            table_name     = "failure_demo_items"
            cursor_col     = "id"
            batch_size     = 100
            updated_at_col = "updated_at"
        }
    } | ConvertTo-Json -Depth 5

    $created = Invoke-RestMethod `
        -Uri "$ApiBase/api/v1/databases/$($targetDb.id)/migrations" `
        -Method Post `
        -ContentType "application/json" `
        -Headers $authHeaders `
        -Body $payload `
        -WebSession $session

    Write-Host ""
    Write-Host "Migration definition created!" -ForegroundColor Green
    Write-Host "  Database : $($targetDb.name) ($($targetDb.id))"
    Write-Host "  Migration: $($created.title) ($($created.id))"
    Write-Host "  Sign in  : $Username / $Password"
    Write-Host ""
    Write-Host "Open in browser:" -ForegroundColor Cyan
    Write-Host "  $ApiBase/databases/$($targetDb.id)/migrations/$($created.id)" -ForegroundColor White
    Write-Host ""
    Write-Host "Next steps in UI:" -ForegroundColor Yellow
    Write-Host "  1. Click 'Submit proposal' (dry-run will PASS automatically)"
    Write-Host "  2. Click 'Approve' as $Username (sole Group Leader on $ConnectionName)"
    Write-Host "  3. Watch the live Job Monitor climb to 5/10 batches (50%) and fail on batch 6!"
    Write-Host "  4. Run: .\scripts\failure-demo\failure-demo.ps1 fix"
    Write-Host "  5. Click 'Retry batches' -> Submit -> Approve -> Watch it resume at batch 6..10!"
    Write-Host ""
}

function Invoke-Fix {
    Invoke-TargetPsql "UPDATE failure_demo_control SET fail_divisor = 1, updated_at = now() WHERE id = 1;"
    Write-Host "Failure DISARMED (fail_divisor = 1). Retrying in the UI will now resume from the last checkpoint and succeed!" -ForegroundColor Green
}

function Invoke-Break {
    param([int]$Threshold)
    Invoke-TargetPsql "UPDATE failure_demo_control SET fail_divisor = 0, fail_after_id = $Threshold, updated_at = now() WHERE id = 1;"
    $batchNo = [math]::Floor($Threshold / 100) + 1
    Write-Host "Failure ARMED (fail_divisor = 0, fail_after_id = $Threshold -> will fail on batch $batchNo)." -ForegroundColor Yellow
}

function Invoke-Reset {
    $sql = @"
UPDATE failure_demo_items SET counter = 0, updated_at = now();
UPDATE failure_demo_control SET fail_divisor = 0, fail_after_id = $FailAfterId, updated_at = now() WHERE id = 1;
"@
    Invoke-TargetPsql $sql
    Write-Host "Reset complete: all 1,000 rows set to counter = 0, failure armed at id > $FailAfterId." -ForegroundColor Green
}

function Invoke-Cleanup {
    Invoke-TargetPsql "DROP TABLE IF EXISTS failure_demo_items; DROP TABLE IF EXISTS failure_demo_control;"
    Write-Host "Dropped failure_demo_items and failure_demo_control." -ForegroundColor Green
}

function Invoke-Status {
    Write-Host "=== Failure Demo Control ===" -ForegroundColor Cyan
    Invoke-TargetPsql "SELECT id, fail_divisor, CASE WHEN fail_divisor = 0 THEN 'ARMED (will fail)' ELSE 'FIXED (will succeed)' END AS state, fail_after_id, updated_at FROM failure_demo_control;"

    Write-Host "=== Row Counter Distribution (failure_demo_items) ===" -ForegroundColor Cyan
    Invoke-TargetPsql "SELECT counter, count(*) AS rows, min(id) AS min_id, max(id) AS max_id FROM failure_demo_items GROUP BY counter ORDER BY counter;"

    Write-Host "=== Latest Batch Checkpoints (_leafygo_batch_checkpoints) ===" -ForegroundColor Cyan
    $cpSql = @"
DO `$`$
BEGIN
    IF to_regclass('public._leafygo_batch_checkpoints') IS NULL THEN
        RAISE NOTICE 'No _leafygo_batch_checkpoints table yet (no batch migration has run on this DB).';
    END IF;
END `$`$;
SELECT
    left(root_execution_id::text, 8) AS root_exec,
    batch_no,
    cursor_from,
    cursor_to,
    rows_affected AS rows,
    left(written_by_execution_id::text, 8) AS written_by,
    CASE WHEN reverted_at IS NULL THEN 'active' ELSE 'reverted' END AS state
FROM _leafygo_batch_checkpoints
WHERE root_execution_id = (
    SELECT root_execution_id
    FROM _leafygo_batch_checkpoints
    ORDER BY committed_at DESC
    LIMIT 1
)
ORDER BY batch_no ASC;
"@
    try {
        Invoke-TargetPsql $cpSql
    } catch {
        Write-Host "(No checkpoints recorded yet on this database.)" -ForegroundColor DarkGray
    }
}

function Show-Sql {
    Write-Host "=== Batch Config ===" -ForegroundColor Cyan
    Write-Host "Table Name     : failure_demo_items"
    Write-Host "Cursor Column  : id"
    Write-Host "Batch Size     : 100"
    Write-Host "Updated At Col : updated_at"
    Write-Host ""
    Write-Host "=== SQL Up ===" -ForegroundColor Cyan
    Write-Host $SqlUp
    Write-Host ""
    Write-Host "=== SQL Down ===" -ForegroundColor Cyan
    Write-Host $SqlDown
}

switch ($Action) {
    "setup"      { Invoke-Setup }
    "create"     { Invoke-CreateMigration }
    "fix"        { Invoke-Fix }
    "break"      { Invoke-Break -Threshold $FailAfterId }
    "fail-later" {
        $t = if ($PSBoundParameters.ContainsKey("FailAfterId")) { $FailAfterId } else { 850 }
        Invoke-Break -Threshold $t
    }
    "status"     { Invoke-Status }
    "reset"      { Invoke-Reset }
    "cleanup"    { Invoke-Cleanup }
    "sql"        { Show-Sql }
    default      { Show-Help }
}
