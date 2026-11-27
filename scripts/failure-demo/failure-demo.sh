#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-help}"
FAIL_AFTER_ID="${2:-550}"

API_BASE="${API_BASE:-http://localhost:8080}"
USERNAME="${LEAFYGO_USER:-rina}"
PASSWORD="${LEAFYGO_PASSWORD:-password123}"
CONNECTION_NAME="${LEAFYGO_DB_NAME:-People}"
COMPOSE_SERVICE="${COMPOSE_SERVICE:-targets-db}"
PG_USER="${PG_USER:-leafygo_app}"
PG_DB="${PG_DB:-people}"
BATCH_SLEEP_SEC="${BATCH_SLEEP_SEC:-0.8}"

run_psql() {
  docker compose exec -T "$COMPOSE_SERVICE" psql -v ON_ERROR_STOP=1 -U "$PG_USER" -d "$PG_DB"
}

show_help() {
  cat <<'EOF'
leafyGo Migration Failure & Checkpoint Resume Demo Helper

Usage:
  ./scripts/failure-demo/failure-demo.sh <action> [fail_after_id]

Actions:
  setup                Create failure_demo_items (1000 rows) + failure_demo_control (armed for batch 6)
  create               Create the batch migration draft in leafyGo UI via API (requires curl + jq)
  status               Inspect control flag, row counter distribution, and _leafygo_batch_checkpoints
  fix                  Disarm failure (fail_divisor = 1) so Retry resumes cleanly from checkpoint
  fail-later [id]      Keep failure armed, move threshold (default 850 -> fail again at batch 9)
  break [id]           Re-arm failure (fail_divisor = 0, default 550 -> batch 6)
  reset                Reset all item counters to 0 and re-arm failure
  cleanup              Drop failure_demo_items and failure_demo_control tables
  sql                  Print SQL Up / SQL Down / Batch Config for manual copy-paste
EOF
}

case "$ACTION" in
  setup)
    run_psql <<EOF
DROP TABLE IF EXISTS failure_demo_items;
DROP TABLE IF EXISTS failure_demo_control;

CREATE TABLE failure_demo_control (
    id            int PRIMARY KEY,
    fail_divisor  int NOT NULL,
    fail_after_id bigint NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

INSERT INTO failure_demo_control (id, fail_divisor, fail_after_id)
VALUES (1, 0, ${FAIL_AFTER_ID});

CREATE TABLE failure_demo_items (
    id         bigint PRIMARY KEY,
    label      text NOT NULL,
    counter    int NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO failure_demo_items (id, label, counter)
SELECT g, 'item-' || lpad(g::text, 4, '0'), 0
FROM generate_series(1, 1000) AS g;
EOF
    echo "Ready! 1,000 rows created. Failure armed for id > ${FAIL_AFTER_ID}."
    ;;

  create)
    command -v jq >/dev/null 2>&1 || { echo "jq is required for 'create'. Run 'sql' instead to copy-paste." >&2; exit 1; }
    COOKIE_JAR="$(mktemp)"
    trap 'rm -f "$COOKIE_JAR"' EXIT

    CSRF="$(curl -sS -c "$COOKIE_JAR" -X POST "$API_BASE/api/v1/auth/login" \
      -H "Content-Type: application/json" \
      -d "{\"username\":\"$USERNAME\",\"password\":\"$PASSWORD\"}" | jq -r '.csrf_token')"

    DB_ID="$(curl -sS -b "$COOKIE_JAR" "$API_BASE/api/v1/databases" | \
      jq -r --arg name "$CONNECTION_NAME" '.databases[] | select((.name | ascii_downcase) == ($name | ascii_downcase)) | .id' | head -n1)"

    if [[ -z "$DB_ID" ]]; then
      echo "Database '$CONNECTION_NAME' not found for user '$USERNAME'." >&2
      exit 1
    fi

    SQL_UP="SELECT pg_sleep(${BATCH_SLEEP_SEC});
UPDATE failure_demo_items
SET counter = counter + CASE
        WHEN id > (SELECT fail_after_id FROM failure_demo_control WHERE id = 1)
            THEN 1 / (SELECT fail_divisor FROM failure_demo_control WHERE id = 1)
        ELSE 1
    END,
    updated_at = now()
WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};"

    SQL_DOWN="SELECT pg_sleep(0.5);
UPDATE failure_demo_items
SET counter = counter - 1,
    updated_at = now()
WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};"

    STAMP="$(date +%H%M%S)"
    BODY="$(jq -n \
      --arg title "Demo: Partial Batch Failure & Resume ($STAMP)" \
      --arg desc "Updates failure_demo_items in 10 batches of 100. Fails at batch 6 while fail_divisor = 0, then resumes from checkpoint after fix." \
      --arg up "$SQL_UP" \
      --arg down "$SQL_DOWN" \
      '{
        title: $title,
        description: $desc,
        sql_up: $up,
        sql_down: $down,
        is_irreversible: false,
        execution_mode: "batch",
        batch_config: {
          table_name: "failure_demo_items",
          cursor_col: "id",
          batch_size: 100,
          updated_at_col: "updated_at"
        }
      }')"

    MIG_ID="$(curl -sS -b "$COOKIE_JAR" -X POST "$API_BASE/api/v1/databases/$DB_ID/migrations" \
      -H "Content-Type: application/json" \
      -H "X-CSRF-Token: $CSRF" \
      -d "$BODY" | jq -r '.id')"

    echo "Migration created! Open in browser:"
    echo "  $API_BASE/databases/$DB_ID/migrations/$MIG_ID"
    echo "Sign in as: $USERNAME / $PASSWORD"
    ;;

  fix)
    run_psql <<< "UPDATE failure_demo_control SET fail_divisor = 1, updated_at = now() WHERE id = 1;"
    echo "Failure DISARMED (fail_divisor = 1). Retry in the UI will now succeed from checkpoint."
    ;;

  break)
    run_psql <<< "UPDATE failure_demo_control SET fail_divisor = 0, fail_after_id = ${FAIL_AFTER_ID}, updated_at = now() WHERE id = 1;"
    echo "Failure ARMED (fail_divisor = 0, fail_after_id = ${FAIL_AFTER_ID})."
    ;;

  fail-later)
    THRESH="${2:-850}"
    run_psql <<< "UPDATE failure_demo_control SET fail_divisor = 0, fail_after_id = ${THRESH}, updated_at = now() WHERE id = 1;"
    echo "Failure ARMED for later batch (fail_divisor = 0, fail_after_id = ${THRESH})."
    ;;

  reset)
    run_psql <<EOF
UPDATE failure_demo_items SET counter = 0, updated_at = now();
UPDATE failure_demo_control SET fail_divisor = 0, fail_after_id = ${FAIL_AFTER_ID}, updated_at = now() WHERE id = 1;
EOF
    echo "Reset complete."
    ;;

  cleanup)
    run_psql <<< "DROP TABLE IF EXISTS failure_demo_items; DROP TABLE IF EXISTS failure_demo_control;"
    echo "Dropped failure_demo_items and failure_demo_control."
    ;;

  status)
    echo "=== Failure Demo Control ==="
    run_psql <<< "SELECT id, fail_divisor, CASE WHEN fail_divisor = 0 THEN 'ARMED (will fail)' ELSE 'FIXED (will succeed)' END AS state, fail_after_id, updated_at FROM failure_demo_control;"
    echo "=== Row Counter Distribution (failure_demo_items) ==="
    run_psql <<< "SELECT counter, count(*) AS rows, min(id) AS min_id, max(id) AS max_id FROM failure_demo_items GROUP BY counter ORDER BY counter;"
    echo "=== Latest Batch Checkpoints ==="
    run_psql <<< "SELECT left(root_execution_id::text, 8) AS root_exec, batch_no, cursor_from, cursor_to, rows_affected AS rows, left(written_by_execution_id::text, 8) AS written_by, CASE WHEN reverted_at IS NULL THEN 'active' ELSE 'reverted' END AS state FROM _leafygo_batch_checkpoints WHERE root_execution_id = (SELECT root_execution_id FROM _leafygo_batch_checkpoints ORDER BY committed_at DESC LIMIT 1) ORDER BY batch_no ASC;" || true
    ;;

  sql)
    cat <<EOF
Batch Config:
  table_name     : failure_demo_items
  cursor_col     : id
  batch_size     : 100
  updated_at_col : updated_at

SQL Up:
SELECT pg_sleep(${BATCH_SLEEP_SEC});
UPDATE failure_demo_items
SET counter = counter + CASE
        WHEN id > (SELECT fail_after_id FROM failure_demo_control WHERE id = 1)
            THEN 1 / (SELECT fail_divisor FROM failure_demo_control WHERE id = 1)
        ELSE 1
    END,
    updated_at = now()
WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};

SQL Down:
SELECT pg_sleep(0.5);
UPDATE failure_demo_items
SET counter = counter - 1,
    updated_at = now()
WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};
EOF
    ;;

  *)
    show_help
    ;;
esac
