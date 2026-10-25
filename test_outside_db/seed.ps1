# Helper script to execute/re-apply seed.sql against leafygo-outside-db
$ErrorActionPreference = "Stop"

Write-Host "Re-applying seed.sql to outside-db..." -ForegroundColor Cyan

$containerName = "leafygo-outside-db"
$dbUser = "outside_admin"
$dbName = "outside_corp"

$running = docker ps --filter "name=$containerName" --format "{{.Names}}"
if (-not $running) {
    Write-Warning "Container $containerName is not running. Starting with docker compose up -d..."
    docker compose up -d
    Start-Sleep -Seconds 3
}

Get-Content -Raw "$PSScriptRoot/seed.sql" | docker exec -i $containerName psql -U $dbUser -d $dbName

Write-Host "Seeding complete!" -ForegroundColor Green
